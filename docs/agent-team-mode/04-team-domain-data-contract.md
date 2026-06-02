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
team_event_counters
```

M2 core 的目标是验证 durable snapshot/event/API 事实源，不提前创建 M3 才消费的 mailbox、
artifact、session visibility 和 idempotency hardening 表。

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
status               created | starting | idle | queued | running | waiting_permission | blocked | canceling_turn | shutting_down | stopped | failed
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
team_session_links
team_mailbox_messages
team_message_receipts
team_task_dependencies
team_artifacts
```

### `team_session_links`

`team_session_links` 是 session 可见性、leader/member 关系和恢复的事实源。它不替代
`session.parent_id`，而是补上 team 语义。M2 可以先通过 `teams.leader_session_id` 和
`team_members.session_id` 表达基础关系；M3 在真正启动长期 teammate、处理 session visibility
和 recovery 时再创建本表。

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
- M1 delegate child session 使用内存 trace，不写本表；M3 如需展示历史 delegate link 再 backfill。
- `(team_id, session_id)` unique。
- active link 的定义是 `ended_at is null`；同一 `session_id` 同时只能有一个 active link。

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
kind                 message_attachment | task_result | change_proposal | patch | verification_log | conflict
title
summary
content_ref
content_hash
metadata_json
created_at
```

M3 只启用 `message_attachment` 和 `task_result`。M3.5 启用 `change_proposal`。M5 才启用
`patch`、`verification_log`、`conflict`。

`change_proposal` 是只读 teammate 的实现提案，不是可 apply patch。metadata 必须包含：

```text
target_files          path, reason, risk
proposed_hunks        file, intent, anchor, line_hint, before_summary, after_summary, pseudo_diff
verification_suggestions
proposal_status       pending_review | revise_requested | discarded | accepted_for_patch
generated_by_run_id
```

`proposed_hunks` 只做轻量结构化，目的是支撑 review、request revision 和 M5 重新生成 patch 的输入，
不是为了在 M3.5 解析或应用 diff。建议字段：

```text
file                  workspace-relative path
intent                add | modify | delete | rename | unknown
anchor                optional symbol/function/heading/search hint
line_hint             optional approximate line or range, non-authoritative
before_summary        optional current behavior/content summary
after_summary         proposed behavior/content summary
pseudo_diff           optional human-readable pseudo diff, not unified diff contract
confidence            optional low | medium | high
```

规则：

- `change_proposal` 不允许写 workspace，不允许 apply。
- `pseudo_diff` 只作为 review 内容，不能进入 patch apply service。
- `accepted_for_patch` 只表示 leader 认为提案值得继续；真正 patch 仍要等 M5 patch artifact。
- 硬校验只覆盖 JSON shape、artifact size、`proposal_status` enum、`intent` enum、workspace-relative
  path 和 path traversal；不校验 line 是否存在、anchor 是否命中或 `pseudo_diff` 是否符合 unified diff。
- `line_hint`、`anchor`、`pseudo_diff` 都是 hint；UI 可以展示 stale/missing context，但不能因此把
  proposal 升级成可 apply patch。

Patch artifact metadata 见 `12-open-architecture-issues.md` 的 M5 冻结决策。DB 只存
`content_ref` / `content_hash` / `metadata_json`，不直接存完整 patch text。

### Content store 契约

`content_ref` 不是文件路径，也不是 member 可构造的任意 URI。它必须由 workspace-scoped
content store 生成并校验：

```go
type ContentRef struct {
    WorkspaceID string
    ArtifactID  string
    Ref         string // opaque, store-generated
    Hash        string // sha256:<hex>
}

type ContentStore interface {
    Put(ctx context.Context, workspaceID, artifactID, kind string, data []byte) (ContentRef, error)
    Get(ctx context.Context, workspaceID string, ref ContentRef) ([]byte, error)
    Verify(ctx context.Context, workspaceID string, ref ContentRef) error
    Delete(ctx context.Context, workspaceID string, ref ContentRef) error
}
```

规则：

- `Ref` 必须是不透明 ID；member/tool input 不能传入绝对路径、相对路径或 `file://`。
- `Get` 和 apply 前必须重新计算 hash 并匹配 `content_hash`，不匹配返回 `artifact_integrity_failed`。
- store 读写必须校验 `workspace_id` 与 artifact row 一致，不能跨 workspace/team 读取。
- M5 第一版使用 app data 下的 filesystem blob store；store root 放在受控 app data 目录，
  不能落在普通 editable workspace path 里。
