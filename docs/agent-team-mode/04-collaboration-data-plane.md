# 04 B 线：Collaboration Data Plane

B 线负责 team 的 durable data plane：DB schema、sqlc、事务、API、event outbox 和 SSE replay。

## 1. 原则

- SQLite 是事实源。
- SSE/pubsub 只是通知。
- 不持久化 runtime workspace id。
- 先用 link table，不急着改 `sessions/messages`。
- 所有关键写操作带 idempotency key。

## 2. M2 第一批表

M2 只做 durable skeleton：

```text
teams
team_members
team_tasks
team_runs
team_events
team_session_links
```

用途：

- 创建 team。
- 创建 leader/member 映射。
- 创建 teammate runtime root session。
- 分配 task。
- 记录 run attempt。
- replay team event。

## 3. M3 第二批表

M3 加协作循环：

```text
team_messages
team_message_receipts
team_artifacts
team_task_dependencies
```

用途：

- direct/broadcast message。
- ask leader。
- peer review。
- task dependency。
- result/report artifact。

## 4. M4/M5 第三批表

```text
team_file_leases
team_worktrees
team_external_agents
```

用途：

- direct write file lease。
- worktree runner。
- A2A gateway mapping。

## 5. 事务边界

必须提供服务层事务：

```go
CreateTeamTx
SpawnMemberTx
EnqueueTaskTx
ClaimTaskTx
StartRunTx
FinishRunTx
CancelRunTx
AppendMessageTx
AppendArtifactTx
RecoverRunsTx
```

规则：

- 状态表和 `team_events` 同事务写。
- commit 后 publish SSE。
- LLM call 不在事务内。
- 等 permission 不持有事务。
- `ClaimTaskTx` 必须 atomic。

## 6. Event outbox

`team_events`：

```sql
seq INTEGER PRIMARY KEY AUTOINCREMENT
id TEXT UNIQUE
team_id TEXT NOT NULL
type TEXT NOT NULL
entity_type TEXT NOT NULL
entity_id TEXT NOT NULL
actor_member_id TEXT
task_id TEXT
run_id TEXT
message_id TEXT
payload_json TEXT NOT NULL DEFAULT '{}'
created_at INTEGER NOT NULL
```

client 恢复：

```text
GET /snapshot -> snapshot_seq
GET /events?since_seq=snapshot_seq
subscribe SSE
if seq gap -> replay
```

## 7. API 路径

M2：

```text
POST   /v1/workspaces/{id}/teams
GET    /v1/workspaces/{id}/teams
GET    /v1/workspaces/{id}/teams/{team_id}
POST   /v1/workspaces/{id}/teams/{team_id}/members
GET    /v1/workspaces/{id}/teams/{team_id}/members
POST   /v1/workspaces/{id}/teams/{team_id}/tasks
GET    /v1/workspaces/{id}/teams/{team_id}/tasks
GET    /v1/workspaces/{id}/teams/{team_id}/runs
POST   /v1/workspaces/{id}/teams/{team_id}/runs/{run_id}/cancel
GET    /v1/workspaces/{id}/teams/{team_id}/snapshot
GET    /v1/workspaces/{id}/teams/{team_id}/events?since_seq=&limit=
```

M3：

```text
POST   /messages
GET    /messages
POST   /messages/{message_id}/read
POST   /messages/{message_id}/ack
POST   /tasks/{task_id}/pause
POST   /tasks/{task_id}/resume
POST   /tasks/{task_id}/retry
GET    /artifacts
```

M4：

```text
POST /artifacts/{artifact_id}/apply
POST /artifacts/{artifact_id}/reject
GET  /file-leases
```

## 8. `Workspace` 接入

不要把所有 team 方法直接塞入现有 `Workspace` 第一版。

建议：

```go
type TeamWorkspace interface {
    Workspace
    CreateTeam(...)
    GetTeamSnapshot(...)
    SpawnTeamMember(...)
    AssignTeamTask(...)
    CancelTeamRun(...)
}
```

TUI 使用 type assertion：

```go
tw, ok := workspace.(TeamWorkspace)
```

## 9. 测试

DB：

- migrations up/down。
- `CreateTeamTx` 写 team/member/link/event。
- `ClaimTaskTx` 并发只成功一次。
- `FinishRunTx` 幂等。

API：

- snapshot 包含 seq。
- events replay 顺序正确。
- old `/agent` 不变。
- unknown payload 不影响旧 client。

