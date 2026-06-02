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

Runner ownership 冻结规则：

- `Coordinator.currentAgent` 只代表默认 coder。team runtime 不复用它，不通过它管理 member
  run lifecycle。
- 每个 M1 delegate 和每个 M3 member runner 都由 `AgentFactory.BuildTeamRunner` 创建新的
  `SessionAgent` 实例；child session 只代表会话身份，不代表可以共享 runner。
- runner 实例内的 `tools`、`models`、`messageQueue`、`activeRequests` 必须和 coder 隔离。
- cancel member/delegate turn 时调用对应 runner 的 `Cancel(sessionID)`，再由 team runtime 写
  run/member/audit/event；不能只调用 `AgentCoordinator.Cancel(sessionID)`。
- flush 是 runtime 的 message service 依赖，不是 `TurnRunner` 的隐式能力。

## AgentRegistry

M2 引入 `AgentRegistry`，先服务 coder 与 team runtime 的统一状态观察，不强行改变旧 API。

```go
type AgentRuntime struct {
    ID       string
    Kind     AgentKind // coder | delegate | team_member
    Spec     AgentSpec
    Session  string
    Status   AgentStatus
    Ready    bool
    ReadyReason string
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
- 每个 delegate 使用独立 `SessionAgent` runner，不复用 coder runner。
- 每个 delegate 使用 read-only tool policy。
- group 最大并发默认 3。
- group cancel 取消所有 active child runs。
- 结果聚合回 leader。
- delegate 之间不互相发消息。
- `DelegateRunGroup` 只保存在内存中；可追踪 child session id、status、cost、error、trace id。
- `DelegateRunner` 的 child session、actor context、tool filtering、cancel/result aggregation 必须按
  M3 `MemberRunner` 可复用的 primitives 设计。

M1 可以用内存 `RunGroup`，不要求 durable team tables，不注册 AgentRegistry runtime，不写
`team_runs` / `team_events`。M1 的产物是 runtime/safety/UI primitives，不是 durable team state。

## M3 TeamRunner

M3 引入长期 TeamRunner。

```go
type TeamRunner interface {
    StartTeam(ctx context.Context, teamID string) error
    StopTeam(ctx context.Context, teamID string, mode StopMode) error
    SpawnMember(ctx context.Context, teamID, memberID string) error
    StopMember(ctx context.Context, teamID, memberID string, mode StopMode) error
    CancelMemberTurn(ctx context.Context, req CancelMemberTurnRequest) error
    Status(ctx context.Context, teamID string) (TeamRuntimeStatus, error)
}

type CancelMemberTurnRequest struct {
    TeamID      string
    MemberID    string
    RequestedBy actor.ActorContext
    Reason      CancelReason // user_requested | leader_requested | shutdown | timeout | system
    Timeout     time.Duration
}
```

`StopMode`：

```text
graceful   no new work, finish current safe point, flush
cancel     cancel current turn, flush, stop
force      cancel and stop after timeout
```

## MemberRunner loop

M3 冻结规则：

- `MemberRunner` 是长期 runtime object，但不是常驻 LLM 调用。它大部分时间处于 idle，
  只有收到 mailbox、task、explicit wakeup 或 recovery wake 时才启动一轮 `team_run`。
- 每个 member 拥有一个可恢复的 child session 和一个独立 `SessionAgent` runner；每一轮模型调用
  必须通过 `TeamAgentCallAdapter` 进入现有 `SessionAgent.Run`，不能裸调 `Run(sessionID, prompt)`。
- M3 默认 `max_concurrent_runs_per_member=1`；同一 member 有新 wakeup 时只合并/排队，不并发
  开第二轮。
- M3 不消费 permission wait/approval；`waiting_permission` 只作为 future state 和 UI/schema 预留，
  真正 permission handling 放 M4。

```text
start
  -> load team/member/session
  -> mark member starting
  -> build independent TurnRunner/SessionAgent
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
  -> TurnRunner.Run(TeamAgentCall)
  -> flush messages through message flusher
  -> finish team_run
  -> mark idle/blocked/failed/stopped
  -> repeat
