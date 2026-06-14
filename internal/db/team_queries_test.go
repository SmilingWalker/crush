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

// newPreparedQueries builds an in-memory SQLite with FK enforcement on,
// applies all migrations (so the M3-01 team schema exists), and returns
// a *Queries built via New() — the same construction path production uses
// (production never calls Prepare; it uses New, whose exec/query/queryRow
// fall through to raw-string execution against the live *sql.DB).
//
// NOTE on Prepare vs New: an earlier draft of this helper called
// Prepare(context.Background(), db). Prepare compiles ALL queries in the
// package (every *sql.Stmt), which surfaced a PRE-EXISTING codebase bug
// unrelated to M3-02: the checked-in ListNewFiles query
// (internal/db/sql/files.sql:58, files.sql.go:244) does `WHERE is_new = 1`
// but no migration adds an `is_new` column to the `files` table — so
// Prepare fails with "no such column: is_new". This drift is dormant in
// production (production uses New, not Prepare, and never calls ListNewFiles
// through a prepared statement). It is OUT OF SCOPE for M3-02 (team queries
// only); flagged to team-lead for a separate follow-up. Using New() here
// sidesteps the drift while still fully exercising the 13 team queries
// through the production code path.
func newPreparedQueries(t *testing.T) (*Queries, *sql.DB) {
	t.Helper()
	db, err := sql.Open("sqlite", ":memory:")
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })

	_, err = db.Exec("PRAGMA foreign_keys = ON;")
	require.NoError(t, err)

	require.NoError(t, initGoose(), "goose init")
	require.NoError(t, goose.Up(db, "migrations"), "apply migrations up")

	return New(db), db
}

// seedTeam inserts a team and returns it. Used by most tests.
func seedTeam(t *testing.T, q *Queries, id, workspaceID string) Team {
	t.Helper()
	team, err := q.InsertTeam(context.Background(), InsertTeamParams{
		ID:              id,
		WorkspaceID:     workspaceID,
		LeaderSessionID: "leader-1",
		Name:            "Alpha",
		CreatedAt:       1000,
		UpdatedAt:       1000,
	})
	require.NoError(t, err)
	return team
}

func TestInsertTeam_GetTeam_ListTeams_ArchiveTeam(t *testing.T) {
	q, _ := newPreparedQueries(t)
	ctx := context.Background()

	team := seedTeam(t, q, "team-A", "ws-1")
	assert.Equal(t, "created", team.Status, "InsertTeam default status")
	assert.Equal(t, int64(1), team.Version, "InsertTeam default version")
	assert.Equal(t, int64(0), team.CostSoFarMicros, "InsertTeam default cost")

	got, err := q.GetTeam(ctx, "team-A")
	require.NoError(t, err)
	assert.Equal(t, team.ID, got.ID)

	// Second team in same workspace; a third archived one (excluded by ListTeams).
	seedTeam(t, q, "team-B", "ws-1")
	seedTeam(t, q, "team-C", "ws-1")
	err = q.ArchiveTeam(ctx, ArchiveTeamParams{ArchivedAt: sql.NullInt64{Int64: 2000, Valid: true}, UpdatedAt: 2000, ID: "team-C"})
	require.NoError(t, err)

	teams, err := q.ListTeams(ctx, "ws-1")
	require.NoError(t, err)
	assert.Len(t, teams, 2, "ListTeams excludes archived and returns non-archived only")
	// Assert the archived team-C is absent.
	for _, tm := range teams {
		assert.NotEqual(t, "team-C", tm.ID)
	}
}

