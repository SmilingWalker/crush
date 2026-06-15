package team

import (
	"context"
	"database/sql"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

// newRunStoreFixture seeds a team + member + task; returns RunStore + sqlDB +
// a raw-UPDATE helper to flip a run's status (InsertRun forces 'queued', but
// FinishRun/MarkRunTerminal guard on specific statuses).
func newRunStoreFixture(t *testing.T) (RunStore, *sql.DB, func(teamID, runID, status string)) {
	sqlDB, q := newStoreFixture(t)
	teams := NewTeamStore(q)
	members := NewMemberStore(q)
	tasks := NewTaskStore(q)
	ctx := context.Background()
	runTx(t, sqlDB, func(tx *sql.Tx) error {
		_, err := teams.CreateTeam(ctx, tx, Team{ID: "t1", WorkspaceID: "ws", LeaderSessionID: "l", Name: "T", Status: TeamCreated, CreatedAt: time.UnixMilli(1), UpdatedAt: time.UnixMilli(1)})
		return err
	})
	runTx(t, sqlDB, func(tx *sql.Tx) error {
		_, err := members.CreateMember(ctx, tx, TeamMember{ID: "m1", TeamID: "t1", Name: "coder", Role: "programmer", AgentProfile: "{}", Status: MemberCreated, CreatedAt: time.UnixMilli(1), UpdatedAt: time.UnixMilli(1)})
		return err
	})
	runTx(t, sqlDB, func(tx *sql.Tx) error {
		_, err := tasks.CreateTask(ctx, tx, TeamTask{ID: "tk1", TeamID: "t1", Title: "x", CreatedByMemberID: "m1", Priority: 1, CreatedAt: time.UnixMilli(1), UpdatedAt: time.UnixMilli(1)})
		return err
	})
	setStatus := func(teamID, runID, status string) {
		runTx(t, sqlDB, func(tx *sql.Tx) error {
			_, err := tx.ExecContext(ctx, "UPDATE team_runs SET status = ? WHERE id = ? AND team_id = ?", status, runID, teamID)
			return err
		})
	}
	return NewRunStore(q), sqlDB, setStatus
}

func TestRunStore_StartGetHeartbeat(t *testing.T) {
	runs, sqlDB, _ := newRunStoreFixture(t)
	ctx := context.Background()

	var started TeamRun
	runTx(t, sqlDB, func(tx *sql.Tx) error {
		var err error
		started, err = runs.StartRun(ctx, tx, TeamRun{
			ID: "r1", TeamID: "t1", MemberID: "m1", SessionID: "sess-1",
			HeartbeatAt: ptrTime(time.UnixMilli(50)),
		})
		return err
	})
	assert.Equal(t, RunQueued, started.Status)
	assert.Equal(t, 1, started.Attempt)
	assert.Equal(t, time.UnixMilli(50), *started.HeartbeatAt)

	var got TeamRun
	runTx(t, sqlDB, func(tx *sql.Tx) error {
		var err error
		got, err = runs.GetRun(ctx, tx, "t1", "r1")
		return err
	})
	assert.Equal(t, "sess-1", got.SessionID)

	// Heartbeat update.
	runTx(t, sqlDB, func(tx *sql.Tx) error {
		return runs.UpdateRunHeartbeat(ctx, tx, "r1", time.UnixMilli(200))
	})
	runTx(t, sqlDB, func(tx *sql.Tx) error {
		var err error
		got, err = runs.GetRun(ctx, tx, "t1", "r1")
		return err
	})
	assert.Equal(t, time.UnixMilli(200), *got.HeartbeatAt)
}

func TestRunStore_FindStaleRuns(t *testing.T) {
	runs, sqlDB, setStatus := newRunStoreFixture(t)
	ctx := context.Background()

	// r1: heartbeat=200, status=running → stale at cutoff 300, fresh at 100.
	runTx(t, sqlDB, func(tx *sql.Tx) error {
		_, err := runs.StartRun(ctx, tx, TeamRun{ID: "r1", TeamID: "t1", MemberID: "m1", SessionID: "s1", HeartbeatAt: ptrTime(time.UnixMilli(200))})
		return err
	})
	setStatus("t1", "r1", "running")

	// r2: heartbeat=500, status=running → fresh at both cutoffs.
	runTx(t, sqlDB, func(tx *sql.Tx) error {
		_, err := runs.StartRun(ctx, tx, TeamRun{ID: "r2", TeamID: "t1", MemberID: "m1", SessionID: "s2", HeartbeatAt: ptrTime(time.UnixMilli(500))})
		return err
	})
	setStatus("t1", "r2", "running")

	var fresh []TeamRun
	runTx(t, sqlDB, func(tx *sql.Tx) error {
		var err error
		fresh, err = runs.FindStaleRuns(ctx, tx, time.UnixMilli(100))
		return err
	})
	assert.Empty(t, fresh, "no run is stale at cutoff=100")

	var stale []TeamRun
	runTx(t, sqlDB, func(tx *sql.Tx) error {
		var err error
		stale, err = runs.FindStaleRuns(ctx, tx, time.UnixMilli(300))
		return err
	})
	assert.Len(t, stale, 1)
	assert.Equal(t, "r1", stale[0].ID)
}

func TestRunStore_FinishRun_StatusGuard(t *testing.T) {
	runs, sqlDB, setStatus := newRunStoreFixture(t)
	ctx := context.Background()

	runTx(t, sqlDB, func(tx *sql.Tx) error {
		_, err := runs.StartRun(ctx, tx, TeamRun{ID: "r1", TeamID: "t1", MemberID: "m1", SessionID: "s1"})
		return err
	})

	// FinishRun guards on status='running'; InsertRun sets 'queued', so a
	// FinishRun while still queued is a no-op (exec returns nil, 0 rows).
	runTx(t, sqlDB, func(tx *sql.Tx) error {
		return runs.FinishRun(ctx, tx, "t1", "r1", 1000, 500, 42, time.UnixMilli(250))
	})
	var got TeamRun
	runTx(t, sqlDB, func(tx *sql.Tx) error {
		var err error
		got, err = runs.GetRun(ctx, tx, "t1", "r1")
		return err
	})
	assert.Equal(t, RunQueued, got.Status, "FinishRun on a queued run is a no-op (guard misses)")

	// Flip to running → FinishRun now matches and completes.
	setStatus("t1", "r1", "running")
	runTx(t, sqlDB, func(tx *sql.Tx) error {
		return runs.FinishRun(ctx, tx, "t1", "r1", 1000, 500, 42, time.UnixMilli(250))
	})
	runTx(t, sqlDB, func(tx *sql.Tx) error {
		var err error
		got, err = runs.GetRun(ctx, tx, "t1", "r1")
		return err
	})
	assert.Equal(t, RunCompleted, got.Status)
	assert.Equal(t, int64(42), *got.CostMicros)
	assert.Equal(t, time.UnixMilli(250), *got.FinishedAt)
}

func TestRunStore_MarkRunTerminal(t *testing.T) {
	runs, sqlDB, setStatus := newRunStoreFixture(t)
	ctx := context.Background()

	runTx(t, sqlDB, func(tx *sql.Tx) error {
		_, err := runs.StartRun(ctx, tx, TeamRun{ID: "r1", TeamID: "t1", MemberID: "m1", SessionID: "s1"})
		return err
	})
	setStatus("t1", "r1", "running")

	runTx(t, sqlDB, func(tx *sql.Tx) error {
		return runs.MarkRunTerminal(ctx, tx, "t1", "r1", RunFailed, "boom", "partial", time.UnixMilli(300))
	})
	var got TeamRun
	runTx(t, sqlDB, func(tx *sql.Tx) error {
		var err error
		got, err = runs.GetRun(ctx, tx, "t1", "r1")
		return err
	})
	assert.Equal(t, RunFailed, got.Status)
	assert.Equal(t, "boom", got.Error)
	assert.Equal(t, "partial", got.UsageStatus)
	assert.Equal(t, time.UnixMilli(300), *got.FinishedAt)
}

// ptrTime is a small local helper for test-only time pointers.
func ptrTime(t time.Time) *time.Time { return &t }
