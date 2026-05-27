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

## 8. 测试

单测：

- registry 注册两个 runtime。
- per-agent ready failure。
- `RunAgent(coder)` 与旧 `Run` 行为一致。
- teammate busy 不影响 coder busy。

集成：

- coder 正在运行时，teammate run 可独立运行。
- cancel teammate run 不取消 coder。
- shutdown 调用 `CancelAll` 覆盖全部 runtime。

