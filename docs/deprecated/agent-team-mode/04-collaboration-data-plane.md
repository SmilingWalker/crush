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

### 2.1 成本预算字段

`team_members` 增加预算控制：

```sql
ALTER TABLE team_members ADD COLUMN max_cost REAL;
ALTER TABLE team_members ADD COLUMN max_tokens INTEGER;
ALTER TABLE team_members ADD COLUMN tokens_used INTEGER NOT NULL DEFAULT 0;
ALTER TABLE team_members ADD COLUMN cost_used REAL NOT NULL DEFAULT 0;
```

`team_runs` 增加预算上限：

```sql
ALTER TABLE team_runs ADD COLUMN max_cost REAL;
ALTER TABLE team_runs ADD COLUMN max_tokens INTEGER;
```

`teams` 增加团队级预算：

```sql
ALTER TABLE teams ADD COLUMN max_cost REAL;
ALTER TABLE teams ADD COLUMN max_tokens INTEGER;
ALTER TABLE teams ADD COLUMN tokens_used INTEGER NOT NULL DEFAULT 0;
ALTER TABLE teams ADD COLUMN cost_used REAL NOT NULL DEFAULT 0;
```

强制执行：

- `FinishRunTx` 更新 member/team 级 `tokens_used`/`cost_used`。
- scheduler claim task 前检查 member 和 team 的剩余预算。
- 超预算时 task 标记 `failed`，写入 system message 通知 leader。
- `max_cost`/`max_tokens` 为 NULL 时表示不限制。

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

## 8. 消息消费与 prompt 构建算法

M3b 引入 mailbox 后，teammate 每次 run 的 prompt 需要从多个来源组装。以下是 prompt 构建的确定算法。

### 8.1 消息来源与优先级

按以下优先级从高到低注入 teammate prompt：

1. **Task description**（最高优先级，不可截断）
2. **未读 direct messages**（按 timestamp 升序排列）
3. **当前 task 依赖的 completed task 的 result summary**（如有）
4. **与当前 task 相关的 broadcast messages**（按 timestamp 升序）
5. **Leader 最新指令**（最新一条 direct message from leader）
6. **自己的 root session 摘要**（如 context window 允许）

显式不包含：
- leader 完整 session。
- 其他 teammate 完整 transcript。
- 所有 workspace history。

### 8.2 Context window 溢出处理

当以上内容总 token 超过 teammate model 的可用 context window 时：

1. 保留 task description（不可截断）。
2. 保留所有 direct messages（不可截断，如果 direct messages 本身超限则只保留最新 N 条）。
3. 截断 task result summary（保留摘要，截断详细内容）。
4. 截断 broadcast messages（只保留最新一条的摘要）。
5. 移除 root session 摘要。

截断后的内容标记 `[truncated, N messages omitted]`。

### 8.3 大消息处理

消息 body 可能包含大段代码或 diff。处理策略：

- `team_messages.body` 限制最大 64KB。
- 超过 64KB 的内容必须通过 `team_artifacts` 表存储。
- `team_messages` 只存 summary（≤ 500 字）+ `artifact_id` 引用。
- 消费时 teammate prompt 只注入 summary，需要完整内容时通过 artifact 读取。

### 8.4 Broadcast 消费

- broadcast 消息为每个 member 生成独立 receipt。
- teammate 只消费与自己当前 task 相关的 broadcast（通过 `task_id` 匹配）。
- 无 task 关联的 broadcast（如 leader 的全局指令）总是消费。
- 已 ack 的 broadcast 不重复注入。

## 9. `Workspace` 接入

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

