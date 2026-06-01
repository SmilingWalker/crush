# 04 Team Domain 与数据契约

M2 的核心是建立 durable team domain。M2 可以不启动长期 teammate，但必须让 team 的
数据、API、event、snapshot 成为后续 M3-M5 的事实源。

## 命名约定

实现层使用 `member`，用户界面和 prompt 可以使用 `teammate` 或 `mate`。

```text
DB/API/internal schema: member
UX/prompt copy: teammate / mate
```

原因：

- `team_members` 与常见 DB 命名一致。
- 用户理解上 `teammate` 更自然。
- prompt 中可以解释：teammate 是用户说法，member 是存储/API 说法。
- runtime 类型也使用 `MemberRunner`、`MemberRuntimeStatus`、`member_runner.go`，避免
  internal 代码出现 `member` / `mate` 双口径。

## M2 第一批表

```text
teams
team_members
team_tasks
team_runs
team_events
team_audit_events
team_session_links
team_idempotency_keys
team_event_counters
```

### `teams`

```text
id
workspace_id
leader_session_id
name
description
status               created | running | paused | canceling | stopped | completed | failed | archived
version
max_cost
max_tokens
cost_so_far_micros
created_at
updated_at
archived_at
```

### `team_members`

```text
id
team_id
session_id
name
role
agent_profile
model_provider
model_name
status               created | starting | idle | queued | running | waiting_permission | blocked | stopped | failed
current_task_id
current_run_id
current_tool_name
last_event_seq
max_cost
max_tokens
cost_so_far_micros
version
created_at
updated_at
stopped_at
```

### `team_tasks`

```text
id
team_id
title
description
status               queued | assigned | in_progress | blocked | completed | failed | canceled
assignee_member_id
created_by_member_id
priority
version
result_summary
created_at
updated_at
completed_at
```

所有 task update 必须带 `expected_version`。冲突返回稳定结构：

```json
{
  "error": "version_conflict",
  "task_id": "task_01",
  "expected_version": 3,
  "current_version": 4
}
```

### `team_runs`

```text
id
team_id
member_id
task_id
session_id
status               queued | running | waiting_permission | completed | failed | canceled | interrupted
attempt
heartbeat_at
started_at
finished_at
prompt_tokens
completion_tokens
cost_micros
usage_status         final | partial | unknown
error
```

### `team_session_links`

`team_session_links` 是 session 可见性、leader/member 关系和恢复的事实源。它不替代
`session.parent_id`，而是补上 team 语义。

```text
id
workspace_id
team_id
session_id
member_id             nullable; leader session 为空
link_type             leader | member | delegate
visibility            normal | hidden_from_session_list
created_at
ended_at
```

规则：

- leader session 使用 `link_type=leader`，`visibility=normal`。
- long-lived member session 使用 `link_type=member`，默认 `hidden_from_session_list`。
- M1 delegate child session 使用 `link_type=delegate`，是否展示由 M1 UI 决定。
- `(team_id, session_id)` unique。
- active link 的定义是 `ended_at is null`；同一 `session_id` 同时只能有一个 active link。

### `team_idempotency_keys`

```text
id
workspace_id
team_id
actor_member_id       nullable for leader/session-only calls
operation             create_team | spawn_member | create_task | append_mailbox | append_artifact
idempotency_key
request_hash
response_ref_type
response_ref_id
created_at
expires_at
```

`(workspace_id, operation, actor_member_id, idempotency_key)` unique。相同 key、相同 request
hash 返回原结果；相同 key、不同 request hash 返回 `idempotency_conflict`。

### `team_event_counters`

```text
team_id
next_seq
updated_at
```

SQLite 下 per-team event seq 使用 counter row 实现：

1. 在同一 transaction 内 `update team_event_counters set next_seq = next_seq + 1 ...`。
2. 读取更新后的 `next_seq - 1` 作为 event seq。
3. 插入 `team_events(team_id, seq)`，依赖 unique 约束兜底。
4. 如果 counter row 不存在，创建 `next_seq=2` 并使用 seq=1。

### `team_events`

第一版只建 `team_events`，按 outbox 语义设计，避免同时维护 `team_events` 和
`team_event_outbox` 两张表。