func TestUpdateTaskCAS_VersionMismatchReturnsErrNoRows(t *testing.T) {
	q, _ := newPreparedQueries(t)
	ctx := context.Background()
	seedTeam(t, q, "team-A", "ws-1")

	// Seed a task via raw INSERT (M3-02 has no InsertTask query; tasks are
	// created by M3-04 store layer later). The test owns the row.
	_, err := q.db.ExecContext(ctx, `INSERT INTO team_tasks
		(id, team_id, title, status, created_by_member_id, priority, version, created_at, updated_at)
		VALUES ('task-1', 'team-A', 'Do thing', 'queued', 'member-1', 0, 1, 100, 100)`)
	require.NoError(t, err)

	// CAS with the WRONG version (2 instead of 1) → no row matches → ErrNoRows.
	_, err = q.UpdateTaskCAS(ctx, UpdateTaskCASParams{
		Status:    "in_progress",
		UpdatedAt: 200,
		ID:        "task-1",
		TeamID:    "team-A",
		Version:   2, // wrong — actual is 1
	})
	require.ErrorIs(t, err, sql.ErrNoRows, "version mismatch must yield sql.ErrNoRows (acceptance 2)")

	// CAS with the CORRECT version (1) → succeeds, version bumps to 2.
	updated, err := q.UpdateTaskCAS(ctx, UpdateTaskCASParams{
		Status:           "in_progress",
		AssigneeMemberID: sql.NullString{String: "member-1", Valid: true},
		UpdatedAt:        200,
		ID:               "task-1",
		TeamID:           "team-A",
		Version:          1,
	})
	require.NoError(t, err)
	assert.Equal(t, int64(2), updated.Version, "CAS increments version")
	assert.Equal(t, "in_progress", updated.Status)
}

func TestClaimNextTask_SingleWinnerUnderContention(t *testing.T) {
	q, _ := newPreparedQueries(t)
	ctx := context.Background()
	seedTeam(t, q, "team-A", "ws-1")

	// Seed exactly ONE queued task.
	_, err := q.db.ExecContext(ctx, `INSERT INTO team_tasks
		(id, team_id, title, status, created_by_member_id, priority, version, created_at, updated_at)
		VALUES ('only-task', 'team-A', 'Only one', 'queued', 'member-1', 5, 1, 100, 100)`)
	require.NoError(t, err)

	// First claim wins (single-connection serialization; no -race).
	winner, err := q.ClaimNextTask(ctx, ClaimNextTaskParams{
		TeamID:           "team-A",
		AssigneeMemberID: sql.NullString{String: "member-1", Valid: true},
		UpdatedAt:        200,
		TeamID_2:         "team-A",
	})
	require.NoError(t, err)
	assert.Equal(t, "only-task", winner.ID, "first claimant wins the task")
	assert.Equal(t, "assigned", winner.Status)

	// Second claim finds no queued task → ErrNoRows (acceptance 3: single winner).
	_, err = q.ClaimNextTask(ctx, ClaimNextTaskParams{
		TeamID:           "team-A",
		AssigneeMemberID: sql.NullString{String: "member-2", Valid: true},
		UpdatedAt:        300,
		TeamID_2:         "team-A",
	})
	require.ErrorIs(t, err, sql.ErrNoRows, "second claimant gets no task — single winner")
}

func TestNextEventSeq_MonotonicNoGap(t *testing.T) {
	q, _ := newPreparedQueries(t)
	ctx := context.Background()
	seedTeam(t, q, "team-A", "ws-1")

	// First call INSERTs the counter row with next_seq=2, returns 2.
	// Subsequent calls conflict+increment: 3, 4, 5. (acceptance 4: monotonic, no gap.)
	want := []int64{2, 3, 4, 5}
	for _, w := range want {
		got, err := q.NextEventSeq(ctx, NextEventSeqParams{
			TeamID:      "team-A",
			UpdatedAt:   100,
			UpdatedAt_2: 100,
		})
		require.NoError(t, err)
		assert.Equal(t, w, got, "NextEventSeq must be monotonic with no gaps")
	}

	// InsertEvent at seq=2 (the first allocatable seq) succeeds and honors the UNIQUE constraint.
	err := q.InsertEvent(ctx, InsertEventParams{
		Seq: 2, ID: "evt-2", WorkspaceID: "ws-1", TeamID: "team-A",
		EventType: "team.created", EntityType: "team", EntityID: "team-A", CreatedAt: 100,
	})
	require.NoError(t, err)
	// Duplicate (team-A, seq=2) must violate the unique index.
	err = q.InsertEvent(ctx, InsertEventParams{
		Seq: 2, ID: "evt-2-dup", WorkspaceID: "ws-1", TeamID: "team-A",
		EventType: "team.created", EntityType: "team", EntityID: "team-A", CreatedAt: 101,
	})
	require.Error(t, err, "duplicate (team_id, seq) must be rejected")
}

