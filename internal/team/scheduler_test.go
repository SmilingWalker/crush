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

func TestScheduler_StartHeartbeat_TickerAndCancel(t *testing.T) {
	_, svc := newSchedulerFixture(t)
	ctx := context.Background()

	snap, err := svc.CreateTeam(ctx, CreateTeamRequest{WorkspaceID: "ws", LeaderSessionID: "l", Name: "T"})
	require.NoError(t, err)
	m, err := svc.SpawnMember(ctx, SpawnMemberRequest{TeamID: snap.Team.ID, Name: "coder", Role: "programmer", AgentProfile: "{}"})
	require.NoError(t, err)
	run, err := svc.StartRun(ctx, StartRunRequest{TeamID: snap.Team.ID, MemberID: m.ID, SessionID: "sess-1"})
	require.NoError(t, err)

	// Fast ticker for testing.
	fastCfg := DefaultSchedulerConfig()
	fastCfg.HeartbeatInterval = 50 * time.Millisecond
	fastS := NewScheduler(svc, fastCfg)

	cancel, err := fastS.StartHeartbeat(ctx, run.ID)
	require.NoError(t, err)
	require.NotNil(t, cancel)

	// Wait for at least 2 ticks.
	time.Sleep(150 * time.Millisecond)
	cancel()
	time.Sleep(100 * time.Millisecond) // allow goroutine to exit

	// Verify heartbeat_at was updated (at least once after the initial value).
	debug, err := svc.DebugSnapshot(ctx, snap.Team.ID)
	require.NoError(t, err)
	for _, r := range debug.Runs {
		if r.ID == run.ID {
			require.NotNil(t, r.HeartbeatAt)
			assert.True(t, r.HeartbeatAt.After(*run.HeartbeatAt),
				"heartbeat should have been updated by ticker (after %v, got %v)", *run.HeartbeatAt, *r.HeartbeatAt)
			return
		}
	}
	t.Fatal("run not found in debug snapshot")
}

func TestScheduler_StartHeartbeat_CancelStopsTicker(t *testing.T) {
	_, svc := newSchedulerFixture(t)
	ctx := context.Background()

	snap, err := svc.CreateTeam(ctx, CreateTeamRequest{WorkspaceID: "ws", LeaderSessionID: "l", Name: "T"})
	require.NoError(t, err)
	m, err := svc.SpawnMember(ctx, SpawnMemberRequest{TeamID: snap.Team.ID, Name: "coder", Role: "programmer", AgentProfile: "{}"})
	require.NoError(t, err)
	run, err := svc.StartRun(ctx, StartRunRequest{TeamID: snap.Team.ID, MemberID: m.ID, SessionID: "sess-1"})
	require.NoError(t, err)

	fastCfg := DefaultSchedulerConfig()
	fastCfg.HeartbeatInterval = 50 * time.Millisecond
	fastS := NewScheduler(svc, fastCfg)

	cancel, err := fastS.StartHeartbeat(ctx, run.ID)
	require.NoError(t, err)

	// Cancel immediately — ticker should NOT fire.
	cancel()

	// Snapshot before sleep.
	debug, err := svc.DebugSnapshot(ctx, snap.Team.ID)
	require.NoError(t, err)
	var hbBefore *time.Time
	for _, r := range debug.Runs {
		if r.ID == run.ID {
			hbBefore = r.HeartbeatAt
			break
		}
	}
	require.NotNil(t, hbBefore)

	// Wait long enough for a tick if it were still running.
	time.Sleep(150 * time.Millisecond)

	// Re-read: heartbeat should NOT have changed.
	debug2, err := svc.DebugSnapshot(ctx, snap.Team.ID)
	require.NoError(t, err)
	for _, r := range debug2.Runs {
		if r.ID == run.ID {
			require.NotNil(t, r.HeartbeatAt)
			assert.True(t, r.HeartbeatAt.Equal(*hbBefore) || !r.HeartbeatAt.After(hbBefore.Add(100*time.Millisecond)),
				"heartbeat should not have advanced after cancel")
			return
		}
	}
}

func TestScheduler_StartHeartbeat_ParentCtxCancelStopsGoroutine(t *testing.T) {
	_, svc := newSchedulerFixture(t)
	ctx := context.Background()

	snap, err := svc.CreateTeam(ctx, CreateTeamRequest{WorkspaceID: "ws", LeaderSessionID: "l", Name: "T"})
	require.NoError(t, err)
	m, err := svc.SpawnMember(ctx, SpawnMemberRequest{TeamID: snap.Team.ID, Name: "coder", Role: "programmer", AgentProfile: "{}"})
	require.NoError(t, err)
	run, err := svc.StartRun(ctx, StartRunRequest{TeamID: snap.Team.ID, MemberID: m.ID, SessionID: "sess-1"})
	require.NoError(t, err)

	fastCfg := DefaultSchedulerConfig()
	fastCfg.HeartbeatInterval = 50 * time.Millisecond
	fastS := NewScheduler(svc, fastCfg)

	parentCtx, parentCancel := context.WithCancel(ctx)
	cancel, err := fastS.StartHeartbeat(parentCtx, run.ID)
	require.NoError(t, err)
	_ = cancel

	// Cancel parent → goroutine should stop (no panic/leak).
	parentCancel()
	time.Sleep(100 * time.Millisecond)
	cancel() // should not panic on already-cancelled context
}
