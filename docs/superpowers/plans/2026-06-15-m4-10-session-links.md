# M4-10: Session Links Plan

## Overview

1 天。`team_session_links` 表 + `SessionLinkStore` + `RecoverMemberSession`。leader/member
session 持久化映射，restart 后可恢复 session link。

Dependency: M4-02 TeamRunner + M3-01 表骨架（teams/team_members 已存在）。

## Seam 1: Migration

New goose migration `internal/db/migrations/20260616000000_create_session_links.sql`:

```sql
-- +goose Up
-- +goose StatementBegin

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
```

Columns: `id` (PK, UUID), `team_id` (FK CASCADE), `member_id` (FK CASCADE), `session_id` (TEXT NOT NULL),
`link_type` (leader|member|delegate, default 'member'), `linked_at` (epoch millis).

Unique constraint on `(team_id, session_id)` — one session can only be linked once per team.

FK pattern matches M3-01: REFERENCES teams(id) ON DELETE CASCADE + REFERENCES team_members(id) ON DELETE CASCADE.
The existing `team_audit_events.session_id` forward-ref comment stays — audit.session_id remains bare TEXT (no FK added
in this milestone; that column serves audit-only read paths and an FK would couple the migration order).

## Seam 2: sqlc queries

Hand-authored in `internal/db/sql/team_queries.sql` (append after existing M4-04 mailbox queries):

```sql
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
```

sqlc generates: `InsertSessionLink`, `GetSessionLinkByMember`, `GetSessionLinksByTeam` on Querier +
`TeamSessionLink` row type in models.go + param structs in team_queries.sql.go.

## Seam 3: Domain type + convert

`models.go` — new `TeamSessionLink` domain struct:

```go
type TeamSessionLink struct {
    ID        string    `json:"id"`
    TeamID    string    `json:"team_id"`
    MemberID  string    `json:"member_id"`
    SessionID string    `json:"session_id"`
    LinkType  string    `json:"link_type"`
    LinkedAt  time.Time `json:"linked_at"`
}
```

`convert.go` — new `toSessionLink(r db.TeamSessionLink) TeamSessionLink` converter (epoch millis → time.Time).

## Seam 4: SessionLinkStore

New file `internal/team/store_session_link.go`:

```go
type SessionLinkStore interface {
    CreateSessionLink(ctx context.Context, tx *sql.Tx, link TeamSessionLink) (TeamSessionLink, error)
    GetSessionLinkByMember(ctx context.Context, tx *sql.Tx, teamID, memberID string) (TeamSessionLink, error)
    GetSessionLinksByTeam(ctx context.Context, tx *sql.Tx, teamID string) ([]TeamSessionLink, error)
}

type sqlcSessionLinkStore struct {
    q *db.Queries
}

func NewSessionLinkStore(q *db.Queries) SessionLinkStore { ... }
```

Methods mirror the existing store pattern (store_team.go, store_member.go): q.WithTx(tx), db params, convert, wrap error.

## Seam 5: RecoverMemberSession (Service layer)

Add to Service interface + teamService:

```go
// RecoverMemberSession returns the most recent session_id linked to a member.
// Returns ("", nil) if no link exists (member has no linked session yet).
RecoverMemberSession(ctx context.Context, teamID, memberID string) (string, error)
```

Implementation: open read tx, call store.GetSessionLinkByMember, return SessionID or empty string.

Wire `SessionLinkStore` into `teamService` struct + `NewService` constructor (new param, stored as field).
M4-04 MailboxStore is already wired the same way; follow the same pattern.

## Seam 6: TeamRunner integration

`StartTeam` currently loads members from DB and starts MemberRunners. With session links, each
member's session should be recovered:

```go
// In StartTeam, after loading member list:
for _, member := range members {
    if member.Status == MemberStopped || member.Status == MemberFailed {
        continue
    }
    if _, exists := t.members[member.ID]; exists {
        continue
    }
    // Recover session link for this member.
    sessionID, err := t.svc.RecoverMemberSession(ctx, teamID, member.ID)
    if err != nil { /* log warning, continue */ }
    if sessionID != "" {
        // member has an existing session — resume it
        member.SessionID = &sessionID
    }
    spec := agent.AgentSpec{}
    mr := NewMemberRunner(member.ID, teamID, spec, t.factory, t.svc)
    t.members[member.ID] = mr
    go mr.Start(ctx)
}
```

The session link is created when a session is first established for a member. For M4-10 scope,
the link is created during SpawnMember (when we have session_id from agent run), and recovered
during StartTeam (restart). The actual session_id assignment during spawn is already handled
by MemberRunner's Run loop; the link write will be a follow-up call after Run produces the
session_id. For the initial implementation, the RecoverMemberSession path is tested by
pre-seeding a link directly.

## Seam 7: Test strategy

1. **Migration test** — extend `internal/db/migrations_team_tables_test.go` or write a small test that
   creates the table, inserts a row, verifies FK CASCADE (delete team → link deleted).

2. **Store test** — `internal/team/store_session_link_test.go`: create link, get by member, get by team,
   verify empty result when no link exists.

3. **Service test** — `internal/team/service_test.go` (extend): RecoverMemberSession returns session_id
   when link exists, "" when no link.

4. **TeamRunner test** — `internal/team/team_runner_test.go` (extend): StartTeam recovers session
   for existing linked members.

## Files changed

| File | Action |
|------|--------|
| `internal/db/migrations/20260616000000_create_session_links.sql` | New |
| `internal/db/sql/team_queries.sql` | Append 3 queries |
| `internal/db/models.go` | Regenerated by sqlc |
| `internal/db/team_queries.sql.go` | Regenerated by sqlc |
| `internal/db/querier.go` | Regenerated by sqlc |
| `internal/team/models.go` | Add TeamSessionLink domain type |
| `internal/team/convert.go` | Add toSessionLink converter |
| `internal/team/store_session_link.go` | New: SessionLinkStore |
| `internal/team/store_session_link_test.go` | New: store tests |
| `internal/team/service.go` | Add SessionLinkStore param + RecoverMemberSession |
| `internal/team/team_runner.go` | StartTeam calls RecoverMemberSession |

## Commit plan

Single commit: `feat(team): add M4-10 Session Links (team_session_links table + RecoverMemberSession)`
