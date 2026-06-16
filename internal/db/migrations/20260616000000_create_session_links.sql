-- +goose Up
-- +goose StatementBegin

-- M4-10: team_session_links connects members to their sessions.
-- FK pattern matches M3-01: REFERENCES teams/id + team_members/id ON DELETE CASCADE.
-- UNIQUE(team_id, session_id) — one session linked once per team.

CREATE TABLE IF NOT EXISTS team_session_links (
    id          TEXT PRIMARY KEY,
    team_id     TEXT NOT NULL REFERENCES teams(id) ON DELETE CASCADE,
    member_id   TEXT NOT NULL REFERENCES team_members(id) ON DELETE CASCADE,
    session_id  TEXT NOT NULL,
    link_type   TEXT NOT NULL DEFAULT 'member',
    linked_at   INTEGER NOT NULL
);

CREATE UNIQUE INDEX IF NOT EXISTS idx_session_links_team_session
    ON team_session_links(team_id, session_id);
CREATE INDEX IF NOT EXISTS idx_session_links_member
    ON team_session_links(member_id);

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS team_session_links;
-- +goose StatementEnd
