-- +goose Up
-- +goose StatementBegin

-- M3b (deferred from M3-01): 2 mailbox tables consumed by M4-04.
-- FK pattern matches M3-01: REFERENCES teams/id + team_members/id
-- ON DELETE CASCADE so deleting a team/member cleans up messages/receipts.

CREATE TABLE IF NOT EXISTS team_mailbox_messages (
    id             TEXT PRIMARY KEY,
    team_id        TEXT NOT NULL REFERENCES teams(id) ON DELETE CASCADE,
    from_member_id TEXT NOT NULL REFERENCES team_members(id) ON DELETE CASCADE,
    kind           TEXT NOT NULL DEFAULT 'message',
    summary        TEXT NOT NULL DEFAULT '',
    payload        TEXT NOT NULL DEFAULT '{}',
    created_at     INTEGER NOT NULL
);

CREATE TABLE IF NOT EXISTS team_message_receipts (
    id           TEXT PRIMARY KEY,
    message_id   TEXT NOT NULL REFERENCES team_mailbox_messages(id) ON DELETE CASCADE,
    to_member_id TEXT NOT NULL REFERENCES team_members(id) ON DELETE CASCADE,
    delivered_at INTEGER,
    read_at      INTEGER
);

CREATE INDEX IF NOT EXISTS idx_mailbox_team_created     ON team_mailbox_messages(team_id, created_at);
CREATE INDEX IF NOT EXISTS idx_mailbox_team_from         ON team_mailbox_messages(team_id, from_member_id);
CREATE INDEX IF NOT EXISTS idx_receipts_member_delivered ON team_message_receipts(to_member_id, delivered_at);
CREATE INDEX IF NOT EXISTS idx_receipts_message          ON team_message_receipts(message_id);

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
-- Drop in reverse FK-dependency order: receipts before messages.
DROP TABLE IF EXISTS team_message_receipts;
DROP TABLE IF EXISTS team_mailbox_messages;
-- +goose StatementEnd
