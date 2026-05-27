# 03 A 线：Runtime Control Plane

A 线负责多个 agent 的创建、运行、取消、恢复。它分 M1 旁路验证和 M2 正式 registry 两步。

## 1. 当前问题

当前 `AgentCoordinator` 的核心假设：

- 一个 workspace 默认一个 `currentAgent`。
- `Run/Cancel/IsBusy/Model/QueuedPrompts` 都指向 `currentAgent`。
- `readyWg` 是 coordinator 级全局 barrier。
- `SessionAgent` busy/queue 只按 `sessionID`。

这些假设不能直接承载长期 teammate。

## 2. M1：DelegateRunner

M1 不改大架构。

要求：

- 从 leader session 派生多个 child session。
- 每个 child session 跑一次 read-only delegate。
- 运行状态保存在内存 run group。
- group cancel 取消所有 active child runs。

文件：

```text
internal/agent/delegate_runner.go
internal/agent/delegate_types.go
```

非目标：

- 不注册长期 runtime。
- 不做 DB scheduler。
- 不改变 `Coordinator.Run`。

## 3. M2：AgentRegistry

M2 引入正式 runtime registry。

建议结构：

```go
type AgentRuntime struct {
    ID     string
    Spec   AgentSpec
    Agent  SessionAgent
    Ready  ReadyGate
    Status csync.Value[AgentStatus]
}

type AgentRegistry interface {
    Register(ctx context.Context, spec AgentSpec) (*AgentRuntime, error)
    Get(agentID string) (*AgentRuntime, bool)
    List() []*AgentRuntime
    RunAgent(ctx context.Context, req AgentRunRequest) (*fantasy.AgentResult, error)
    Refresh(ctx context.Context, agentID string) error
    CancelRun(runID string)
    CancelSession(agentID, sessionID string)
    CancelAll()
}
```

`Coordinator` 改造：

```go
type coordinator struct {
    defaultAgentID string
    registry       AgentRegistry
    builder        *AgentBuilder
}
```

兼容要求：

- `Run` 等价于 `RunAgent(defaultAgentID)`.
- `AgentIsBusy` 只看 coder。
- `Cancel(sessionID)` 只取消 coder session。
- `CancelAll` registry-wide。

## 4. AgentBuilder

把当前 `buildAgent/buildTools/buildAgentModels` 收束到 builder。

目标接口：

```go
type AgentBuilder interface {
    Build(ctx context.Context, spec AgentSpec) (*AgentRuntime, error)
    Refresh(ctx context.Context, runtime *AgentRuntime) error
}
```

`buildAgentModels` 改造要求：

- 当前只按全局 large/small 选模型。
- teammate 需要按 `AgentSpec.Model` 或 member model override。
- small model 可继续用于 title/summary。

`buildTools` 改造要求：

```go
type ToolBuildOptions struct {
    AgentID      string
    TeamID       string
    IsSubAgent   bool
    AllowedTools []string
    AllowedMCP   map[string][]string
    HookMode     HookMode
    SkillPolicy  SkillPolicy
    MCPPolicy    MCPPolicy
}
```

## 5. ReadyGate

当前 `readyWg` 是全局。目标改为 per-agent：

```go
type ReadyGate interface {
    Wait() error
    Resolve(error)
}
```

要求：

- `RunAgent` 只等待目标 agent。
- teammate 构建失败不影响 coder。
- MCP/tool refresh 只更新相关 runtime。

## 6. RunContext

`SessionAgentCall` 增加：

```go
RunContext AgentRunContext
```

`RunAgent` 负责填充：

```go
type AgentRunContext struct {
    TeamID    string
    AgentID   string
    MemberID  string
    TaskID    string
    RunID     string
    SessionID string
}
```

进入 `SessionAgent.Run` 后写入 tool context。

## 7. Cancel 语义

M2/M3 后必须区分：

```text
CancelSession(agentID, sessionID)
CancelRun(runID)
CancelAll()
```

team scheduler 只能依赖 `CancelRun(runID)`。

## 8. Heartbeat 机制

M3a 引入 heartbeat 用于检测 interrupted run。

实现要求：

- `team_runs` 表包含 `heartbeat_at` 字段。
- 在 `SessionAgent.Run` 的 streaming 回调（`OnTextDelta`、`OnToolCall`）中触发 heartbeat 更新。
- 频率：每 30-60 秒更新一次。
- 使用独立 ticker goroutine 或在 streaming 回调中判断距离上次更新是否超过 30s。
- 不要在每次 text delta 时都写 DB，避免写热点。

Startup recovery 使用 heartbeat：

- 查询 `status IN ('running','starting','waiting_tool') AND heartbeat_at < now - interval`。
- 过期的 run 标记为 `interrupted`。
- 按 retry policy 重新入队。

不使用独立 heartbeat goroutine 的原因：

- 每个 run 一个 ticker goroutine 浪费资源。
- streaming 回调已经在 run 生命周期内，天然可用。
- 回调失败时 heartbeat 自然停止更新，满足"no heartbeat = possibly dead"语义。

## 9. Member 并发度控制

M2/M3a 需要控制单个 member 同时运行的 run 数量。

实现：

- `team_members` 表增加 `max_concurrent_runs INTEGER DEFAULT 1`。
- scheduler claim task 前检查 member 当前 active run 数。
- active run 数从 `team_runs WHERE member_id=? AND status IN ('queued','starting','running','waiting_tool','waiting_permission')` 查询。
- 如果 active run 数 >= max_concurrent_runs，跳过该 member。

默认值：

- M1/M2 默认 `max_concurrent_runs=1`，避免并发复杂度。
- M3a 可按需放开。

## 10. Coordinator 大小管理

`coordinator.go` 当前已 1279 行。引入 team 相关逻辑后需避免进一步膨胀。

策略：

- team 工具注册逻辑提取到 `internal/agent/team_adapter.go`。
- `team_adapter.go` 持有 `TeamToolRuntime` 接口，负责 team 工具的构建和注册。
- coordinator 只通过 `SetTeamRuntime(TeamToolRuntime)` 接收接口，不直接引用 team 包。
- 新增 `internal/agent/builder.go` 收束 `buildAgent/buildTools/buildAgentModels`。

包依赖方向保持不变：`agent` 不 import `team`。

## 11. AgentStatus 语义

`AgentRuntime.Status` 使用 `csync.Value[AgentStatus]`，但需要明确语义范围：

Status 只表示 agent 自身的启动/停止状态：

```text
starting   // 正在构建（buildAgent/buildTools）
idle       // 构建完成，可接受 run
stopping   // 正在关闭
stopped    // 已关闭
failed     // 构建失败
```

run 级别的状态（running/queued/waiting_tool/waiting_permission）放在 `team_runs` 表，不在 AgentStatus 中。

原因：

- 一个 agent 可能同时跑多个 task run。
- `running` 在 agent 级别语义模糊（哪个 run？）。
- agent 级别只需要知道"是否准备好接受新 run"。

## 12. 测试

单测：

- registry 注册两个 runtime。
- per-agent ready failure。
- `RunAgent(coder)` 与旧 `Run` 行为一致。
- teammate busy 不影响 coder busy。
- heartbeat 更新频率正确（streaming 回调触发）。
- member 并发度控制（max_concurrent_runs=1 时第二个 task 排队）。

集成：

- coder 正在运行时，teammate run 可独立运行。
- cancel teammate run 不取消 coder。
- shutdown 调用 `CancelAll` 覆盖全部 runtime。
- heartbeat 过期后 startup recovery 正确标记 interrupted。
- coordinator 行数增长受控（< 1400 行）。

