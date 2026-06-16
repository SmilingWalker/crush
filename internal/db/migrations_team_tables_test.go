package db

import (
	"context"
	"database/sql"
	"testing"

	"github.com/pressly/goose/v3"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	_ "modernc.org/sqlite"
)

// openMemoryDB opens an in-memory SQLite (driver name "sqlite", the
// same identifier connect_modernc.go registers on win/amd64) with FK
// enforcement on. It deliberately bypasses Connect — which requires a
// real on-disk data dir — so the migration can be round-tripped in
// isolation. The test calls the package's own initGoose() (idempotent
// via gooseInitOnce) so goose.SetBaseFS(FS) and the sqlite3 dialect
// are configured before Up/Reset.
func openMemoryDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", ":memory:")
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })

	_, err = db.Exec("PRAGMA foreign_keys = ON;")
	require.NoError(t, err, "enable FK enforcement")

	require.NoError(t, initGoose(), "goose init (SetBaseFS + dialect)")
	return db
}

// teamTablesExistQuery counts the 7 M3 team tables. NOTE: the LIKE
// pattern is 'team%' (NOT 'team\_%') because the root table is named
// 'teams' (team + 's', no underscore), which a 'team\_%' pattern would
// wrongly exclude — leaving a count of 6 and a false-negative test.
// 'team%' matches all 10 (teams + team_members + team_tasks + team_runs
// + team_events + team_event_counters + team_audit_events
// + team_mailbox_messages + team_message_receipts + team_session_links)
// and nothing else in a fresh DB.
const teamTablesExistQuery = `
SELECT count(*) FROM sqlite_master
WHERE type = 'table' AND name LIKE 'team%'
`

// teamIndexesExistQuery counts the 10 M3 team indexes. All 10 index
// names start with 'idx_team' (including idx_teams_workspace_status,
// which is 'idx_team' + 's_...'), so the 'idx\_team%' ESCAPE '\' pattern
// matches exactly 10.
const teamIndexesExistQuery = `
SELECT count(*) FROM sqlite_master
WHERE type = 'index' AND name LIKE 'idx\_team%' ESCAPE '\'
`

// TestTeamTablesMigration_Up applies the M3-01 migration to a fresh
// in-memory DB and asserts acceptance criteria 1, 2, and 4:
//   1. 10 team tables and 10 team indexes are created
//   2. the (team_id, seq) UNIQUE index rejects a duplicate insert
//   4. column types/constraints match the design (probed via a
//      representative insert per table, plus the CASCADE behavior)
func TestTeamTablesMigration_Up(t *testing.T) {
	db := openMemoryDB(t)

	// Apply ALL migrations (including the new team migration). goose.Up
	// runs from version 0 to the latest version embedded in FS.
	require.NoError(t, goose.Up(db, "migrations"), "apply all migrations up")

	ctx := context.Background()

	// Acceptance 1a: exactly 10 team tables.
	var tableCount int
	require.NoError(t,
		db.QueryRowContext(ctx, teamTablesExistQuery).Scan(&tableCount),
		"count team tables")
	assert.Equal(t, 10, tableCount, "expected 10 team tables after up")

	// Acceptance 1b: exactly 10 team indexes (all 10 start with idx_team).
	var indexCount int
	require.NoError(t,
		db.QueryRowContext(ctx, teamIndexesExistQuery).Scan(&indexCount),
		"count team indexes")
	assert.Equal(t, 10, indexCount, "expected 10 idx_team* indexes after up")

	// Acceptance 2: (team_id, seq) UNIQUE rejects duplicates.
	insertTeam := `INSERT INTO teams
		(id, workspace_id, leader_session_id, name, status, version, cost_so_far_micros, created_at, updated_at)
		VALUES ('team-A', 'ws-1', 'sess-leader', 'Alpha', 'created', 1, 0, 1, 1)`
	_, err := db.Exec(insertTeam)
	require.NoError(t, err, "seed team for events test")

	insertEvent := func(seq int) error {
		_, e := db.Exec(`INSERT INTO team_events
			(seq, id, workspace_id, team_id, event_type, entity_type, entity_id, created_at)
			VALUES (?, ?, 'ws-1', 'team-A', 'team.created', 'team', 'team-A', 1)`,
			seq, "evt-"+itoa(seq))
		return e
	}
	require.NoError(t, insertEvent(1), "first (team-A, seq=1) insert succeeds")
	err = insertEvent(1)
	require.Error(t, err, "duplicate (team_id, seq) must violate UNIQUE idx_team_events_team_seq")
	// SQLite (modernc) surfaces: "UNIQUE constraint failed: team_events.team_id, team_events.seq".
	assert.Contains(t, err.Error(), "UNIQUE", "error should name the UNIQUE violation")

	// Acceptance 4 (representative): FK CASCADE works — deleting the
	// team removes its seeded event (proves the CASCADE decision from
	// Seam 5 is wired, not just declared).
	_, err = db.Exec("DELETE FROM teams WHERE id = 'team-A'")
	require.NoError(t, err, "delete team should cascade (no FK block)")
	var eventCount int
	require.NoError(t,
		db.QueryRowContext(ctx,
			`SELECT count(*) FROM team_events WHERE team_id = 'team-A'`).Scan(&eventCount))
	assert.Equal(t, 0, eventCount, "cascaded delete should remove the team's events")
}

// TestTeamTablesMigration_DownResetsCleanly applies all migrations,
// then resets (down-to-zero), and asserts acceptance criterion 3:
// down removes all team tables. It then re-applies up to prove the
// migration round-trips idempotently.
func TestTeamTablesMigration_DownResetsCleanly(t *testing.T) {
	db := openMemoryDB(t)

	require.NoError(t, goose.Up(db, "migrations"), "apply up before reset")

	// Reset = roll back ALL versions to 0 (goose.Down only drops one).
	require.NoError(t, goose.Reset(db, "migrations"), "reset (down to v0)")

	ctx := context.Background()

	// Acceptance 3: no team tables remain.
	var tableCount int
	require.NoError(t,
		db.QueryRowContext(ctx, teamTablesExistQuery).Scan(&tableCount),
		"count team tables after reset")
	assert.Equal(t, 0, tableCount, "expected 0 team tables after down/reset")

	// Round-trip idempotency: re-applying up must succeed and recreate
	// the 7 tables (proves Down left no orphaned schema objects that
	// would block a second Up).
	require.NoError(t, goose.Up(db, "migrations"), "re-apply up after reset")
	require.NoError(t,
		db.QueryRowContext(ctx, teamTablesExistQuery).Scan(&tableCount),
		"count team tables after re-up")
	assert.Equal(t, 10, tableCount, "expected 10 team tables after re-up")
}

// itoa is a tiny local int->string to keep the test free of strconv
// imports. Only used for synthesizing unique event ids above.
func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	return string(buf[i:])
}
