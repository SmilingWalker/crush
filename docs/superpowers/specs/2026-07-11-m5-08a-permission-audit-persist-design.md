# M5-08a: Permission Audit 落盘

> 日期：2026-07-11
> 里程碑：M4（Permission Bridge + Audit）
> 范围：audit 落盘 + 补齐已触发 action 的字段

## 背景

`PermissionBridge` 通过 `PermAuditFunc` 回调发权限审计事件，但 `app.go` 的 `SetAuditFunc` 实现只写 `slog.Info`——进程重启即丢，且无法按 team/member/task 查询。DB 表 `team_audit_events`、`AuditStore.AppendAudit`、`ListAudit` 查询接口都已就绪，但 permission 流程从不调用。audit 安全能力名义上有、实际是空壳。

产品化定位（server/client 多用户、合规、事故复盘）要求 audit 必须落盘。

## 目标

1. 把 `SetAuditFunc` 从纯 slog 改为写 `team_audit_events` 表
2. 补全 `PermAuditEvent` 缺失字段（WorkspaceID / SessionID / ToolCallID），让落盘记录完整可用
3. 保证已触发的 6 种 audit action（grant_auto / permission_allowed / permission_denied / permission_expired / permission_canceled / permission_orphaned）字段完整

## 非目标（defer）

- M5-08b：补 `permission_requested` / `late_response` / `hook_allow` / `hook_deny` 四种未触发 action
- M5-09：`hooked_tool.go` 的 hook audit 接线（目前只写 slog）
- 给 `team_audit_events` 加新列

## 设计决策

| 决策 | 选择 | 理由 |
|------|------|------|
| 事务边界 | auditFunc 内自开短事务 | bridge 保持 DB-agnostic；audit 与权限决策不共享事务 |
| 落盘失败语义 | best-effort（slog.Error，不影响决策） | audit 是观测数据，不应阻塞实时权限交互 |
| 字段补全 | 给 PermAuditEvent 加 WorkspaceID/SessionID/ToolCallID | 保证落盘记录完整，不留半空字段 |
| DecidedBy 存储 | 塞 Summary（"decided_by:user"） | 不改表结构；M5-08a 不引入 migration |
| Resource 分类 | ResourceType="tool" + ResourceRef=ToolName | 语义清晰，将来可扩 "bash"/"file" |

## 改动点

### 1. `internal/team/permission_bridge.go` — 补全 PermAuditEvent 字段

给 `PermAuditEvent` 加：
```go
type PermAuditEvent struct {
    WorkspaceID string  // 新增
    SessionID   string  // 新增
    ToolCallID  string  // 新增
    // ...既有字段不变
    Action    PermAuditAction
    TeamID    string
    MemberID  string
    TaskID    string
    RunID     string
    ToolName  string
    Resource  string
    Decision  string
    Scope     string
    DecidedBy string
    Timestamp time.Time
}
```

在现有 6 个 `auditFn` 调用点补字段（这些上下文里都已有信息）：
- `permission_bridge.go:309`（grant_auto）— 来自 `opts`（SessionID/ToolCallID）和 `ac`（actor context）
- `permission_fsm.go:88/101/125/140/155`（5 个 FSM action）— 来自 `permReq`（已有 TeamID/MemberID/SessionID/ToolCallID）

### 2. `internal/team/permission_bridge.go` — 新增导出转换函数

```go
// PermEventToAuditEvent converts a PermAuditEvent to an AuditEvent row.
func PermEventToAuditEvent(e PermAuditEvent) AuditEvent {
    action := string(e.Action)
    summary := ""
    if e.DecidedBy != "" {
        summary = "decided_by:" + e.DecidedBy
    }
    return AuditEvent{
        ID:           uuid.New().String(),
        WorkspaceID:  e.WorkspaceID,
        TeamID:       e.TeamID,
        MemberID:     strPtrOrNil(e.MemberID),
        TaskID:       strPtrOrNil(e.TaskID),
        RunID:        strPtrOrNil(e.RunID),
        SessionID:    strPtrOrNil(e.SessionID),
        ToolCallID:   strPtrOrNil(e.ToolCallID),
        EventType:    "permission." + action,
        Action:       strPtrOrNil(action),
        ResourceType: strPtrOrNil("tool"),
        ResourceRef:  strPtrOrNil(e.ToolName),
        Decision:     strPtrOrNil(e.Decision),
        Scope:        strPtrOrNil(e.Scope),
        Summary:      strPtrOrNil(summary),
        CreatedAt:    e.Timestamp,
    }
}
```