```

`TeamAgentCall` fields are defined in `00-technical-design.md` and are the only supported
team-to-agent call shape. `MemberRunner` must not call `Run(sessionID, prompt)` directly and
must not bypass `TeamAgentCallAdapter` when reaching the existing `SessionAgent.Run` implementation.

Queued 语义：

- `MemberRunner` 自己维护 single-flight 状态；如果当前 member 已有 active turn，新 wakeup
  保留在 mailbox/local queue，不直接调用 `SessionAgent.Run`。
- 调用前如果 `TurnRunner.IsSessionBusy(sessionID)` 为 true，返回 queued/busy sentinel，不创建
  第二个 running team run。
- 如果 `SessionAgent.Run` 仍返回 `(nil, nil)`，adapter 必须转成 `TurnQueued`。该状态是
  non-terminal，只能表示“本次调用没有真正开始或无法被追踪”，不能写 completed run。
- 当前代码没有 queued-start callback；M3 不依赖 `SessionAgent.messageQueue` 作为 team mailbox。
  若后续需要复用内部 queue，必须先新增明确的 queued-start lifecycle event。

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

`CancelMemberTurn` 冻结合约：

```text
1. resolve active member runtime and active team_run
2. if no active run: append optional audit no-op and return nil
3. transaction:
     CAS member running/waiting_permission/queued -> canceling_turn
     keep team_run status as running/waiting_permission until terminal result
     append member_cancel_requested event
     append audit with requested_by/reason/run_id/session_id
4. call that member runner's Cancel(session_id), never Coordinator.Cancel(session_id)
5. wait up to timeout for MemberRunner turn loop to observe cancellation and flush messages
6. transaction:
     if turn observed canceled: team_run -> canceled, member -> idle/blocked as task policy requires
     if turn already completed before cancel won: keep completed, append cancel_late audit
     if timeout or runner lost: team_run -> interrupted, member -> failed or idle only if runner is not busy
     cancel pending permission requests for that run when M4 is enabled
     append terminal event/audit
7. publish team event / SSE
```

约束：

- `CancelMemberTurn` 是幂等操作；重复 cancel 同一 active run 返回同一 terminal/ongoing 状态，不重复
  写 terminal event。
- cancel current turn 不清 mailbox/task queue，只阻止当前 run 继续；后续 wakeup 仍可让 member 再次工作。
- 如果 cancel requested 后模型/tool loop late return，必须按 run version / terminal state guard 忽略
  late write，最多写 audit/debug；不能把 `interrupted/canceled` 又改回 `completed`。
- M3 还没有 permission resume，但如果 run 处于 `waiting_permission` 预留状态，cancel 必须能把 run
  转 terminal，并让 member 离开等待态。
- UI 看到 `canceling_turn` 时禁用重复 cancel，保留 stop/force stop 操作。

Member graceful shutdown 顺序固定为：

```text
stop accepting wakeups
append member_shutdown_requested event/audit
if current turn exists: cancel current turn
wait with timeout
flush messages through message service
mark active run canceled/interrupted if needed
mark member stopped
append member_stopped event/audit
publish SSE/team event
```

约束：

- shutdown 期间新 mailbox/task 仍写入 durable domain，但不再唤醒该 member；UI 显示 stopped 或
  shutting_down。
- timeout 后允许进入 `force` path，但仍必须尝试 message service flush 并写 audit。
- shutdown 不能删除 mailbox、task、artifact 历史。
- app shutdown 对所有 active member 执行同一顺序；不得只 cancel goroutine 而不写 run/member 状态。

## Recovery

启动时：

1. load active teams。
2. find members in `running/waiting_permission/queued`。
3. find runs with stale `heartbeat_at`。
4. mark stale runs as `interrupted`。
5. based on task retry policy, requeue or mark blocked。
6. publish snapshot-required event。

M3 recovery 冻结规则：

- `running` 且 heartbeat 超时的 run 标记 `interrupted`，`usage_status` 按 partial/unknown 规则写入。
- `waiting_permission` 在 M3 不恢复等待；启动后标记 `interrupted` 或 `blocked`，并提示 M4 才支持
  permission resume。
- member 如果没有 active run 且仍 active，恢复为 `idle`；若 task retry policy 不允许重试，恢复为
  `blocked`。
- 未读 direct mailbox、queued/assigned task 和 dependency completion 会重新触发 wakeup。
- recovery 不重放普通 assistant output；只依据 durable team state、mailbox、task、run、event。
- stale recovery 必须写 event/audit，并让 UI 通过 snapshot reload 看见。

## 并发控制

早期默认：

```text
max_team_members = 3
max_concurrent_runs_per_member = 1
max_active_team_runs = 3
max_permission_pending_per_team = 3
```

超过限制时不要悄悄排无限队列，必须返回 structured error，让 leader 调整计划。
