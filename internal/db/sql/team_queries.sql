-- M3-02: 13 team-data-domain queries. Package db (sqlc.yaml package: "db").
-- Runtime-verified against modernc SQLite: UPDATE ... FROM (ClaimNextTask),
-- ON CONFLICT ... DO UPDATE ... RETURNING (NextEventSeq) both execute
-- correctly. If a future sqlc generate on a CLI-equipped machine rejects
-- UPDATE FROM, replace ClaimNextTask's FROM clause with the subquery form:
--   WHERE id = (SELECT id FROM team_tasks WHERE team_id = ? AND status = 'queued'
--               ORDER BY priority DESC, created_at ASC LIMIT 1)
--     AND team_id = ? AND status = 'queued'

-- name: InsertTeam :one
INSERT INTO teams (id, workspace_id, leader_session_id, name, description, status, version, max_cost, max_tokens, cost_so_far_micros, created_at, updated_at)
VALUES (?, ?, ?, ?, ?, 'created', 1, ?, ?, 0, ?, ?)
RETURNING *;

-- name: GetTeam :one
SELECT * FROM teams WHERE id = ? LIMIT 1;

-- name: ListTeams :many
SELECT * FROM teams
WHERE workspace_id = ? AND status != 'archived'
ORDER BY created_at DESC;

-- name: ArchiveTeam :exec
UPDATE teams SET status = 'archived', archived_at = ?, updated_at = ? WHERE id = ?;

-- name: UpdateTaskCAS :one
UPDATE team_tasks
SET status = ?, assignee_member_id = ?, result_summary = ?, version = version + 1, updated_at = ?
WHERE id = ? AND team_id = ? AND version = ?
RETURNING *;

-- name: ClaimNextTask :one
WITH next AS (
    SELECT id FROM team_tasks
    WHERE team_id = ? AND status = 'queued'
    ORDER BY priority DESC, created_at ASC
    LIMIT 1
)
UPDATE team_tasks
SET assignee_member_id = ?, status = 'assigned', version = version + 1, updated_at = ?
FROM next
WHERE team_tasks.id = next.id AND team_tasks.team_id = ? AND team_tasks.status = 'queued'
RETURNING team_tasks.*;

-- name: NextEventSeq :one
INSERT INTO team_event_counters(team_id, next_seq, updated_at)
VALUES (?, 2, ?)
ON CONFLICT(team_id) DO UPDATE
SET next_seq = next_seq + 1, updated_at = ?
RETURNING next_seq;

-- name: InsertEvent :exec
INSERT INTO team_events (seq, id, workspace_id, team_id, event_type, entity_type, entity_id, actor_member_id, task_id, run_id, message_id, payload_json, published_at, created_at)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?);

-- name: ListEventsAfter :many
SELECT * FROM team_events
WHERE team_id = ? AND seq > ?
ORDER BY seq ASC
LIMIT ?;

-- name: UpdateRunHeartbeat :exec
UPDATE team_runs SET heartbeat_at = ? WHERE id = ?;

-- name: FinishRun :exec
UPDATE team_runs
SET status = 'completed', finished_at = ?, prompt_tokens = ?, completion_tokens = ?, cost_micros = ?, usage_status = 'final'
WHERE id = ? AND team_id = ? AND status = 'running';

-- name: MarkRunTerminal :exec
UPDATE team_runs
SET status = ?, finished_at = ?, error = ?, usage_status = ?
WHERE id = ? AND team_id = ? AND status = ?;

-- name: FindStaleRuns :many
SELECT * FROM team_runs
WHERE status IN ('running', 'waiting_permission', 'queued')
  AND heartbeat_at < ?;

-- name: InsertMember :one
INSERT INTO team_members (id, team_id, session_id, name, role, agent_profile, model_provider, model_name, status, current_task_id, current_run_id, current_tool_name, last_event_seq, max_cost, max_tokens, cost_so_far_micros, version, created_at, updated_at)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, 'created', ?, ?, ?, 0, ?, ?, 0, 1, ?, ?)
RETURNING *;

-- name: GetMember :one
SELECT * FROM team_members WHERE id = ? LIMIT 1;

-- name: ListMembers :many
SELECT * FROM team_members WHERE team_id = ? ORDER BY created_at ASC;

-- name: UpdateMemberCAS :one
UPDATE team_members
SET status = ?, current_task_id = ?, current_run_id = ?, current_tool_name = ?, last_event_seq = ?, version = version + 1, updated_at = ?
WHERE id = ? AND team_id = ? AND version = ?
RETURNING *;

-- name: InsertTask :one
INSERT INTO team_tasks (id, team_id, title, description, status, assignee_member_id, created_by_member_id, priority, version, created_at, updated_at)
VALUES (?, ?, ?, ?, 'queued', ?, ?, ?, 1, ?, ?)
RETURNING *;

-- name: GetTask :one
SELECT * FROM team_tasks WHERE id = ? AND team_id = ? LIMIT 1;

-- name: ListTasks :many
SELECT * FROM team_tasks WHERE team_id = ? ORDER BY priority DESC, created_at ASC;

-- name: InsertRun :one
INSERT INTO team_runs (id, team_id, member_id, task_id, session_id, status, attempt, heartbeat_at, started_at)
VALUES (?, ?, ?, ?, ?, 'queued', 1, ?, ?)
RETURNING *;

-- name: GetRun :one
SELECT * FROM team_runs WHERE id = ? AND team_id = ? LIMIT 1;

-- name: InsertAudit :exec
INSERT INTO team_audit_events (id, workspace_id, team_id, member_id, task_id, run_id, session_id, tool_call_id, event_type, action, resource_type, resource_ref, input_hash, summary, decision, scope, created_at)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?);

-- name: ListAudit :many
SELECT * FROM team_audit_events WHERE team_id = ? ORDER BY created_at DESC LIMIT ?;

-- M4-04: Mailbox queries

-- name: InsertMessage :one
INSERT INTO team_mailbox_messages (id, team_id, from_member_id, kind, summary, payload, created_at)
VALUES (?, ?, ?, ?, ?, ?, ?)
RETURNING *;

-- name: GetUnreadMessages :many
SELECT m.* FROM team_mailbox_messages m
JOIN team_message_receipts r ON r.message_id = m.id
WHERE r.to_member_id = ? AND r.read_at IS NULL
ORDER BY m.created_at ASC
LIMIT ?;

-- name: InsertReceipt :exec
INSERT INTO team_message_receipts (id, message_id, to_member_id, delivered_at, read_at)
VALUES (?, ?, ?, ?, ?);

-- name: MarkDelivered :exec
UPDATE team_message_receipts
SET delivered_at = ?
WHERE message_id = ? AND to_member_id = ?;

-- name: MarkRead :exec
UPDATE team_message_receipts
SET read_at = ?
WHERE message_id = ? AND to_member_id = ?;

-- M4-10: Session link queries

-- name: InsertSessionLink :one
INSERT INTO team_session_links (id, team_id, member_id, session_id, link_type, linked_at)
VALUES (?, ?, ?, ?, ?, ?)
RETURNING *;

-- name: GetSessionLinkByMember :one
SELECT * FROM team_session_links
WHERE team_id = ? AND member_id = ? AND link_type = 'member'
ORDER BY linked_at DESC
LIMIT 1;

-- name: GetSessionLinksByTeam :many
SELECT * FROM team_session_links
WHERE team_id = ?
ORDER BY linked_at DESC;