- filesystem path 是 ContentStore implementation detail，不能出现在 API、DB `content_ref`、UI 或
  member-visible payload 中。
- archive/delete team 时执行 retention cleanup；清理失败写 audit/debug，不静默忽略。

演进路线：

- M5.1 增加 orphan blob sweep：扫描 content store 与 `team_artifacts` 引用差异，清理无主 blob。
- M5.2 增加 quota/retention policy：按 workspace/team 限制 blob 总量、单 artifact size 和保留周期。
- M6+ 可替换为 DB blob、CAS/object store 或 remote backend；`ContentStore` API 和 opaque ref/hash
  contract 不变。
- 后续如引入加密，放在 ContentStore implementation 内；上层仍只看 opaque ref 和 hash。

## M4 权限事实表

Permission 的详细状态机在 `07-safety-permission-audit.md`。数据层在 M4 新增：

```text
team_permission_requests
team_permission_grants
```

它们是 pending permission、scoped grant、late response、orphaned recovery 和
`pending_permissions` snapshot 的事实源。`team_audit_events` 只记录审计，不承担 live
permission state。

## M5 写作业边界

```text
team_apply_conflicts
```

M5 仍以 patch artifact 为主。允许在 leader review/apply 时做 base/hash 冲突检测、写入前
recheck 和进程内 per-path serialize guard，但不引入 teammate direct write、worktree runtime
或跨进程正式 file lease。

`team_apply_conflicts` 用于记录 apply 阶段失败原因：

```text
id
team_id
artifact_id
task_id
member_id
base_hash
current_hash
conflict_type          base_mismatch | pre_write_mismatch | apply_failed | rollback_failed | partial_apply
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

M5 apply safety 第一版要求：

- apply 前验证所有 touched files 的 base hash；任一 mismatch 整体 no-write。
- 写入前对每个 touched file 做 pre-write recheck；recheck mismatch 生成 conflict，整体 no-write。
- 同一进程内同一路径 apply 使用 per-path mutex 或等价 serialize guard。
- 单文件写入使用 temp file + fsync/close + atomic rename。
- 多文件 apply 不宣称 filesystem-level atomic；必须保留 before-image backup，后续文件失败时尝试
  rollback。rollback 失败写 `partial_apply` / `rollback_failed` conflict，并让 UI 显示人工处理。

## DDL / SQLC implementation contract

M2 migration 要给工程实现足够稳定的表契约，不能只落字段名。

通用约束：

- 所有 `id` 使用 text UUID/ULID，与现有项目 ID 风格保持一致。
- 所有外键列保留 text id；是否启用 SQLite FK enforcement 由现有 DB 层统一决定。
- 所有表必须有 `created_at`，可变实体必须有 `updated_at` 和 `version`。
- 所有 monetary cost 使用 integer micros 或 decimal string，不能使用 float。
- JSON 字段统一命名为 `*_json`，存储 compact JSON，不存 raw prompt/file content。
- M2.5/PR-6 起，所有通过 server/client API 可重试创建的写操作必须带 `idempotency_key`
  或明确不可重试；M2 local/debug service 可先不建 idempotency 表。
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
team_event_counters(team_id) primary key
```

M3/M2.5 后续索引：