```text
seq                  monotonically increasing per team
id
workspace_id
team_id
event_type
entity_type
entity_id
actor_member_id
task_id
run_id
message_id
payload_json
published_at
created_at
```

### `team_audit_events`

M2 建表，但 M2 只要求 domain-level audit；M4 起强制覆盖 permission/tool/hook。

```text
id
workspace_id
team_id
member_id
task_id
run_id
session_id
tool_call_id
event_type
action
resource_type
resource_ref
input_hash
summary
decision
scope
created_at
```

默认不记录 raw file content、headers、env、完整 tool input/output。

## M3 第二批表

```text
team_mailbox_messages
team_message_receipts
team_task_dependencies
team_artifacts
```

### `team_mailbox_messages`

```text
id
team_id
from_member_id
from_role
recipient_type       direct | broadcast | role
to_member_id
to_role
kind                 message | task_assignment | task_status | shutdown_request | shutdown_ack | permission_request | permission_response
correlation_id
summary
payload_json
created_at
```

`permission_request` / `permission_response` 在 M3 只作为 schema 预留；M4 才实现
permission wait/approval 状态机和 runner 消费逻辑。

### `team_message_receipts`

```text
message_id
team_id
member_id
delivered_at
read_at
```

使用 receipt 表而不是 message 上单字段，方便 broadcast/role 展开。

### `team_task_dependencies`

```text
id
team_id
task_id
depends_on_task_id
dependency_type       blocks_start | blocks_completion | informational
created_by_member_id
created_at
resolved_at
```

规则：

- `(team_id, task_id, depends_on_task_id)` unique。
- 不允许 task 依赖自己。
- M3 只实现 `blocks_start` 和 `informational`。
- scheduler 不能 claim 仍有 unresolved `blocks_start` dependency 的 task。
- prompt envelope 可以读取 completed dependency 的 `result_summary`。

### `team_artifacts`

```text
id
team_id
member_id
task_id
run_id
kind                 message_attachment | task_result | patch | verification_log | conflict
title
summary
content_ref
content_hash
metadata_json
created_at
```

M3 只启用 `message_attachment` 和 `task_result`。M5 才启用 `patch`、`verification_log`、
`conflict`。

Patch artifact metadata 见 `12-open-architecture-issues.md` 的 M5 冻结决策。DB 只存
`content_ref` / `content_hash` / `metadata_json`，不直接存完整 patch text。

## M5 写作业边界

```text
team_apply_conflicts
```

M5 仍以 patch artifact 为主。允许在 leader review/apply 时做 base/hash 冲突检测，
但不引入 teammate direct write、worktree runtime 或正式 file lease。

`team_apply_conflicts` 用于记录 apply 阶段失败原因：

```text
id
team_id
artifact_id
task_id
member_id
base_hash
current_hash
conflict_summary
created_at
```

M6 才考虑：

```text
team_file_locks
team_file_leases
team_worktrees
```

其中 lock/lease 只在 direct write 或 worktree/process backend 需要时启用。

## M4 权限事实表

Permission 的详细状态机在 `07-safety-permission-audit.md`。数据层在 M4 新增：

```text
team_permission_requests
team_permission_grants
```

它们是 pending permission、scoped grant、late response、orphaned recovery 和
`pending_permissions` snapshot 的事实源。`team_audit_events` 只记录审计，不承担 live
permission state。

## DDL / SQLC implementation contract

M2 migration 要给工程实现足够稳定的表契约，不能只落字段名。

通用约束：

- 所有 `id` 使用 text UUID/ULID，与现有项目 ID 风格保持一致。
- 所有外键列保留 text id；是否启用 SQLite FK enforcement 由现有 DB 层统一决定。
- 所有表必须有 `created_at`，可变实体必须有 `updated_at` 和 `version`。
- 所有 monetary cost 使用 integer micros 或 decimal string，不能使用 float。
- JSON 字段统一命名为 `*_json`，存储 compact JSON，不存 raw prompt/file content。
- 所有可由 tool 重试创建的写操作必须带 `idempotency_key` 或明确不可重试。
- 新 team 表使用 `*_micros` integer 记录 cost；现有 session 层如果仍有 `REAL cost`，
  只在展示或兼容读取时转换，不把 float 写入 team domain。

