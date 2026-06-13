# M5: 权限审批 — 开发任务拆分

> 里程碑：M5 | 任务数：10 | 总工时：16 人天 | 建议周期：3-4 周
> 依赖：M4 (MemberRunner, ActorContext) + M3 (team tables)

---

## M5-01: PermissionBridge

**工时**: 2 天 | **依赖**: M4-01, 现有 `permission.Service`

### 涉及文件

- `internal/team/permission_bridge.go`（新建）

### 核心设计

```go
// PermissionBridge 是现有 permission.Service 的 team-aware wrapper
// 不创建第二套权限系统
type PermissionBridge struct {
    inner      permission.Service
    store      *PermissionStore
    grantStore *GrantStore
    auditFn    AuditFunc
}

func (b *PermissionBridge) Request(ctx context.Context, opts permission.CreatePermissionRequest) (bool, error) {
    ac, hasTeam := actor.FromContext(ctx)
    if !hasTeam || ac.TeamID == "" {
        // 非 team session：直接走原有流程
        return b.inner.Request(ctx, opts)
    }

    // Team session：
    // 1. 检查 scoped grant
    if grant, ok := b.grantStore.FindActiveGrant(ctx, ac, opts); ok {
        b.auditFn(ctx, AuditEvent{Action: "grant_auto", ...})
        return true, nil
    }

    // 2. 创建 permission request
    req := PermissionRequest{
        TeamID:     ac.TeamID,
        MemberID:   ac.MemberID,
        TaskID:     ac.TaskID,
        RunID:      ac.RunID,
        Status:     "pending",
    }
    b.store.CreateRequest(ctx, req)

    // 3. 写 audit
    b.auditFn(ctx, AuditEvent{Action: "permission_requested", ...})

    // 4. 发布 waiting_permission 事件
    // 5. 等待 UI 决策（通过 channel 或 pubsub）
    return b.waitDecision(ctx, req.ID)
}
```

### 约束（不可违反）

1. 非 team session 行为完全不变（delegate to inner）
2. `hooked_tool.go` 仍是执行前最终 hook gate
3. Hook deny 阻止执行 + 写 audit
4. Hook allow 写 team audit
5. `allow once` 只 resolve 当前 tool call
6. `allow for task` 创建 team-scoped grant（不调用 `GrantPersistent` 创建 session-wide grant）

### 验收标准

1. 非 team session 行为不变
2. Team session 走 bridge → 显示 member/task/run 信息
3. Hook allow 写 team audit
4. Hook deny 阻止
5. `allow once` / `allow for task` scope 正确

---

## M5-02 ~ M5-03: Permission Tables + Stores

**工时**: 1.5d + 1.5d = 3 天

### team_permission_requests

```sql
CREATE TABLE team_permission_requests (
    id              TEXT PRIMARY KEY,
    workspace_id    TEXT NOT NULL,
    team_id         TEXT NOT NULL REFERENCES teams(id),
    member_id       TEXT NOT NULL,
    task_id         TEXT,
    run_id          TEXT,
    session_id      TEXT,
    tool_call_id    TEXT NOT NULL,
    tool_name       TEXT NOT NULL,
    action          TEXT NOT NULL,
    resource_type   TEXT NOT NULL CHECK (resource_type IN ('file','mcp','bash','network','other')),
    resource_ref    TEXT,
    reason_summary  TEXT,
    status          TEXT NOT NULL DEFAULT 'pending'
                        CHECK (status IN ('pending','allowed','denied','expired','canceled','orphaned')),
    requested_scope TEXT NOT NULL DEFAULT 'call',
    decision_scope  TEXT,
    decision        TEXT,
    decided_by      TEXT CHECK (decided_by IN ('user','system','hook')),
    created_at      INTEGER NOT NULL,
    expires_at      INTEGER,
    decided_at      INTEGER,
    orphaned_at     INTEGER
);
```

### team_permission_grants

```sql
CREATE TABLE team_permission_grants (
    id               TEXT PRIMARY KEY,
    workspace_id     TEXT NOT NULL,
    team_id          TEXT NOT NULL REFERENCES teams(id),
    member_id        TEXT NOT NULL,
    task_id          TEXT,
    session_id       TEXT,
    tool_name        TEXT NOT NULL,
    action           TEXT NOT NULL,
    resource_type    TEXT NOT NULL,
    resource_ref     TEXT,
    scope            TEXT NOT NULL CHECK (scope IN ('call','task','session')),
    source_request_id TEXT NOT NULL,
    expires_at       INTEGER,
    created_at       INTEGER NOT NULL,
    revoked_at       INTEGER
);
```

---

## M5-04: Scoped Grant 状态机

**工时**: 2 天 | **依赖**: M5-02, M5-03

### 状态转换

```
pending ──→ allowed   (用户批准)
       ──→ denied    (用户拒绝)
       ──→ expired   (超时)
       ──→ canceled  (run取消)
       ──→ orphaned  (重启恢复)

allowed ──→ (grant created, scope=call|task|session)
denied  ──→ (member blocked, report alternative)
expired ──→ (member blocked, actions disabled)
```

### 关键逻辑

```go
func (fsm *PermissionFSM) Resolve(ctx context.Context, req ResolveRequest) error {
    // Late response check
    run, _ := fsm.svc.GetRun(ctx, req.RunID)
    if run.IsTerminal() {
        // 只写 audit，不创建 grant
        fsm.auditFn(ctx, AuditEvent{Action: "late_response", Decision: req.Decision})
        return nil
    }

    if req.Decision == "allowed" && req.Scope == "task" {
        fsm.grantStore.CreateGrant(ctx, Grant{
            MemberID: req.MemberID,
            TaskID:   req.TaskID,
            Scope:    "task",
        })
    }
    // ...
}
```

### 验收标准

1. pending→allowed 创建 grant（scope=task）
2. pending→denied → member blocked
3. pending→expired timeout → member blocked
4. pending→canceled run cancel 级联
5. pending→orphaned startup recovery
6. Late response 只写 audit 不创建 grant

---

## M5-05 ~ M5-09: UI、Queue、Late Response、Audit、Hook Audit

| 任务 | 工时 | 核心产出 |
|------|------|----------|
| M5-05 Permission UI | 2.0d | 扩展现有 dialog：显示 member/task/run/tool/path + allow once/for task/deny + 队列 |
| M5-06 Queue + Timeout | 1.5d | maxPending=3, defaultTTL=5min, time.AfterFunc expire, 队列激活 |
| M5-07 Late Response | 1.0d | run.IsTerminal() check，只 audit 不 grant |
| M5-08 Audit 覆盖 | 1.5d | 15种audit action全覆盖，查询 <100ms，不可删除 |
| M5-09 Hook Audit | 1.0d | hooked_tool.go 修改：hook allow/deny team mode 下写 team_audit_events |

---

## M5-10: M5 E2E 测试

**工时**: 2 天

### 测试场景

1. `TestM5_MemberRequestPermission_UserAllows`: member write → pending → allow once → tool proceeds → audit
2. `TestM5_MemberRequestPermission_UserDenies`: member edit → pending → deny → blocked → audit
3. `TestM5_AllowTaskScope`: allow for task → same task auto-approve → different task new request
4. `TestM5_LateResponse`: run completed → late response → no grant → audit only
5. `TestM5_PermissionTimeout`: timer expires → expired → member blocked
6. `TestM5_HookAllowAudit`: hook allow → no prompt → audit hook_allow
7. `TestM5_NonTeamSessionUnchanged`: non-team → existing flow unchanged

---