func TestListEventsAfter(t *testing.T) {
	q, _ := newPreparedQueries(t)
	ctx := context.Background()
	seedTeam(t, q, "team-A", "ws-1")

	for _, seq := range []int64{2, 3, 4, 5} {
		require.NoError(t, q.InsertEvent(ctx, InsertEventParams{
			Seq: seq, ID: "evt-" + itoaTeam(int(seq)), WorkspaceID: "ws-1", TeamID: "team-A",
			EventType: "x", EntityType: "y", EntityID: "z", CreatedAt: 100,
		}))
	}

	// ListEventsAfter(team-A, after=3) → seqs 4,5 in ASC order, LIMIT 10.
	got, err := q.ListEventsAfter(ctx, ListEventsAfterParams{TeamID: "team-A", Seq: 3, Limit: 10})
	require.NoError(t, err)
	require.Len(t, got, 2)
	assert.Equal(t, int64(4), got[0].Seq)
	assert.Equal(t, int64(5), got[1].Seq)
}

func TestRunHeartbeat_FinishRun_MarkRunTerminal_FindStaleRuns(t *testing.T) {
	q, db := newPreparedQueries(t)
	ctx := context.Background()
	seedTeam(t, q, "team-A", "ws-1")

	// Seed member + run via raw INSERT (no InsertMember/InsertRun query in M3-02).
	_, err := db.ExecContext(ctx, `INSERT INTO team_members
		(id, team_id, name, role, agent_profile, status, last_event_seq, cost_so_far_micros, version, created_at, updated_at)
		VALUES ('member-1', 'team-A', 'coder', 'programmer', '{}', 'created', 0, 0, 1, 100, 100)`)
	require.NoError(t, err)
	_, err = db.ExecContext(ctx, `INSERT INTO team_runs
		(id, team_id, member_id, session_id, status, attempt, heartbeat_at)
		VALUES ('run-1', 'team-A', 'member-1', 'sess-1', 'running', 1, 50)`)
	require.NoError(t, err)

	// UpdateRunHeartbeat.
	require.NoError(t, q.UpdateRunHeartbeat(ctx, UpdateRunHeartbeatParams{
		HeartbeatAt: sql.NullInt64{Int64: 200, Valid: true}, ID: "run-1",
	}))

	// FindStaleRuns: cutoff=100 → run-1 (heartbeat 200) is NOT stale; cutoff=300 → it IS stale.
	fresh, err := q.FindStaleRuns(ctx, sql.NullInt64{Int64: 100, Valid: true})
	require.NoError(t, err)
	assert.Empty(t, fresh, "heartbeat=200 is not stale at cutoff=100")
	stale, err := q.FindStaleRuns(ctx, sql.NullInt64{Int64: 300, Valid: true})
	require.NoError(t, err)
	assert.Len(t, stale, 1)
	assert.Equal(t, "run-1", stale[0].ID)

	// FinishRun: status='running' guard matches → completes.
	require.NoError(t, q.FinishRun(ctx, FinishRunParams{
		FinishedAt:       sql.NullInt64{Int64: 250, Valid: true},
		PromptTokens:     sql.NullInt64{Int64: 1000, Valid: true},
		CompletionTokens: sql.NullInt64{Int64: 500, Valid: true},
		CostMicros:       sql.NullInt64{Int64: 42, Valid: true},
		ID:               "run-1",
		TeamID:           "team-A",
	}))
	// Second FinishRun: status is now 'completed', guard misses → no error (exec is no-op).
	require.NoError(t, q.FinishRun(ctx, FinishRunParams{
		FinishedAt: sql.NullInt64{Int64: 999, Valid: true},
		ID:         "run-1", TeamID: "team-A",
	}))

	// MarkRunTerminal on a fresh running run.
	_, err = db.ExecContext(ctx, `INSERT INTO team_runs
		(id, team_id, member_id, session_id, status, attempt)
		VALUES ('run-2', 'team-A', 'member-1', 'sess-2', 'running', 1)`)
	require.NoError(t, err)
	require.NoError(t, q.MarkRunTerminal(ctx, MarkRunTerminalParams{
		Status:      "failed",
		FinishedAt:  sql.NullInt64{Int64: 300, Valid: true},
		Error:       sql.NullString{String: "boom", Valid: true},
		UsageStatus: sql.NullString{String: "partial", Valid: true},
		ID:          "run-2",
		TeamID:      "team-A",
		Status_2:    "running",
	}))
}

