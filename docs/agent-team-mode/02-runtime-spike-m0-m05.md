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
| feature flag 草案 | `experimental.agent_team_preview`, `experimental.agent_team` |
| package 草案 | `internal/team` 包结构和接口草图 |
| ActorContext 草案 | workspace/team/member/task/run/session/tool identity |
| risk gate | M0.5 不通过，不进入 M3 runtime 产品化 |

### 退出条件

- 所有里程碑范围清楚。
- M1 演示内容清楚。
- M2/M3 需要的 DB/API/runtime 边界清楚。
- 开发 issue 可以按 M1/M2/M3 拆分。

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
  -> build TurnRunner from existing agent factory
  -> start goroutine
  -> mark idle

Send
  -> enqueue prompt in in-memory mailbox
  -> wake runner

MemberRunner loop
  -> wait prompt/cancel/stop
  -> mark running
  -> build minimal SessionAgentCall
  -> call TurnRunner.Run(SessionAgentCall)
  -> flush messages
  -> mark idle/failed/stopped
```

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
| 独立 session | mate 使用独立 session，不复用 leader session |
| leader 不阻塞 | mate run 不阻塞 leader run |
| busy 不吞 prompt | mate busy 时 prompt 要么排队，要么明确拒绝 |
| cancel 可恢复 | cancel current turn 后 mate 可继续接新 prompt |
| stop 可 flush | stop 时 cancel/wait/FlushAll |
| shutdown 不泄漏 | app shutdown 后无 mate goroutine 泄漏 |
| busy 不污染 | `AgentIsBusy` 不因为 mate running 变成 true |
| queued case 正确 | `Run` queued case 不被误标完成 |

## M0.5 到 M1/M2 的分界

M0.5 验证 runtime 可行性；M1 验证产品价值和安全底座；M2 才建 durable team domain。

不要把 M0.5 spike 直接扩成产品功能。通过后应沉淀接口和测试，再进入正式实现。
