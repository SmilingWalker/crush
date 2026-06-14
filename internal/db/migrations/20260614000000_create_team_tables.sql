-- +goose Up
-- +goose StatementBegin

-- M3: 7 core team-data-domain tables.
-- Second-batch tables (team_session_links, team_mailbox_messages,
-- team_message_receipts, team_task_dependencies, team_artifacts,
-- team_idempotency_keys) are deferred to M3b/M2.5/M4 and are NOT
-- created here. Columns that forward-reference those tables
-- (team_events.message_id, team_audit_events.tool_call_id /
-- session_id) are kept as bare TEXT with no FK.

CREATE TABLE IF NOT EXISTS teams (
    id                 TEXT PRIMARY KEY,
    workspace_id       TEXT NOT NULL,
    leader_session_id  TEXT NOT NULL,
    name               TEXT NOT NULL,
    description        TEXT,
    status             TEXT NOT NULL DEFAULT 'created',
    version            INTEGER NOT NULL DEFAULT 1,
    max_cost           INTEGER,                  -- micros
    max_tokens         INTEGER,
    cost_so_far_micros INTEGER NOT NULL DEFAULT 0,
    created_at         INTEGER NOT NULL,
    updated_at         INTEGER NOT NULL,
    archived_at        INTEGER
);

CREATE TABLE IF NOT EXISTS team_members (
    id                 TEXT PRIMARY KEY,
    team_id            TEXT NOT NULL REFERENCES teams(id) ON DELETE CASCADE,
    session_id         TEXT,
    name               TEXT NOT NULL,
    role               TEXT NOT NULL,
    agent_profile      TEXT NOT NULL,            -- JSON: agent spec reference
    model_provider     TEXT,
    model_name         TEXT,
    status             TEXT NOT NULL DEFAULT 'created',
    current_task_id    TEXT,
    current_run_id     TEXT,
    current_tool_name  TEXT,
    last_event_seq     INTEGER NOT NULL DEFAULT 0,
    max_cost           INTEGER,
    max_tokens         INTEGER,
    cost_so_far_micros INTEGER NOT NULL DEFAULT 0,
    version            INTEGER NOT NULL DEFAULT 1,
    created_at         INTEGER NOT NULL,
    updated_at         INTEGER NOT NULL,
    stopped_at         INTEGER
);

CREATE TABLE IF NOT EXISTS team_tasks (
    id                   TEXT PRIMARY KEY,
    team_id              TEXT NOT NULL REFERENCES teams(id) ON DELETE CASCADE,
    title                TEXT NOT NULL,
    description          TEXT,
    status               TEXT NOT NULL DEFAULT 'queued',
    assignee_member_id   TEXT,                   -- bare TEXT: no FK (cycle)
    created_by_member_id TEXT NOT NULL,          -- bare TEXT: no FK (cycle)
    priority             INTEGER NOT NULL DEFAULT 0,
    version              INTEGER NOT NULL DEFAULT 1,
    result_summary       TEXT,
    created_at           INTEGER NOT NULL,
    updated_at           INTEGER NOT NULL,
    completed_at         INTEGER
);

CREATE TABLE IF NOT EXISTS team_runs (
    id                TEXT PRIMARY KEY,
    team_id           TEXT NOT NULL REFERENCES teams(id) ON DELETE CASCADE,
    member_id         TEXT NOT NULL REFERENCES team_members(id) ON DELETE CASCADE,
    task_id           TEXT REFERENCES team_tasks(id) ON DELETE CASCADE,
    session_id        TEXT NOT NULL,
    status            TEXT NOT NULL DEFAULT 'queued',
    attempt           INTEGER NOT NULL DEFAULT 1,
    heartbeat_at      INTEGER,
    started_at        INTEGER,
    finished_at       INTEGER,
    prompt_tokens     INTEGER,
    completion_tokens INTEGER,
    cost_micros       INTEGER,
    usage_status      TEXT,                      -- final | partial | unknown
    error             TEXT
);

CREATE TABLE IF NOT EXISTS team_events (
    seq             INTEGER NOT NULL,
    id              TEXT PRIMARY KEY,
    workspace_id    TEXT NOT NULL,
    team_id         TEXT NOT NULL REFERENCES teams(id) ON DELETE CASCADE,
    event_type      TEXT NOT NULL,
    entity_type     TEXT NOT NULL,
    entity_id       TEXT NOT NULL,
    actor_member_id TEXT,
    task_id         TEXT,
    run_id          TEXT,
    message_id      TEXT,                        -- forward-ref to team_mailbox_messages (M3b); bare TEXT
    payload_json    TEXT,
    published_at    INTEGER,
    created_at      INTEGER NOT NULL
);

CREATE TABLE IF NOT EXISTS team_event_counters (
    team_id    TEXT PRIMARY KEY REFERENCES teams(id) ON DELETE CASCADE,
    next_seq   INTEGER NOT NULL DEFAULT 1,
    updated_at INTEGER NOT NULL
);

CREATE TABLE IF NOT EXISTS team_audit_events (
    id            TEXT PRIMARY KEY,
    workspace_id  TEXT NOT NULL,
    team_id       TEXT NOT NULL REFERENCES teams(id) ON DELETE CASCADE,
    member_id     TEXT,
    task_id       TEXT,
    run_id        TEXT,
    session_id    TEXT,                          -- forward-ref to team_session_links (M2.5); bare TEXT
    tool_call_id  TEXT,                          -- forward-ref to mailbox/receipts (M3b); bare TEXT
    event_type    TEXT NOT NULL,
    action        TEXT,
    resource_type TEXT,
    resource_ref  TEXT,
    input_hash    TEXT,
    summary       TEXT,
    decision      TEXT,
    scope         TEXT,
    created_at    INTEGER NOT NULL
);

-- Indexes (10 total; all start with 'idx_team' for the test's LIKE filter)
CREATE INDEX IF NOT EXISTS idx_teams_workspace_status            ON teams(workspace_id, status);
CREATE INDEX IF NOT EXISTS idx_team_members_team_status          ON team_members(team_id, status);
CREATE INDEX IF NOT EXISTS idx_team_members_team_session         ON team_members(team_id, session_id);
CREATE INDEX IF NOT EXISTS idx_team_tasks_team_status_priority   ON team_tasks(team_id, status, priority, created_at);
CREATE INDEX IF NOT EXISTS idx_team_tasks_team_assignee_status   ON team_tasks(team_id, assignee_member_id, status);
CREATE INDEX IF NOT EXISTS idx_team_runs_team_member_status      ON team_runs(team_id, member_id, status);
CREATE INDEX IF NOT EXISTS idx_team_runs_team_task               ON team_runs(team_id, task_id);
CREATE UNIQUE INDEX IF NOT EXISTS idx_team_events_team_seq       ON team_events(team_id, seq);
CREATE INDEX IF NOT EXISTS idx_team_events_workspace_team_created ON team_events(workspace_id, team_id, created_at);
CREATE INDEX IF NOT EXISTS idx_team_audit_team_created           ON team_audit_events(team_id, created_at);

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
-- Drop in reverse FK-dependency order. Indexes are dropped
-- implicitly with their tables, matching initial.sql's Down shape.
DROP TABLE IF EXISTS team_audit_events;
DROP TABLE IF EXISTS team_events;
DROP TABLE IF EXISTS team_event_counters;
DROP TABLE IF EXISTS team_runs;
DROP TABLE IF EXISTS team_tasks;
DROP TABLE IF EXISTS team_members;
DROP TABLE IF EXISTS teams;
-- +goose StatementEnd
