package team

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestRecoveryReport_TypesAreConstructable verifies the recovery types compile
// and can be instantiated (acceptance #3 foundation — the report is what
// RecoverStaleRuns returns).
func TestRecoveryReport_TypesAreConstructable(t *testing.T) {
	now := time.Now()
	info := StaleRunInfo{
		RunID:         "run-1",
		TeamID:        "team-1",
		MemberID:      "m-1",
		Status:        "running",
		LastHeartbeat: now.Add(-2 * time.Minute),
	}
	report := RecoveryReport{
		InterruptedCount: 1,
		Details:          []StaleRunInfo{info},
	}
	assert.Equal(t, 1, report.InterruptedCount)
	assert.Equal(t, "run-1", report.Details[0].RunID)
	assert.True(t, report.Details[0].LastHeartbeat.Before(now))
}

// TestService_FindStaleRuns verifies the Seam 9 addition: Service.FindStaleRuns
// returns running runs with stale heartbeats.
func TestService_FindStaleRuns(t *testing.T) {
	svc, _ := newServiceFixture(t)
	ctx := context.Background()

	snap, err := svc.CreateTeam(ctx, CreateTeamRequest{WorkspaceID: "ws", LeaderSessionID: "l", Name: "T"})
	require.NoError(t, err)
	m, err := svc.SpawnMember(ctx, SpawnMemberRequest{TeamID: snap.Team.ID, Name: "coder", Role: "programmer", AgentProfile: "{}"})
	require.NoError(t, err)

	// Start a run — heartbeat_at is set to now.
	run, err := svc.StartRun(ctx, StartRunRequest{TeamID: snap.Team.ID, MemberID: m.ID, SessionID: "sess-1"})
	require.NoError(t, err)

	// Cutoff far in the past → no stale runs.
	stale, err := svc.FindStaleRuns(ctx, time.Now().Add(-1*time.Hour))
	require.NoError(t, err)
	assert.Empty(t, stale, "fresh run with recent heartbeat should not be stale")

	// Cutoff far in the future → run IS stale (heartbeat_at < cutoff).
	stale, err = svc.FindStaleRuns(ctx, time.Now().Add(1*time.Hour))
	require.NoError(t, err)
	require.Len(t, stale, 1)
	assert.Equal(t, run.ID, stale[0].ID)
	assert.Equal(t, RunRunning, stale[0].Status)
}

// newSchedulerFixture builds a real Service over in-memory SQLite and wraps it
// in a Scheduler with default config.
func newSchedulerFixture(t *testing.T) (*Scheduler, Service) {
	t.Helper()
	svc, _ := newServiceFixture(t)
	s := NewScheduler(svc, DefaultSchedulerConfig())
	return s, svc
}

func TestScheduler_ClaimNextTask_DelegatesToService(t *testing.T) {
	s, svc := newSchedulerFixture(t)
	ctx := context.Background()

	snap, err := svc.CreateTeam(ctx, CreateTeamRequest{WorkspaceID: "ws", LeaderSessionID: "l", Name: "T"})
	require.NoError(t, err)
	m, err := svc.SpawnMember(ctx, SpawnMemberRequest{TeamID: snap.Team.ID, Name: "coder", Role: "programmer", AgentProfile: "{}"})
	require.NoError(t, err)
	_, err = svc.CreateTask(ctx, CreateTaskRequest{TeamID: snap.Team.ID, Title: "task", CreatedByMemberID: m.ID, Priority: 5})
	require.NoError(t, err)

	claimed, err := s.ClaimNextTask(ctx, ClaimNextTaskRequest{TeamID: snap.Team.ID, AssigneeMemberID: m.ID})
	require.NoError(t, err)
	assert.Equal(t, TaskAssigned, claimed.Status)
	assert.Equal(t, m.ID, *claimed.AssigneeMemberID)

	_, err = s.ClaimNextTask(ctx, ClaimNextTaskRequest{TeamID: snap.Team.ID, AssigneeMemberID: m.ID})
	require.ErrorIs(t, err, ErrNoTaskAvailable)
}

// TestScheduler_ClaimNextTask_ConcurrentSingleWinner is acceptance #1: 10
// goroutines race to claim ONE queued task → exactly 1 winner. Uses the same
// SetMaxOpenConns(1) pattern as M3-04 (Seam 8).
func TestScheduler_ClaimNextTask_ConcurrentSingleWinner(t *testing.T) {
	s, svc := newSchedulerFixture(t)
	ctx := context.Background()

	snap, err := svc.CreateTeam(ctx, CreateTeamRequest{WorkspaceID: "ws", LeaderSessionID: "l", Name: "T"})
	require.NoError(t, err)
	m, err := svc.SpawnMember(ctx, SpawnMemberRequest{TeamID: snap.Team.ID, Name: "coder", Role: "programmer", AgentProfile: "{}"})
	require.NoError(t, err)
	_, err = svc.CreateTask(ctx, CreateTaskRequest{TeamID: snap.Team.ID, Title: "only one", CreatedByMemberID: m.ID, Priority: 5})
	require.NoError(t, err)

	const N = 10
	var (
		wg      sync.WaitGroup
		mu      sync.Mutex
		wins    int
		noAvail int
	)
	for range N {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := s.ClaimNextTask(ctx, ClaimNextTaskRequest{TeamID: snap.Team.ID, AssigneeMemberID: m.ID})
			mu.Lock()
			defer mu.Unlock()
			if err == nil {
				wins++
			} else if errors.Is(err, ErrNoTaskAvailable) {
				noAvail++
			}
		}()
	}
	wg.Wait()

	assert.Equal(t, 1, wins, "exactly 1 goroutine wins the single task")
	assert.Equal(t, 9, noAvail, "the other 9 goroutines get ErrNoTaskAvailable")
}