```text
team_session_links(session_id, team_id)
team_session_links(session_id) where ended_at is null
team_task_dependencies(team_id, task_id)
team_task_dependencies(team_id, depends_on_task_id)
team_idempotency_keys(workspace_id, operation, actor_member_id, idempotency_key) unique
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

## M2.5 / PR-6 API hardening table

`team_idempotency_keys` 在 server/client API 开始承诺可重试写操作时创建，不属于 M2 core migration。

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

Idempotency request fields：

```text
CreateTeamRequest.idempotency_key
SpawnMemberRequest.idempotency_key
CreateTaskRequest.idempotency_key
AppendMailboxRequest.idempotency_key
AppendArtifactRequest.idempotency_key
```

M2 local/debug path 可以把这些字段标记为 optional/no-op；PR-6 server/client write API
hardening 后必须执行 idempotency contract。

## API / event contract

HTTP 和 local workspace 使用同一 proto 结构，server/client mode 不能少功能。
本节 wrapper 只用于 team endpoints，不迁移现有非 team API。旧 `proto.Error{message}` 和
现有 client 行为保持兼容；team client 需要单独实现 structured error decoding。

### Team endpoint auth / authorization

Team endpoints 继承现有 server/client workspace 访问模型，但必须在 team route 层再做
workspace 与 actor 校验。没有 workspace 访问权的 client 不能读取或修改任何 team state。

规则：

- 所有 `/v1/workspaces/{id}/teams/*` endpoint 必须验证 caller 对 `{id}` workspace 有访问权。
- `team_id` 必须属于 path 中的 `workspace_id`；不匹配返回 `not_found`，不泄露真实 team。
- leader-only endpoint：create team、spawn/stop member、archive team、apply/reject patch、
  resolve permission。
- member-capable endpoint：list/get task、claim/update own task、send message、report status。
- server API 不信任 request body 中的 actor；actor 必须由 authenticated session/client context
  与 team membership 派生。
- local mode 也使用同一 actor validation path，避免 local/server 行为分叉。
- SSE `PayloadTypeTeamEvent` 只向有 workspace 访问权的 client 广播；payload 内仍不包含 raw
  secret、raw patch content 或完整 tool output。

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
feature_disabled
not_implemented
artifact_integrity_failed
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

### SSE / proto 类型

server/client mode 必须沿用现有 `internal/server/events.go` 的 envelope 模式，而不是发裸 JSON。
M2 新增以下最小类型：

```go
// internal/pubsub/events.go
const PayloadTypeTeamEvent PayloadType = "team_event"

// internal/proto/team.go
type TeamEvent struct {
    WorkspaceID   string         `json:"workspace_id"`
    TeamID        string         `json:"team_id"`
    Seq           int64          `json:"seq"`
    EventType     string         `json:"event_type"`
    EntityType    string         `json:"entity_type"`
    EntityID      string         `json:"entity_id"`
    ActorMemberID string         `json:"actor_member_id,omitempty"`
    TaskID        string         `json:"task_id,omitempty"`
    RunID         string         `json:"run_id,omitempty"`
    MessageID     string         `json:"message_id,omitempty"`
    Payload       map[string]any `json:"payload,omitempty"`
    CreatedAt     int64          `json:"created_at"`
}

// internal/server/events.go
func teamEventToProto(e team.Event) proto.TeamEvent
```

`wrapEvent` 必须新增 `pubsub.Event[team.Event] -> PayloadTypeTeamEvent` 分支；`internal/client/proto.go`
必须新增 `PayloadTypeTeamEvent` case。旧 client 看到 unknown payload type 时继续忽略并记录 warning；
新 client 对 unknown `event_type` 只 reload snapshot，不崩溃。

## Service / Store 接口

`team.Service` 是 use-case facade，不是 database dump。server/client/workspace/UI 只依赖
facade；SQL/query 边界放在 milestone-scoped store 里。跨表事务只能由 Service 编排，store
不向外部层暴露。

M2 facade 只包含 core durable domain 能力：

```go
type Service interface {
    CreateTeam(ctx context.Context, req CreateTeamRequest) (TeamSnapshot, error)
    GetTeamSnapshot(ctx context.Context, workspaceID, teamID string) (TeamSnapshot, error)
    ListTeams(ctx context.Context, workspaceID string, filter TeamFilter) ([]Team, error)
    ArchiveTeam(ctx context.Context, req ArchiveTeamRequest) error

    SpawnMember(ctx context.Context, req SpawnMemberRequest) (TeamMember, error)
    UpdateMemberState(ctx context.Context, req UpdateMemberStateRequest) (TeamMember, error)
    ListMembers(ctx context.Context, teamID string) ([]TeamMember, error)

    CreateTask(ctx context.Context, req CreateTaskRequest) (TeamTask, error)
    GetTask(ctx context.Context, teamID, taskID string) (TeamTask, error)
    UpdateTask(ctx context.Context, req UpdateTaskRequest) (TeamTask, error)
    ClaimNextTask(ctx context.Context, req ClaimNextTaskRequest) (TeamTask, error)
    ListTasks(ctx context.Context, teamID string, filter TaskFilter) ([]TeamTask, error)

    StartRun(ctx context.Context, req StartRunRequest) (TeamRun, error)
    HeartbeatRun(ctx context.Context, runID string) error
    FinishRun(ctx context.Context, req FinishRunRequest) error
    MarkRunTerminal(ctx context.Context, req MarkRunTerminalRequest) error

    ListEventsAfter(ctx context.Context, teamID string, afterSeq int64, limit int) ([]TeamEvent, error)
    DebugSnapshot(ctx context.Context, teamID string) (DebugSnapshot, error)
}
```

M2 internal stores：

```go
type TeamStore interface {
    CreateTeam(ctx context.Context, tx Tx, req CreateTeamRequest) (Team, error)
    GetTeam(ctx context.Context, tx Tx, teamID string) (Team, error)
    ListTeams(ctx context.Context, tx Tx, workspaceID string, filter TeamFilter) ([]Team, error)
    ArchiveTeam(ctx context.Context, tx Tx, req ArchiveTeamRequest) error
}

type MemberStore interface {
    CreateMember(ctx context.Context, tx Tx, req SpawnMemberRequest) (TeamMember, error)
    UpdateMemberState(ctx context.Context, tx Tx, req UpdateMemberStateRequest) (TeamMember, error)
    ListMembers(ctx context.Context, tx Tx, teamID string) ([]TeamMember, error)
}

type TaskStore interface {
    CreateTask(ctx context.Context, tx Tx, req CreateTaskRequest) (TeamTask, error)
    GetTask(ctx context.Context, tx Tx, teamID, taskID string) (TeamTask, error)
    UpdateTaskCAS(ctx context.Context, tx Tx, req UpdateTaskRequest) (TeamTask, error)
    ClaimNextTask(ctx context.Context, tx Tx, req ClaimNextTaskRequest) (TeamTask, error)
    ListTasks(ctx context.Context, tx Tx, teamID string, filter TaskFilter) ([]TeamTask, error)
}

type RunStore interface {
    StartRun(ctx context.Context, tx Tx, req StartRunRequest) (TeamRun, error)
    HeartbeatRun(ctx context.Context, tx Tx, runID string) error
    FinishRun(ctx context.Context, tx Tx, req FinishRunRequest) error
    MarkRunTerminal(ctx context.Context, tx Tx, req MarkRunTerminalRequest) error
}

type EventStore interface {
    AppendEvent(ctx context.Context, tx Tx, req AppendEventRequest) (TeamEvent, error)
    ListEventsAfter(ctx context.Context, tx Tx, teamID string, afterSeq int64, limit int) ([]TeamEvent, error)
}

type AuditStore interface {
    AppendAudit(ctx context.Context, tx Tx, req AuditEvent) error
    ListAudit(ctx context.Context, tx Tx, filter AuditFilter) ([]AuditEvent, error)
}
```

`MarkRunTerminalRequest` 用于 `canceled` / `interrupted` / `failed` 等非正常完成路径，不能只传
`run_id`。最小字段：

```text
run_id
team_id
member_id
expected_status       running | waiting_permission | queued
terminal_status       canceled | interrupted | failed
reason                user_requested | leader_requested | shutdown | timeout | runtime_lost | system
actor
usage_status          partial | unknown
error
finished_at
```

规则：

- terminal update 必须 CAS 当前 run 状态；已经 `completed/canceled/interrupted/failed` 的 run
  不能被 late result 覆盖。
- `CancelMemberTurn`、shutdown、startup recovery 和 runtime panic recovery 都使用该 request。
- audit/event 仍由 `team.Service` 在同一 transaction 编排，store 不直接 publish。

后续阶段按消费时机追加 store：

| 阶段 | 追加 store | facade 扩展 |
| --- | --- | --- |
| M3 | `MailboxStore`, `ReceiptStore`, `DependencyStore`, `ArtifactStore`, `SessionLinkStore` | mailbox、receipt、dependency、artifact、session visibility use cases |
| M3.5 | `ProposalStore` 或复用 `ArtifactStore` 的 `change_proposal` subset | proposal review/revise/discard/accept-for-patch |
| M4 | `PermissionStore`, `GrantStore` | team permission request、resolve、grant lookup |
| M5 | `PatchStore`, `ContentStore`, `ApplyConflictStore` | patch artifact、content verification、apply/reject/conflict |

规则：

- 每个里程碑只要求实现当阶段 store；未到阶段的 use case 不允许 stub 成“看起来可用”。
- flag off 或未到阶段时返回 `feature_disabled` / `not_implemented`，不能创建部分状态。
- store 只接受 `tx` 或 query executor，不自己开启跨表 transaction。
- Service method 必须负责 `domain row + audit + event` 的事务编排。
- server/client/UI 不 import store interface；只通过 `team.Service` / `TeamWorkspace` 访问。

## 事务规则

- domain row、audit、event 在同一 DB transaction 写入。
- LLM call 不在 transaction 内。
- permission wait 不持有 transaction。
- `ClaimNextTask` 必须是 atomic SQL，不允许 select 后 update。
- SSE/pubsub 通知失败不回滚 DB 事务。`team_events` row 必须与 domain row 原子写入。
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
