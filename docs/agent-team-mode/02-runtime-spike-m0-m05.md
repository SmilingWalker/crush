# 02 M0 / M0.5 Runtime Spike

M0 和 M0.5 的目标不是发布功能，而是证明长期 member runtime 能和当前 `SessionAgent`
安全共存。这个阶段失败，后续 M2/M3 都不能推进。

## M0：方案冻结与边界对齐

### 目标

- 冻结术语、目录、feature flag 和模块边界。
- 明确旧 `/agent` tool 不变。
- 明确 TeamRuntime 不进入 `Coordinator.currentAgent`。
- 明确 M1 是 read-only delegates，不是长期 teammate。
- 明确 M3 才出现真实 mailbox/team loop。

### 交付物

| 交付物 | 内容 |
| --- | --- |
| 文档冻结 | 本目录文档成为正式实现口径 |
| feature flag 草案 | `Options.Experimental.AgentTeamPreview`, `Options.Experimental.AgentTeam` |
| package 草案 | `internal/team` 包结构和接口草图 |
| ActorContext 草案 | `internal/actor.ActorContext`，包含 workspace/team/member/task/run/session/tool identity |
| risk gate | M0.5 不通过，不进入 M3 runtime 产品化 |

### 退出条件

- 所有里程碑范围清楚。
- M1 演示内容清楚。
- M2/M3 需要的 DB/API/runtime 边界清楚。
- 开发 issue 可以按 M1/M2/M3 拆分。
- feature flag 结构已落到 `internal/config.Options.Experimental`，默认 nil/false，且 team API flag-off 返回
  `feature_disabled`。

## M0.5：隐藏 Runtime Spike

### 为什么要做

旧路线从 M1 read-only delegates 开始，能演示并行调研，但不能回答真正 TeamRuntime 的
核心风险：

- 长期 teammate goroutine 能否稳定持有 session。
- `SessionAgent.Run` 能否由 `MemberRunner` loop 驱动。
- busy/queued/cancel/stop/flush 是否语义清楚。
- app shutdown 是否会泄漏 goroutine。
- teammate busy 是否会污染 leader busy。

### 范围

只做 hidden spike，不暴露给普通用户：

```text
internal/team/models.go
internal/team/runner.go
internal/team/member_runner.go
internal/team/inmemory_mailbox.go
internal/team/runtime_spike_test.go
```

最小接口：

```go
type SpikeRuntime interface {
    StartMember(ctx context.Context, spec MemberSpec) (MemberRuntimeID, error)
    Send(ctx context.Context, memberID MemberRuntimeID, prompt string) error
    CancelCurrentTurn(ctx context.Context, memberID MemberRuntimeID) error
    StopMember(ctx context.Context, memberID MemberRuntimeID) error
    Status(ctx context.Context, memberID MemberRuntimeID) MemberRuntimeStatus
}
```

### 实现思路

```text
StartMember
  -> create/load child session
  -> build independent TurnRunner/SessionAgent from AgentFactory
  -> start goroutine
  -> mark idle

Send
  -> enqueue prompt in in-memory mailbox
  -> wake runner

MemberRunner loop
  -> wait prompt/cancel/stop
  -> mark running
  -> build minimal TeamAgentCall
  -> call TurnRunner.Run(TeamAgentCall)
  -> flush messages through injected message flusher
  -> mark idle/failed/stopped
```

M0.5 必须验证 `AgentFactory` 创建的是新的 `SessionAgent` 实例，而不是复用
`Coordinator.currentAgent`。验证重点：

- member runner 的 `tools`、`models`、`messageQueue`、`activeRequests` 与 coder runner 隔离。
- coder 运行中 `SetTools` / `SetModels` 不会改变 member runner 的受限工具和模型策略。
- member cancel 调用自己的 runner，不通过 `Coordinator.Cancel(sessionID)`。
- stop/shutdown 的 flush 通过 message service 注入完成，不要求 `SessionAgent` 暴露 `FlushAll`。

M0.5 spike 不允许直接构造旧 `internal/agent.SessionAgentCall` 给 team runtime 使用。必须先通过
`TeamAgentCallAdapter` 显式映射：

```text
TeamAgentCall
  -> TeamAgentCallAdapter
  -> existing internal/agent.SessionAgentCall
  -> SessionAgent.Run
  -> TurnRunResult
```

当 `SessionAgent.Run` 因 session busy 返回 `(nil, nil)` 时，adapter 必须返回 `TurnQueued`
或等价 sentinel。测试成功标准是：queued turn 不会触发 task completed、不会写 completed run。

M0.5 不依赖 `SessionAgent` 内部 `messageQueue` 来追踪后续真正开始执行的时间；当前代码没有
queued-start callback，调用者无法可靠知道入队 turn 何时被递归执行。实现上优先由
`MemberRunner` 做 single-flight gate：同一 member 正在运行时，新 wakeup 留在 mailbox/local
queue，下一轮由 `MemberRunner` 自己启动。如果 spike 发现必须复用 `SessionAgent.messageQueue`，
则 M0.5 不通过，先补 queued-start lifecycle callback 后再进入 M3。

### 不做

- 不做 DB mailbox。
- 不做 task board。
- 不做 permission bridge。
- 不做 TUI 面板。
- 不做多 teammate 产品化。
- 不做 A2A。

### 验收

| 验收项 | 说明 |
| --- | --- |
| 独立 session | mate 使用独立 child session，不复用 leader session |
| 独立 runner | mate 使用独立 `SessionAgent` 实例，不复用 `Coordinator.currentAgent` |
| 策略隔离 | coder `SetTools`/`SetModels` 不影响 mate runner |
| leader 不阻塞 | mate run 不阻塞 leader run |
| busy 不吞 prompt | mate busy 时 prompt 留在 member queue/mailbox，或明确返回 queued/busy sentinel |
| cancel 可恢复 | cancel current turn 后 mate 可继续接新 prompt |
| stop 可 flush | stop 时 cancel/wait，并通过 message service flush |
| shutdown 不泄漏 | app shutdown 后无 mate goroutine 泄漏 |
| busy 不污染 | `AgentIsBusy` 不因为 mate running 变成 true |
| queued case 正确 | `Run` queued case 返回 `TurnQueued`/sentinel，不写 completed run；无 callback 时不复用内部 queue |

## M0.5 到 M1/M2 的分界

M0.5 验证 runtime 可行性；M1 验证产品价值和安全底座；M2 才建 durable team domain。

不要把 M0.5 spike 直接扩成产品功能。通过后应沉淀接口和测试，再进入正式实现。