放在 permission_bridge.go（与 PermAuditEvent 同文件），用既有 `strPtrOrNil` helper。

### 3. `internal/app/app.go` — auditFunc 改为落盘

`app.go:179` 的 `SetAuditFunc` 闭包改为：
```go
app.permBridge.SetAuditFunc(func(ctx context.Context, e team.PermAuditEvent) {
    tx, err := app.db.BeginTx(ctx, nil)
    if err != nil {
        slog.Error("Failed to begin audit tx", "action", e.Action, "error", err)
        return
    }
    defer tx.Rollback() // best-effort: rollback if commit not reached
    if err := app.auditStore.AppendAudit(ctx, tx, team.PermEventToAuditEvent(e)); err != nil {
        slog.Error("Failed to persist permission audit",
            "action", e.Action, "team_id", e.TeamID, "error", err)
        return
    }
    if err := tx.Commit(); err != nil {
        slog.Error("Failed to commit permission audit", "action", e.Action, "error", err)
    }
})
```

需要在 `app.App` struct 上存 `auditStore AuditStore` 引用（当前 line 199 创建后没存）。`WorkspaceID` 在 auditFunc 闭包里捕获（app 已有 workspace 概念，或从 config 拿——需确认 app 层 workspaceID 的获取方式）。

### 4. WorkspaceID 来源

`PermissionBridge` 目前不知道 workspaceID。两个选项：
- **选项 A（推荐）**：给 `NewPermissionBridge` 加 workspaceID 参数，bridge 在触发 auditFn 时填入 PermAuditEvent.WorkspaceID
- 选项 B：在 app.go 的 auditFunc 闭包里捕获 workspaceID，填进 PermAuditEvent

选 A——让 bridge 持有 workspaceID，和它持有 TeamID 的模式一致，auditFn 调用点不用每个都手动填。

## 不改的东西

- `PermissionBridge` / `PermissionFSM` 的核心权限逻辑不变
- `AppendAudit` / `AuditEvent` / `AuditStore` 接口不变
- `team_audit_events` 表结构不变（不加 migration）
- 测试中 auditFunc 仍是 mock（不依赖真实 DB）

## 测试策略

### 单元测试（permission_bridge_test.go）

用捕获型 `PermAuditFunc`，验证各路径触发的 `PermAuditEvent`：
- 新增 `TestPermEventToAuditEvent` — 验证字段映射（EventType 前缀、ResourceType/Ref、Summary 的 decided_by、空字符串→NULL）
- 既有测试补充断言：`WorkspaceID` / `SessionID` / `ToolCallID` 非空

### 落盘测试（app_team_test.go 或新文件）

用真实 SQLite（`:memory:`，`SetMaxOpenConns(1)`），验证：
- auditFunc 触发后 `ListAudit` 能查到对应行
- 字段完整（EventType = "permission.xxx"，action/tool/team 对得上）
- 落盘失败不影响权限决策（mock 一个失败的 store，验证 Request 仍返回正确）

## 验收条件

- [ ] `SetAuditFunc` 写 `team_audit_events` 表（不只 slog）
- [ ] 6 种已触发 action 的 PermAuditEvent 字段完整（含 WorkspaceID/SessionID/ToolCallID）
- [ ] audit 落盘失败只 slog.Error，不影响 allow/deny 决策
- [ ] `ListAudit` 能查到 permission audit 行
- [ ] `go build ./...` 通过
- [ ] `go test ./internal/team/... ./internal/app/...` 通过
