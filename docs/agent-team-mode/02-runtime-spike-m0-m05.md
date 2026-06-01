# 02 M0 / M0.5 Runtime Spike

## 整合来源

- Notion Phase 0：先验证长期 mate goroutine 的 start/send/cancel/stop/flush。
- 当前 repo M0：冻结文档、feature flag、ActorContext 草案、风险登记。

## 为什么需要 M0.5

当前 repo 原路线从 M1 read-only delegates 开始，能验证并行调研价值，但不能验证
真正 AgentTeam 的核心风险：

- 长期 mate 是否能稳定持有 session。
- `SessionAgent.Run` 是否能被 MateRunner loop 驱动。
- `SessionAgent.Run` 返回 `nil, nil` queued case 是否会被误判完成。
- cancel current turn 后 mate 是否还能继续接新任务。
- app shutdown 是否能 stop runner 并 flush messages。
- leader session 是否会被 mate busy 状态污染。

因此新增隐藏阶段 M0.5，不暴露给普通用户。

## M0 交付

- 冻结术语：team、leader、mate、delegate、runtime、task、mailbox。
- 确认 feature flag：`experimental.agent_team_preview=false`。
- 明确旧 `/agent` tool 不改语义。
- 明确 `currentAgent` 不承载 team runtime。
- 建立 `internal/team` 空包或接口草案。
- 建立风险门禁。

## M0.5 交付

最小接口：

```go
StartMate(profile, sessionID)
Send(mateID, prompt)
CancelCurrentTurn(mateID)
StopMate(mateID)
Status(mateID)
```

最小组件：

```text
internal/team/models.go
internal/team/runner.go
internal/team/mate_runner.go
internal/team/inmemory_mailbox.go
```

可以先用 in-memory mailbox：

```go
type InMemoryMailbox struct {
    ch chan TeamPrompt
}
```

## 不做

- DB mailbox。
- task board。
- permission bridge。
- 多 mate 产品化。
- TUI 大面板。
- A2A。

## 验收

- mate 有独立 session。
- mate run 不阻塞 leader。
- send -> mate receives -> run one turn。
- busy mate 不吞 prompt。
- cancel current turn 后 mate 可继续。
- stop 时 cancel/wait/flush。
- app shutdown 不漏 goroutine。
- leader `IsBusy/Cancel/QueuedPrompts` 不受 mate 污染。

M0.5 不通过，不进入长期 TeamRunner 产品化。

