# 00 当前代码约束

本文件记录 Agent Team Mode 设计必须尊重的现有代码边界。它不是路线图，而是后续拆分实现的硬约束。

## 1. Workspace/API 入口仍是单 agent

当前 `internal/workspace/workspace.go` 中 `Workspace` 只有：

```go
AgentRun(ctx context.Context, sessionID, prompt string, attachments ...message.Attachment) error
AgentCancel(sessionID string)
AgentIsBusy() bool
AgentIsSessionBusy(sessionID string) bool
```

client/server 路径：

```text
ClientWorkspace.AgentRun
  -> client.SendMessage
  -> POST /v1/workspaces/{id}/agent
  -> server.handlePostWorkspaceAgent
  -> backend.SendMessage
  -> ws.AgentCoordinator.Run
```

约束：

- `proto.AgentMessage` 没有 `agent_id/team_id/member_id/task_id/run_id`。
- `/v1/workspaces/{id}/agent` 当前语义就是默认 coder。
- Team API 必须新增独立路径，不应复用旧 `/agent` 做多 agent 路由。

实现要求：

- M1 可以不改 Workspace 接口，只在本地 app/TUI 中做 preview runner。
- M2 新增 `TeamWorkspace` 扩展接口，TUI 用 type assertion 接入。
- 旧 `Workspace.AgentRun` 保持兼容。

## 2. `AgentCoordinator` 是隐式 singleton

当前 `internal/agent/coordinator.go` 关键结构：

```go
type coordinator struct {
    currentAgent SessionAgent
    agents       map[string]SessionAgent
    readyWg      errgroup.Group
}
```

已确认事实：

- `NewCoordinator` 只构建 `config.AgentCoder`。
- `Run` 总是调用 `c.currentAgent.Run(...)`。
- `CancelAll` 当前只调用 `c.currentAgent.CancelAll()`。
- `UpdateModels` 只更新 `currentAgent`。
- `IsBusy/Model/QueuedPrompts` 都隐式读取 `currentAgent`。
- `agents map` 只是预留，不是可用 registry。
- `readyWg` 是 coordinator 级全局等待门。

实现要求：

- M1 不强行重构 coordinator，优先旁路 `DelegateRunner`。
- M2 才引入 `AgentRegistry`。
- 旧 `IsBusy/Model/QueuedPrompts` 默认只看 coder，不能被 teammate 状态污染。
- `CancelAll` 在 M2 后必须 registry-wide。

## 3. `SessionAgent` 队列不能当 team scheduler

当前 `internal/agent/agent.go`：

- `activeRequests` 是 `sessionID -> context.CancelFunc`。
- `messageQueue` 是 `sessionID -> []SessionAgentCall`。
- `Run` 在检查 busy 后，还会读取 session/message、生成 title、创建 user message，直到后面才写入 `activeRequests`。
- `PrepareStep` 会把 queued prompt 合并进当前模型 step。
- run 结束后再递归处理剩余 queue。

约束：

- `activeRequests` 不是 CAS mutex。
- `messageQueue` 不是 durable queue。
- queue 不是严格 FIFO 的 task scheduler。
- `Cancel(sessionID)` 不等价于 `CancelRun(runID)`。

实现要求：

- M1 delegate 用独立 child session，降低同 session 并发风险。
- M2/M3 team scheduler 必须以 DB `team_tasks/team_runs` 为事实源。
- 内存只保存 `run_id -> cancel func`。

## 4. session schema 有 parent，但普通列表只看 top-level

当前 `sessions` 有 `parent_session_id`。

`ListSessions` SQL：

```sql
SELECT ...
FROM sessions
WHERE parent_session_id is NULL
ORDER BY updated_at DESC
```

约束：

- child session 默认不出现在普通 session list。
- 现有 agent tool child session id 使用 `messageID$$toolCallID`。
- `CreateTaskSession(ctx, toolCallID, parentSessionID, title)` 会直接用传入 `toolCallID` 作为 session id。

实现要求：

- team run session 不复用 `messageID$$toolCallID` 语义。
- durable team session 通过 `team_session_links` 映射。
- Team UI 自己查 team session tree，不改普通 `ListSessions`。

## 5. pubsub/SSE 只能当通知

当前 `internal/pubsub/events.go` 只有既有 payload type：

```go
PayloadTypePermissionRequest
PayloadTypePermissionNotification
PayloadTypeMessage
PayloadTypeSession
PayloadTypeAgentEvent
...
```

client `SubscribeEvents` 对 unknown payload type 只 warn 并继续。

约束：

- 可以安全新增 `PayloadTypeTeamEvent`。
- pubsub/SSE 不是可靠事实源。
- team 状态必须先落 SQLite，再 publish SSE。

实现要求：

- M2 加 `team_events(seq)` outbox。
- TUI reconnect 使用 snapshot + `events?since_seq=`。
- SSE event 只触发 refetch 或增量刷新。

## 6. permission 当前缺 actor 归因

当前 `internal/permission/permission.go`：

```go
type CreatePermissionRequest struct {
    SessionID string
    ToolCallID string
    ToolName string
    Description string
    Action string
    Params any
    Path string
}
```

持久授权 key：

```go
SessionID + ToolName + Action + Path
```

约束：

- UI 无法知道哪个 teammate、哪个 task、哪个 run 发起请求。
- `allow_session` 对长期 teammate 过宽。
- `hook allow` 是 permission prompt 的旁路。
- 很多 read-only tools 不产生 permission request。

实现要求：

- M2 加 `ActorContext`，字段 optional。
- M3 加 scoped grant。
- M3 加 `ToolExecutionEvent` 覆盖所有工具，不只 permission prompt。

## 7. config 当前没有 experimental flag

当前 `config.Options` 有 `Debug`、`DisabledTools`、`DisabledSkills` 等，但没有 `experimental` 分组。

实现要求：

- D 线先新增配置结构，例如：

```go
type ExperimentalOptions struct {
    AgentTeamPreview bool `json:"agent_team_preview,omitempty"`
    AgentTeam        bool `json:"agent_team,omitempty"`
}
```

- 默认值必须 false。
- flag off 时不改变 UI、API、prompt 和 tools。