// TestTeamQueriesSQLRuntime is the Task-1 Step-3 runtime smoke: it confirms
// the trickiest SQL strings parse against modernc SQLite through goose-applied
// M3-01 schema (de-risks Seam 4 independently of the full method tests above).
func TestTeamQueriesSQLRuntime(t *testing.T) {
	db, err := sql.Open("sqlite", ":memory:")
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	require.NoError(t, initGoose())
	require.NoError(t, goose.Up(db, "migrations"))
	ctx := context.Background()

	// ClaimNextTask UPDATE ... FROM.
	_, err = db.ExecContext(ctx, `INSERT INTO teams(id,workspace_id,leader_session_id,name,version,cost_so_far_micros,created_at,updated_at) VALUES('t1','ws','l','n',1,0,1,1)`)
	require.NoError(t, err)
	_, err = db.ExecContext(ctx, `INSERT INTO team_tasks(id,team_id,title,status,created_by_member_id,priority,version,created_at,updated_at) VALUES('tk1','t1','x','queued','m',5,1,1,1)`)
	require.NoError(t, err)
	res, err := db.ExecContext(ctx, `WITH next AS (SELECT id FROM team_tasks WHERE team_id='t1' AND status='queued' ORDER BY priority DESC, created_at ASC LIMIT 1)
		UPDATE team_tasks SET assignee_member_id='m', status='assigned', version=version+1, updated_at=2 FROM next WHERE team_tasks.id=next.id AND team_tasks.team_id='t1' AND team_tasks.status='queued'`)
	require.NoError(t, err)
	n, _ := res.RowsAffected()
	assert.Equal(t, int64(1), n, "ClaimNextTask UPDATE FROM affects 1 row")

	// NextEventSeq UPSERT RETURNING.
	var seq int64
	require.NoError(t, db.QueryRowContext(ctx, `INSERT INTO team_event_counters(team_id,next_seq,updated_at) VALUES('t1',2,1) ON CONFLICT(team_id) DO UPDATE SET next_seq=next_seq+1, updated_at=1 RETURNING next_seq`).Scan(&seq))
	assert.Equal(t, int64(2), seq)
	require.NoError(t, db.QueryRowContext(ctx, `INSERT INTO team_event_counters(team_id,next_seq,updated_at) VALUES('t1',2,1) ON CONFLICT(team_id) DO UPDATE SET next_seq=next_seq+1, updated_at=1 RETURNING next_seq`).Scan(&seq))
	assert.Equal(t, int64(3), seq)
}

// itoaTeam is a local int->string (test-only, avoids strconv import churn).
func itoaTeam(n int) string {
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
