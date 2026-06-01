# 04 Team Domain Data Contract

## 整合来源

- Notion：完整 `internal/team.Service`、`team_mates`、`team_mailbox_messages`、
  `team_audit_events`、`team_event_outbox`。
- 当前 repo：分期引入 `teams/team_members/team_tasks/team_runs/team_events/team_session_links`，
  并强调 idempotency、SSE replay、成本预算。

## 取舍

采用 Notion 的字段完整度，采用当前 repo 的分期策略。

命名需要在 M0 冻结：`team_mates` 与 `team_members` 二选一。本文建议实现层使用
`team_members`，延续当前 repo 文档；对外概念仍可称 mate/teammate。

## M2 第一批表

```text
teams
team_members
team_tasks
team_runs
team_events 或 team_event_outbox
team_session_links
```

说明：

- `team_runs` 保留，因为 Crush 需要 run attempt、heartbeat、cancel、recovery。
- `team_events` 与 `team_event_outbox` 可以先合并为一个 outbox 风格表，字段包含
  `seq/published_at/payload_json`。
- `team_session_links` 避免改动现有 `sessions/messages` 语义。
- M2 即引入 team/member/run 级 token/cost budget 字段。

## M3 第二批表

```text
team_mailbox_messages
team_message_receipts 或 delivered/read 字段
team_task_dependencies
team_artifacts
```

说明：

- mailbox 必须有 recipient、kind、correlation_id、delivered/read。
- task update 必须带 `expected_version`。
- artifact 用于 M5 patch write。

## M4/M5 第三批表

```text
team_audit_events
team_file_locks
team_file_leases
team_worktrees
team_external_agents
```

说明：

- audit 可以 M2 建表，M4 强制覆盖所有 permission/tool event。
- file lock/lease 不应早于 patch artifact，否则写路径风险过大。
- `team_external_agents` 留给 M6 A2A gateway。

## Service 接口

```go
type Service interface {
    CreateTeam(ctx context.Context, req CreateTeamRequest) (Team, error)
    GetTeam(ctx context.Context, teamID string) (TeamSnapshot, error)
    ListTeams(ctx context.Context, workspaceID string) ([]Team, error)
    ArchiveTeam(ctx context.Context, teamID string, expectedVersion int64) error

    SpawnMember(ctx context.Context, req SpawnMemberRequest) (Member, error)
    UpdateMemberState(ctx context.Context, req UpdateMemberStateRequest) (Member, error)
    StopMember(ctx context.Context, req StopMemberRequest) error
    ListMembers(ctx context.Context, teamID string) ([]Member, error)

    AppendMailbox(ctx context.Context, req AppendMailboxRequest) (MailboxMessage, error)
    ListUnread(ctx context.Context, teamID, memberID string, limit int) ([]MailboxMessage, error)
    MarkDelivered(ctx context.Context, ids []string, deliveredAt int64) error
    MarkRead(ctx context.Context, ids []string, readAt int64) error

    CreateTask(ctx context.Context, req CreateTaskRequest) (TeamTask, error)
    UpdateTask(ctx context.Context, req UpdateTaskRequest) (TeamTask, error)
    ClaimNextTask(ctx context.Context, req ClaimNextTaskRequest) (TeamTask, error)
    ListTasks(ctx context.Context, teamID string, filter TaskFilter) ([]TeamTask, error)

    StartRun(ctx context.Context, req StartRunRequest) (TeamRun, error)
    HeartbeatRun(ctx context.Context, runID string) error
    FinishRun(ctx context.Context, req FinishRunRequest) error
    CancelRun(ctx context.Context, runID string) error

    AppendAudit(ctx context.Context, req AuditEvent) error
    DebugSnapshot(ctx context.Context, teamID string) (DebugSnapshot, error)
}
```

## 事务规则

- domain row、audit、outbox 同事务。
- LLM call 不在事务内。
- permission wait 不持有事务。
- claim/update 使用 atomic SQL 或 CAS。
- event publish 失败不回滚 DB。
- 每个写操作都带 idempotency key 或明确可重试语义。

## CAS 错误格式

Task update 冲突返回结构应稳定，方便模型恢复：

```json
{
  "error": "version_conflict",
  "current_version": 4,
  "current_task": {
    "id": "task_01",
    "status": "in_progress"
  }
}
```

模型看到冲突后必须重新 `team_task_list` 或 `team_task_get`，不能盲目重试。

