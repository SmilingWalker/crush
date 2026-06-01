# 05 Runtime Control Plane

Runtime Control Plane 负责 agent 的创建、运行、取消、恢复和资源控制。它要把现有
`SessionAgent` 包成可管理的长期 member runtime，同时不污染默认 coder。

## 组件关系

```text
agent.Coordinator
  owns default coder only
  exposes AgentFactory / TurnRunner

team.TeamRuntime
  owns TeamRunner instances
  owns MemberRunner lifecycle
  depends on team.Service
  depends on AgentFactory / TurnRunner
```

## AgentRegistry

M2 引入 `AgentRegistry`，先服务 coder 与 team runtime 的统一状态观察，不强行改变旧 API。

```go
type AgentRuntime struct {
    ID       string
    Kind     AgentKind // coder | delegate | team_member
    Spec     AgentSpec
    Session  string
    Status   AgentStatus
    Ready    ReadyGate
    Runner   TurnRunner
}
```

规则：

- 旧 `AgentIsBusy()` 默认只看 coder。
- `AgentRegistry` 可以提供 debug/status，但不替代 team DB state。
- team run 级状态写 `team_runs`，registry 只表达 runtime 是否 alive。

## M1 DelegateRunner

M1 是演示用的一次性并行 delegate，不是长期 teammate。

```go
type DelegateRunner interface {
    RunGroup(ctx context.Context, req DelegateRunGroupRequest) (DelegateRunGroupResult, error)
    CancelGroup(ctx context.Context, groupID string) error
}
```

实现要求：

- 每个 delegate 独立 child session。
- 每个 delegate 使用 read-only tool policy。
- group 最大并发默认 3。
- group cancel 取消所有 active child runs。
- 结果聚合回 leader。
- delegate 之间不互相发消息。

M1 可以用内存 `RunGroup`，不要求 durable team tables。

## M3 TeamRunner

M3 引入长期 TeamRunner。

```go
type TeamRunner interface {
    StartTeam(ctx context.Context, teamID string) error
    StopTeam(ctx context.Context, teamID string, mode StopMode) error
    SpawnMember(ctx context.Context, teamID, memberID string) error
    StopMember(ctx context.Context, teamID, memberID string, mode StopMode) error
    CancelMemberTurn(ctx context.Context, teamID, memberID string) error
    Status(ctx context.Context, teamID string) (TeamRuntimeStatus, error)
}
```

`StopMode`：

```text
graceful   no new work, finish current safe point, flush
cancel     cancel current turn, flush, stop
force      cancel and stop after timeout
```

## MemberRunner loop

```text
start
  -> load team/member/session
  -> mark member starting
  -> build TurnRunner
  -> mark idle
  -> wait for wake source:
       unread mailbox
       assigned task
       explicit wakeup
       cancel current turn
       shutdown
  -> build prompt envelope
  -> start team_run
  -> mark member running
  -> TurnRunner.Run(SessionAgentCall)
  -> flush messages
  -> finish team_run
  -> mark idle/blocked/failed/stopped
  -> repeat
```

`SessionAgentCall` fields are defined in `00-technical-design.md` and are the only supported
team-to-agent call shape. `MemberRunner` must not call `Run(sessionID, prompt)` directly.

Wake source 优先级：

1. shutdown/cancel control。
2. permission response（M4 起启用；M3 只保留 schema 预留）。
3. assigned task。
4. direct mailbox。
5. dependency completion。
6. periodic heartbeat/recovery wake。

M3a 最小 runtime E2E 可以使用 direct mailbox + minimal prompt envelope。PR-8 必须包含
足够的 direct mailbox read/wake path 来证明 one read-only member can wake, run one turn, and
report status；PR-9 再补 broadcast/role、receipts hardening 和完整 prompt envelope。

## 状态机

```text
created
  -> starting
  -> idle
  -> queued
  -> running
  -> waiting_permission
  -> blocked
  -> canceling_turn
  -> shutting_down
  -> stopped
  -> failed
```

状态变化必须：

- 更新 `team_members.version`。
- 写 `team_events`。
- 必要时写 `team_audit_events`。
- publish SSE notification。

## Scheduler

M3a 实现最小 scheduler：

```text
queued task
  -> atomic claim
  -> assigned/running
  -> run heartbeat
  -> completed/failed/interrupted
```

规则：

- `ClaimNextTask` 使用 atomic SQL。
- member `max_concurrent_runs` M3 默认 1。
- run heartbeat 每 30-60 秒一次，不随每个 token delta 写 DB。
- app startup 标记 heartbeat 过期 run 为 `interrupted`。
- retry policy 只重试 retryable task。

## Cancel 语义

| 操作 | 语义 | 影响 |
| --- | --- | --- |
| cancel member turn | 取消当前 run | member 保留，后续可继续 |
| clear member queue | 清未处理 wakeup/prompt | 不删除 mailbox 历史 |
| stop member | graceful shutdown | 不接新任务 |
| stop team | stop/cancel 所有 member | team 进入 stopped/archived |

现有 `AgentCoordinator.Cancel(sessionID)` 不足以作为 team cancel，因为它不写 team run、
member state、audit 和 event。

## Recovery

启动时：

1. load active teams。
2. find members in `running/waiting_permission/queued`。
3. find runs with stale `heartbeat_at`。
4. mark stale runs as `interrupted`。
5. based on task retry policy, requeue or mark blocked。
6. publish snapshot-required event。

## 并发控制

早期默认：

```text
max_team_members = 3
max_concurrent_runs_per_member = 1
max_active_team_runs = 3
max_permission_pending_per_team = 3
```

超过限制时不要悄悄排无限队列，必须返回 structured error，让 leader 调整计划。
