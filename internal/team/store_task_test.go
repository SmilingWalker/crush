package team

import (
	"context"
	"database/sql"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

// newTaskStoreFixture seeds a team + member so task/run FKs are satisfiable.
// Returns the TaskStore, the *sql.DB (for BeginTx in concurrent tests), and a
// helper that opens a committed tx scope.
func newTaskStoreFixture(t *testing.T) (TaskStore, *sql.DB) {
	sqlDB, q := newStoreFixture(t)
	teams := NewTeamStore(q)
	members := NewMemberStore(q)
	ctx := context.Background()
	runTx(t, sqlDB, func(tx *sql.Tx) error {
		_, err := teams.CreateTeam(ctx, tx, Team{ID: "t1", WorkspaceID: "ws", LeaderSessionID: "l", Name: "T", Status: TeamCreated, CreatedAt: time.UnixMilli(1), UpdatedAt: time.UnixMilli(1)})
		return err
	})
	runTx(t, sqlDB, func(tx *sql.Tx) error {
		_, err := members.CreateMember(ctx, tx, TeamMember{ID: "m1", TeamID: "t1", Name: "coder", Role: "programmer", AgentProfile: "{}", Status: MemberCreated, CreatedAt: time.UnixMilli(1), UpdatedAt: time.UnixMilli(1)})
		return err
	})
	return NewTaskStore(q), sqlDB
}

func TestTaskStore_CreateGetListUpdateCAS(t *testing.T) {
	tasks, sqlDB := newTaskStoreFixture(t)
	ctx := context.Background()

	var created TeamTask
	runTx(t, sqlDB, func(tx *sql.Tx) error {
		var err error
		created, err = tasks.CreateTask(ctx, tx, TeamTask{
			ID: "tk1", TeamID: "t1", Title: "Do thing", CreatedByMemberID: "m1", Priority: 5,
			CreatedAt: time.UnixMilli(1), UpdatedAt: time.UnixMilli(1),
		})
		return err
	})
	assert.Equal(t, TaskQueued, created.Status)
	assert.Equal(t, 1, created.Version)

	var got TeamTask
	runTx(t, sqlDB, func(tx *sql.Tx) error {
		var err error
		got, err = tasks.GetTask(ctx, tx, "t1", "tk1")
		return err
	})
	assert.Equal(t, "Do thing", got.Title)

	runTx(t, sqlDB, func(tx *sql.Tx) error {
		_, err := tasks.CreateTask(ctx, tx, TeamTask{ID: "tk2", TeamID: "t1", Title: "two", CreatedByMemberID: "m1", Priority: 3, CreatedAt: time.UnixMilli(2), UpdatedAt: time.UnixMilli(2)})
		return err
	})

	var listed []TeamTask
	runTx(t, sqlDB, func(tx *sql.Tx) error {
		var err error
		listed, err = tasks.ListTasks(ctx, tx, "t1")
		return err
	})
	assert.Len(t, listed, 2)
	assert.Equal(t, "tk1", listed[0].ID, "ListTasks ordered by priority DESC (tk1=5 before tk2=3)")

	// CAS with WRONG version → ErrVersionConflict.
	runTxExpectErr(t, sqlDB, func(tx *sql.Tx) error {
		_, err := tasks.UpdateTaskCAS(ctx, tx, UpdateTaskCASRequest{ID: "tk1", TeamID: "t1", Status: TaskInProgress, ExpectedVersion: 99, UpdatedAt: time.UnixMilli(9)})
		return err
	}, ErrVersionConflict)

	// CAS with CORRECT version → bumps to 2.
	var updated TeamTask
	runTx(t, sqlDB, func(tx *sql.Tx) error {
		var err error
		updated, err = tasks.UpdateTaskCAS(ctx, tx, UpdateTaskCASRequest{ID: "tk1", TeamID: "t1", Status: TaskInProgress, ExpectedVersion: 1, UpdatedAt: time.UnixMilli(10)})
		return err
	})
	assert.Equal(t, 2, updated.Version)
}

func TestTaskStore_ClaimNextTask_NoQueuedReturnsErrNoTaskAvailable(t *testing.T) {
	tasks, sqlDB := newTaskStoreFixture(t)
	runTxExpectErr(t, sqlDB, func(tx *sql.Tx) error {
		_, err := tasks.ClaimNextTask(context.Background(), tx, ClaimNextTaskRequest{TeamID: "t1", AssigneeMemberID: "m1", UpdatedAt: time.UnixMilli(1)})
		return err
	}, ErrNoTaskAvailable)
}

// TestTaskStore_ClaimNextTask_SingleWinnerUnder10Goroutines is acceptance #3:
// 10 goroutines race to claim ONE queued task → exactly 1 winner, the other 9
// get no task. SetMaxOpenNons(1) in the fixture serializes all DB access
// through one connection, so the 10 BeginTx calls queue and execute one at a
// time; the atomic UPDATE guard (WHERE status='queued') in ClaimNextTask makes
// the winner deterministic: the first claim flips status to 'assigned', the
// next 9 match zero rows → ErrNoTaskAvailable. NO -race (cgo broken; the
// invariant holds at the DB layer regardless of goroutine interleaving).
func TestTaskStore_ClaimNextTask_SingleWinnerUnder10Goroutines(t *testing.T) {
	sqlDB, q := newStoreFixture(t)
	teams := NewTeamStore(q)
	tasks := NewTaskStore(q)
	ctx := context.Background()

	runTx(t, sqlDB, func(tx *sql.Tx) error {
		_, err := teams.CreateTeam(ctx, tx, Team{ID: "t1", WorkspaceID: "ws", LeaderSessionID: "l", Name: "T", Status: TeamCreated, CreatedAt: time.UnixMilli(1), UpdatedAt: time.UnixMilli(1)})
		return err
	})
	runTx(t, sqlDB, func(tx *sql.Tx) error {
		_, err := tasks.CreateTask(ctx, tx, TeamTask{ID: "only", TeamID: "t1", Title: "only one", CreatedByMemberID: "m1", Priority: 5, CreatedAt: time.UnixMilli(1), UpdatedAt: time.UnixMilli(1)})
		return err
	})

	const N = 10
	var (
		wg      sync.WaitGroup
		mu      sync.Mutex
		wins    int
		noAvail int
		other   int
	)
	for i := range N {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			tx, err := sqlDB.BeginTx(ctx, nil)
			if err != nil {
				// Under SetMaxOpenConns(1) contention a BeginTx could fail
				// (busy/locked) — count it as "no task" rather than passing
				// a nil tx to ClaimNextTask (which would panic in WithTx).
				mu.Lock()
				other++
				mu.Unlock()
				return
			}
			_, err = tasks.ClaimNextTask(ctx, tx, ClaimNextTaskRequest{TeamID: "t1", AssigneeMemberID: "m1", UpdatedAt: time.UnixMilli(int64(idx))})
			if err == nil {
				_ = tx.Commit()
				mu.Lock()
				wins++
				mu.Unlock()
				return
			}
			_ = tx.Rollback()
			mu.Lock()
			defer mu.Unlock()
			if errors.Is(err, ErrNoTaskAvailable) {
				noAvail++
			} else {
				// Any other error (e.g. transient "database is locked" under
				// single-conn contention) also means "no task won".
				other++
			}
		}(i)
	}
	wg.Wait()

	assert.Equal(t, 1, wins, "exactly 1 goroutine wins the single task")
	assert.Equal(t, 9, noAvail+other, "the other 9 goroutines get no task")
}