M2 必须创建的索引：

```text
teams(workspace_id, status)
team_members(team_id, status)
team_members(team_id, session_id)
team_tasks(team_id, status, priority, created_at)
team_tasks(team_id, assignee_member_id, status)
team_runs(team_id, member_id, status)
team_runs(team_id, task_id)
team_events(team_id, seq) unique
team_events(workspace_id, team_id, created_at)
team_audit_events(team_id, created_at)
team_session_links(session_id, team_id)
team_session_links(session_id) where ended_at is null
team_idempotency_keys(workspace_id, operation, actor_member_id, idempotency_key) unique
team_event_counters(team_id) primary key
team_task_dependencies(team_id, task_id)
team_task_dependencies(team_id, depends_on_task_id)
```

Event seq：

- `team_events.seq` 在每个 `team_id` 内单调递增。
- 写 event 必须与 domain row 在同一 transaction 内完成。
- SQLite 使用 `team_event_counters(team_id, next_seq)` 作为最终方案，不使用
  `select max(seq)+1`。
- SSE payload 携带 `seq`，client 发现 gap 时重新拉 snapshot。

Task CAS：

```sql
UPDATE team_tasks
SET status = ?, result_summary = ?, version = version + 1, updated_at = ?
WHERE id = ? AND team_id = ? AND version = ?;
```

更新影响 0 行时返回 `version_conflict`，调用方必须 `team_task_get` 后合并 intent。

Atomic claim：

```text
ClaimNextTask(team_id, member_id)
  -> atomically pick queued task by priority/created_at
  -> set assignee_member_id, status=assigned, version=version+1
  -> append event/audit in same transaction
```

不允许 service 层先 select 再 update。

Idempotency：

```text
CreateTeamRequest.idempotency_key
SpawnMemberRequest.idempotency_key
CreateTaskRequest.idempotency_key
AppendMailboxRequest.idempotency_key
AppendArtifactRequest.idempotency_key
```

同一 actor、同一 key、同一 operation 必须返回同一结果；payload 不一致时返回
`idempotency_conflict`。

## API / event contract

HTTP 和 local workspace 使用同一 proto 结构，server/client mode 不能少功能。
本节 wrapper 只用于 team endpoints，不迁移现有非 team API。旧 `proto.Error{message}` 和
现有 client 行为保持兼容；team client 需要单独实现 structured error decoding。

### Request / response shape

```json
{
  "idempotency_key": "op_...",
  "actor": {
    "kind": "leader",
    "session_id": "ses_..."
  },
  "payload": {}
}
```

响应：

```json
{
  "data": {},
  "event_seq": 42,
  "trace_id": "trace_..."
}
```

错误：

```json
{
  "error": {
    "code": "version_conflict",
    "message": "task version changed",
    "details": {
      "task_id": "task_01",
      "expected_version": 3,
      "current_version": 4
    }
  },
  "trace_id": "trace_..."
}
```

稳定错误码：

```text
not_found
invalid_actor
permission_denied
version_conflict
idempotency_conflict
budget_exceeded
team_archived
member_not_running
runtime_unavailable
```

### TeamEvent payload

```json
{
  "type": "team.event",
  "workspace_id": "ws_...",
  "team_id": "team_...",
  "seq": 42,
  "event_type": "task.updated",
  "entity_type": "task",
  "entity_id": "task_...",
  "actor_member_id": "member_...",
  "task_id": "task_...",
  "run_id": "run_...",
  "message_id": null,
  "payload": {},
  "created_at": 1779850000
}
```

Client 规则：

- unknown `event_type` 不报错，只触发 snapshot reload 或忽略。
- unknown top-level field 忽略。
- missing `seq` 视为不可 replay event，强制 reload snapshot。
- API list 默认分页，`limit` 最大 100，返回 `next_cursor`。

## Service 接口

