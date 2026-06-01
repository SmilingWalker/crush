# 05 Runtime Control Plane

## 整合来源

- Notion：TeamRunner、MateRunner、Mate state machine、cancel/shutdown 区分。
- 当前 repo：DelegateRunner、AgentRegistry、ReadyGate、RunContext、heartbeat、coordinator 拆分。

## 取舍

- M1 保留 DelegateRunner，作为 read-only preview。
- M2 引入 AgentRegistry，为 coder 与 teammate runtime 统一建模。
- M3 引入 TeamRunner/MateRunner，承载长期 teammate。

## 核心组件

```text
agent.Coordinator
  -> AgentRegistry
  -> AgentBuilder
  -> TeamToolRuntime interface

team.Service
team.TeamRunner
team.MateRunner
team.PermissionBridge
```

`agent` 不应直接 import `team`。`Coordinator` 通过窄接口接入 team tools，避免循环依赖
和 `coordinator.go` 膨胀。

## TeamRunner 职责

```go
type TeamRunner interface {
    StartTeam(ctx context.Context, teamID string) error
    StopTeam(ctx context.Context, teamID string, mode StopMode) error
    SpawnMember(ctx context.Context, teamID string, memberID string) error
    StopMember(ctx context.Context, teamID, memberID string, mode StopMode) error
    CancelMemberTurn(teamID, memberID string) error
    Status(teamID string) TeamRuntimeStatus
}
```

## MateRunner Loop

```text
start
  -> load member session
  -> mark idle
  -> wait unread mailbox / assigned task / wakeup / cancel / shutdown
  -> build prompt envelope
  -> mark running
  -> SessionAgent.Run
  -> flush messages
  -> mark idle / blocked / failed / stopped
  -> repeat
```

关键规则：

- `SessionAgent.Run` 返回 `nil, nil` queued case 时，不得标记 task completed。
- `StopMember` 必须 stop wakeup loop、cancel current run、wait、FlushAll。
- `CancelMemberTurn` 只取消当前 turn，不删除 member。
- `StopTeam` 广播 shutdown request，超时后强制 cancel。
- app shutdown 必须先 stop TeamRunner，再关闭 DB。

## Member State Machine

```text
created
  -> starting
  -> idle
  -> queued
  -> running
  -> waiting_permission
  -> blocked / idle
  -> canceling_turn
  -> shutting_down
  -> stopped / failed
```

状态更新要求：

- 每次状态变化写 `team_members.version + 1`。
- 每次变化写 team event/outbox。
- TUI reducer 按 `team_id + member_id + seq` 幂等处理。
- 发现 seq gap 时拉 snapshot。

## Cancel 语义

| 动作 | 语义 |
| --- | --- |
| `CancelMemberTurn` | 取消当前 turn，member 仍存在 |
| `ClearMemberQueue` | 清 pending prompt/wakeup |
| `StopMember` | graceful shutdown，不接新任务 |
| `StopTeam` | stop/cancel team 下所有 member |

现有 `AgentCoordinator.Cancel(sessionID)` 不足以作为 team cancel，因为它不知道 team/member/run
状态，也不会写 team event。

## Heartbeat

保留当前 repo 方案：

- `team_runs.heartbeat_at`。
- streaming callback 每 30-60s 更新一次。
- 不要每个 text delta 都写 DB。
- startup recovery 标记 heartbeat 过期 run 为 `interrupted`。
- retry policy 决定是否重新入队。

## AgentRegistry 与 ReadyGate

M2 引入：

```go
type AgentRuntime struct {
    ID     string
    Spec   AgentSpec
    Agent  SessionAgent
    Ready  ReadyGate
    Status csync.Value[AgentStatus]
}
```

`AgentStatus` 只表达 runtime 启停状态：`starting/idle/stopping/stopped/failed`。
run 级状态放 `team_runs`。

