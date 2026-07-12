# M5-08b: 补 permission_requested + late_response Audit Action

> 日期：2026-07-12
> 里程碑：M4（Permission Bridge + Audit）
> 范围：给 2 个已声明但未触发的 audit action 加 `auditFn` 调用

## 背景

M5-08a 完成了 audit 落盘（`SetAuditFunc` 写 `team_audit_events`），但 10 种 audit action 中有 4 种只声明未触发。本次处理其中 2 个属于 `PermissionBridge` 内部的：`permission_requested` 和 `late_response`。（另外 2 个 `hook_allow`/`hook_deny` 属于 M5-09，涉及 `hooked_tool.go` 构造函数改动，另开。）

## 目标

1. `permission_requested`：在 `requestWithUI` 入队时触发——记录"team member 发起了权限请求"
2. `late_response`：在 `ResolveRequest` 找不到 entry 时触发——记录"UI 对一个已不在队列里的请求点了 allow/deny"

## 非目标

- M5-09（`hook_allow` / `hook_deny` 接线到 `hooked_tool.go`）——另开
- 区分"late"和"从未存在"——按决策，ResolveRequest 找不到 entry 一律当 late_response 审计（reqID 是程序生成的 UUID，拼写错误极罕见）
- 给 `ResolveRequest` 加 `ctx context.Context` 参数——用 `context.Background()` 代替（audit 是 best-effort fire-and-forget，late 场景下 caller 的 ctx 可能已 cancel）

## 设计决策

| 决策 | 选择 | 理由 |
|------|------|------|
| late 检测 | 不区分，一律当 late | 最简；reqID 是 UUID，拼写错误极罕见 |
| ResolveRequest 的 ctx | 用 `context.Background()` | late 场景下 caller ctx 可能已 cancel；audit 是 fire-and-forget |

## 改动点

### 1. `internal/team/permission_bridge.go` — permission_requested

在 `requestWithUI` 函数内，`b.entries[reqID] = entry` 之后、`b.queue.Enqueue` 之前（约 L378），加 `auditFn` 调用。`now` 变量已在函数内定义（L363 `now := time.Now()`）：

```go
b.auditFn(ctx, PermAuditEvent{
    WorkspaceID: b.workspaceID, SessionID: opts.SessionID, ToolCallID: reqID,
    Action: PermAuditPermissionRequested, TeamID: ac.TeamID, MemberID: ac.MemberID,
    TaskID: ac.TaskID, RunID: ac.RunID, ToolName: opts.ToolName,
    Resource: opts.Path, Timestamp: now,
})
```

字段来源：`opts`（`permission.CreatePermissionRequest`，含 SessionID/ToolName/Action/Path）和 `ac`（`actor.ActorContext`，含 TeamID/MemberID/TaskID/RunID）。

### 2. `internal/team/permission_bridge.go` — late_response

在 `ResolveRequest` 的 `if !ok` 分支（约 L441），当前直接返回 error。改为先 audit 再返回：

```go
if !ok {
    b.queueMu.Unlock()
    slog.Debug("perm_bridge: ResolveRequest not pending (late or unknown)", "tool_call_id", reqID)
    b.auditFn(context.Background(), PermAuditEvent{
        WorkspaceID: b.workspaceID, ToolCallID: reqID,
        Action: PermAuditLateResponse, Timestamp: time.Now(),
    })
    return fmt.Errorf("request not pending: %s", reqID)
}
```

注意：late_response 的 `PermAuditEvent` 只有 WorkspaceID 和 ToolCallID 可填（TeamID/MemberID 等信息已随 entry 删除，无法恢复）。这是可接受的——late_response 的审计价值是"有人对一个已消失的请求做了操作"，reqID 足够追溯到是哪次请求。

### 不改的东西

- `PermissionBridge` struct 不变
- `NewPermissionBridge` 不变
- `PermEventToAuditEvent` 不变（已支持所有 action 类型）
- `ResolveRequest` 签名不变（不加 ctx 参数）

## 测试策略

### `TestPermissionBridge_RequestedAuditFires`

验证 `permission_requested` 在 team member 请求权限时触发。用捕获型 `PermAuditFunc`：
- 构造 bridge，注入捕获型 auditFn
- 构造 team actor context（`actor.ActorContext{TeamID, MemberID, ...}`）
- 用带 timeout 的 ctx 调用 `bridge.Request`（SkipRequests=false，会走到 requestWithUI）
- requestWithUI 会 block 在 `select` 等 UI 决策——用 `context.WithTimeout(ctx, 100ms)` 让它超时退出
- 断言捕获的 audit 事件中有 `permission_requested`，字段完整（WorkspaceID/SessionID/ToolCallID/TeamID/MemberID/ToolName）

### `TestPermissionBridge_LateResponseAudit`

验证 `late_response` 在 resolve 不存在的 reqID 时触发：
- 构造 bridge，注入捕获型 auditFn
- 调用 `bridge.ResolveRequest("nonexistent-id", true, "call")`
- 断言捕获的 audit 事件中有 `late_response`，含 ToolCallID="nonexistent-id"
- 断言 `ResolveRequest` 返回 error

## 验收条件

- [ ] `permission_requested` 在 `requestWithUI` 入队时触发 auditFn
- [ ] `late_response` 在 `ResolveRequest` 找不到 entry 时触发 auditFn
- [ ] 两个新 audit 事件落盘到 `team_audit_events`（通过 M5-08a 的 `PermEventToAuditEvent` + app.go 落盘路径自动生效）
- [ ] `go build ./...` 通过
- [ ] `go test ./internal/team/...` 通过