```go
type Service interface {
    CreateTeam(ctx context.Context, req CreateTeamRequest) (Team, error)
    GetTeam(ctx context.Context, teamID string) (TeamSnapshot, error)
    ListTeams(ctx context.Context, workspaceID string) ([]Team, error)
    ArchiveTeam(ctx context.Context, teamID string, expectedVersion int64) error

    SpawnMember(ctx context.Context, req SpawnMemberRequest) (TeamMember, error)
    UpdateMemberState(ctx context.Context, req UpdateMemberStateRequest) (TeamMember, error)
    StopMember(ctx context.Context, req StopMemberRequest) error
    ListMembers(ctx context.Context, teamID string) ([]TeamMember, error)

    CreateTask(ctx context.Context, req CreateTaskRequest) (TeamTask, error)
    GetTask(ctx context.Context, teamID, taskID string) (TeamTask, error)
    UpdateTask(ctx context.Context, req UpdateTaskRequest) (TeamTask, error)
    ClaimNextTask(ctx context.Context, req ClaimNextTaskRequest) (TeamTask, error)
    ListTasks(ctx context.Context, teamID string, filter TaskFilter) ([]TeamTask, error)
    AddTaskDependency(ctx context.Context, req AddTaskDependencyRequest) (TaskDependency, error)
    ListTaskDependencies(ctx context.Context, teamID, taskID string) ([]TaskDependency, error)

    AppendMailbox(ctx context.Context, req AppendMailboxRequest) (MailboxMessage, error)
    ListUnread(ctx context.Context, teamID, memberID string, limit int) ([]MailboxMessage, error)
    MarkDelivered(ctx context.Context, req MarkReceiptRequest) error
    MarkRead(ctx context.Context, req MarkReceiptRequest) error

    StartRun(ctx context.Context, req StartRunRequest) (TeamRun, error)
    HeartbeatRun(ctx context.Context, runID string) error
    FinishRun(ctx context.Context, req FinishRunRequest) error
    CancelRun(ctx context.Context, runID string) error

    AppendArtifact(ctx context.Context, req AppendArtifactRequest) (TeamArtifact, error)
    AppendAudit(ctx context.Context, req AuditEvent) error
    ListEventsAfter(ctx context.Context, teamID string, afterSeq int64, limit int) ([]TeamEvent, error)
    CreatePermissionRequest(ctx context.Context, req CreateTeamPermissionRequest) (TeamPermissionRequest, error)
    ResolvePermissionRequest(ctx context.Context, req ResolveTeamPermissionRequest) (TeamPermissionRequest, error)
    FindActiveGrant(ctx context.Context, req FindTeamGrantRequest) (TeamPermissionGrant, error)
    DebugSnapshot(ctx context.Context, teamID string) (DebugSnapshot, error)
}
```

## 事务规则

- domain row、audit、event 在同一 DB transaction 写入。
- LLM call 不在 transaction 内。
- permission wait 不持有 transaction。
- `ClaimNextTask` 必须是 atomic SQL，不允许 select 后 update。
- event publish 失败不回滚 DB。
- 每个可重试写操作必须有 idempotency key 或明确不可重试。

## Snapshot 契约

`DebugSnapshot` 是 TUI 和 client reconnect 的修复源，最小字段：

```text
team
members
tasks
runs
mailbox_depth
pending_permissions
recent_events
artifacts
cost
heartbeat_age
queue_depth
last_seq
```

TUI 处理顺序：

```text
GET snapshot -> last_seq
subscribe SSE
GET events since last_seq
if seq gap -> reload snapshot
```

### Event replay API

```text
GET /v1/workspaces/{id}/teams/{team_id}/events?after_seq=42&limit=100
```

响应：

```json
{
  "data": {
    "events": [],
    "last_seq": 58,
    "has_more": false,
    "next_cursor": null
  },
  "trace_id": "trace_..."
}
```

Service method：

```go
ListEventsAfter(ctx context.Context, teamID string, afterSeq int64, limit int) ([]TeamEvent, error)
```

规则：

- `after_seq` 是 exclusive。
- `limit` 默认 100，最大 100。
- 如果 `after_seq` 小于最早保留 seq，返回 `event_history_compacted`，client 必须 reload snapshot。
  第一版可以不做 compaction，但错误码要预留。
