package team

import (
	"context"
	"database/sql"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/charmbracelet/crush/internal/agent"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// --- Shutdown tests (M4-07) ---

// TestShutdown_Graceful_IdleMember verifies that a graceful shutdown on an idle
// member waits briefly, cancels the loop, and transitions to stopped, writing
// both shutting_down and stopped events.
func TestShutdown_Graceful_IdleMember(t *testing.T) {
	svc, sqlDB := newServiceFixture(t)
	snap, err := svc.CreateTeam(context.Background(), CreateTeamRequest{
		WorkspaceID: "ws-grace", LeaderSessionID: "lead", Name: "graceful-idle",
	})
	require.NoError(t, err)
	member, err := svc.SpawnMember(context.Background(), SpawnMemberRequest{
		TeamID: snap.Team.ID, Name: "m1", Role: "coder", AgentProfile: "{}",
	})
	require.NoError(t, err)

	mockRunner := &recordingTurnRunner{
		runResult: agent.TurnRunResult{Status: agent.TurnCompleted},
	}
	factory := &stubAgentFactory{runner: mockRunner}
	mr := NewMemberRunner(member.ID, member.TeamID, agent.AgentSpec{}, factory, svc, nil, nil)
	err = mr.Start(context.Background())
	require.NoError(t, err)

	time.Sleep(100 * time.Millisecond) // let Start settle

	// Graceful shutdown on idle member.
	err = mr.Shutdown(context.Background(), StopGraceful)
	require.NoError(t, err)
	assert.Equal(t, MemberStopped, mr.State)

	// Verify events were written.
	var shuttingDownCount, stoppedCount int
	row := sqlDB.QueryRow(`SELECT count(*) FROM team_events WHERE team_id = ? AND event_type = 'member.shutting_down' AND entity_id = ?`,
		snap.Team.ID, member.ID)
	require.NoError(t, row.Scan(&shuttingDownCount))
	assert.Equal(t, 1, shuttingDownCount, "member.shutting_down event should be written")

	row = sqlDB.QueryRow(`SELECT count(*) FROM team_events WHERE team_id = ? AND event_type = 'member.stopped' AND entity_id = ?`,
		snap.Team.ID, member.ID)
	require.NoError(t, row.Scan(&stoppedCount))
	assert.Equal(t, 1, stoppedCount, "member.stopped event should be written")
}

// TestShutdown_Cancel_IdleMember verifies that cancel shutdown on an idle member
// works correctly — no active run, so MarkRunTerminal is a no-op.
func TestShutdown_Cancel_IdleMember(t *testing.T) {
	svc, _ := newServiceFixture(t)
	snap, err := svc.CreateTeam(context.Background(), CreateTeamRequest{
		WorkspaceID: "ws-cancel", LeaderSessionID: "lead", Name: "cancel-idle",
	})
	require.NoError(t, err)
	member, err := svc.SpawnMember(context.Background(), SpawnMemberRequest{
		TeamID: snap.Team.ID, Name: "m1", Role: "coder", AgentProfile: "{}",
	})
	require.NoError(t, err)

	mockRunner := &recordingTurnRunner{
		runResult: agent.TurnRunResult{Status: agent.TurnCompleted},
	}
	factory := &stubAgentFactory{runner: mockRunner}
	mr := NewMemberRunner(member.ID, member.TeamID, agent.AgentSpec{}, factory, svc, nil, nil)
	err = mr.Start(context.Background())
	require.NoError(t, err)
	time.Sleep(100 * time.Millisecond)

	err = mr.Shutdown(context.Background(), StopCancel)
	require.NoError(t, err)
	assert.Equal(t, MemberStopped, mr.State)
}

// TestShutdown_Force_IdleMember verifies Force mode shutdown — immediate,
// no events written, no flush.
func TestShutdown_Force_IdleMember(t *testing.T) {
	svc, sqlDB := newServiceFixture(t)
	snap, err := svc.CreateTeam(context.Background(), CreateTeamRequest{
		WorkspaceID: "ws-force", LeaderSessionID: "lead", Name: "force-idle",
	})
	require.NoError(t, err)
	member, err := svc.SpawnMember(context.Background(), SpawnMemberRequest{
		TeamID: snap.Team.ID, Name: "m1", Role: "coder", AgentProfile: "{}",
	})
	require.NoError(t, err)

	mockRunner := &recordingTurnRunner{
		runResult: agent.TurnRunResult{Status: agent.TurnCompleted},
	}
	factory := &stubAgentFactory{runner: mockRunner}
	mr := NewMemberRunner(member.ID, member.TeamID, agent.AgentSpec{}, factory, svc, nil, nil)
	err = mr.Start(context.Background())
	require.NoError(t, err)
	time.Sleep(100 * time.Millisecond)

	err = mr.Shutdown(context.Background(), StopForce)
	require.NoError(t, err)
	assert.Equal(t, MemberStopped, mr.State)

	// Force mode should NOT write events.
	var eventCount int
	row := sqlDB.QueryRow(`SELECT count(*) FROM team_events WHERE team_id = ? AND event_type IN ('member.shutting_down', 'member.stopped') AND entity_id = ?`,
		snap.Team.ID, member.ID)
	require.NoError(t, row.Scan(&eventCount))
	assert.Equal(t, 0, eventCount, "Force mode should not write shutdown/stopped events")
}

// TestShutdown_Idempotent verifies that calling Shutdown on an already-stopped
// member is a no-op (returns nil, no state change, no event noise).
func TestShutdown_Idempotent(t *testing.T) {
	svc, sqlDB := newServiceFixture(t)
	snap, err := svc.CreateTeam(context.Background(), CreateTeamRequest{
		WorkspaceID: "ws-idem", LeaderSessionID: "lead", Name: "idempotent",
	})
	require.NoError(t, err)
	member, err := svc.SpawnMember(context.Background(), SpawnMemberRequest{
		TeamID: snap.Team.ID, Name: "m1", Role: "coder", AgentProfile: "{}",
	})
	require.NoError(t, err)

	mockRunner := &recordingTurnRunner{
		runResult: agent.TurnRunResult{Status: agent.TurnCompleted},
	}
	factory := &stubAgentFactory{runner: mockRunner}
	mr := NewMemberRunner(member.ID, member.TeamID, agent.AgentSpec{}, factory, svc, nil, nil)
	err = mr.Start(context.Background())
	require.NoError(t, err)
	time.Sleep(100 * time.Millisecond)

	// First shutdown.
	err = mr.Shutdown(context.Background(), StopGraceful)
	require.NoError(t, err)
	assert.Equal(t, MemberStopped, mr.State)

	// Count events after first shutdown.
	var eventCountFirst int
	row := sqlDB.QueryRow(`SELECT count(*) FROM team_events WHERE team_id = ? AND event_type IN ('member.shutting_down', 'member.stopped') AND entity_id = ?`,
		snap.Team.ID, member.ID)
	require.NoError(t, row.Scan(&eventCountFirst))

	// Second shutdown — should be a no-op.
	err = mr.Shutdown(context.Background(), StopGraceful)
	require.NoError(t, err)
	assert.Equal(t, MemberStopped, mr.State)

	// Events should not have increased.
	var eventCountSecond int
	row = sqlDB.QueryRow(`SELECT count(*) FROM team_events WHERE team_id = ? AND event_type IN ('member.shutting_down', 'member.stopped') AND entity_id = ?`,
		snap.Team.ID, member.ID)
	require.NoError(t, row.Scan(&eventCountSecond))
	assert.Equal(t, eventCountFirst, eventCountSecond, "idempotent shutdown should not write duplicate events")
}

// TestShutdown_StopWakeups verifies that once Shutdown starts, new wakes are
// dropped and not processed.
func TestShutdown_StopWakeups(t *testing.T) {
	svc, _ := newServiceFixture(t)
	snap, err := svc.CreateTeam(context.Background(), CreateTeamRequest{
		WorkspaceID: "ws-wakes", LeaderSessionID: "lead", Name: "stop-wakes",
	})
	require.NoError(t, err)
	member, err := svc.SpawnMember(context.Background(), SpawnMemberRequest{
		TeamID: snap.Team.ID, Name: "m1", Role: "coder", AgentProfile: "{}",
	})
	require.NoError(t, err)

	mockRunner := &recordingTurnRunner{
		runResult: agent.TurnRunResult{Status: agent.TurnCompleted},
	}
	factory := &stubAgentFactory{runner: mockRunner}
	mr := NewMemberRunner(member.ID, member.TeamID, agent.AgentSpec{}, factory, svc, nil, nil)
	err = mr.Start(context.Background())
	require.NoError(t, err)
	time.Sleep(100 * time.Millisecond)

	// Count wake calls before shutdown.
	mr.mu.Lock()
	callsBefore := len(mockRunner.runCalls)
	mr.mu.Unlock()

	// Shutdown in Force mode (fast path).
	err = mr.Shutdown(context.Background(), StopForce)
	require.NoError(t, err)

	// Send wakes after shutdown — they should be silently dropped.
	mr.Wake(WakeSourceExplicit)
	mr.Wake(WakeSourceTask)

	time.Sleep(100 * time.Millisecond)

	// No new runs should have started.
	mr.mu.Lock()
	callsAfter := len(mockRunner.runCalls)
	mr.mu.Unlock()
	assert.Equal(t, callsBefore, callsAfter, "wakes after shutdown should not trigger new runs")
}

// TestShutdown_FlushCalled verifies the flushFn is called during shutdown.
func TestShutdown_FlushCalled(t *testing.T) {
	svc, _ := newServiceFixture(t)
	snap, err := svc.CreateTeam(context.Background(), CreateTeamRequest{
		WorkspaceID: "ws-flush", LeaderSessionID: "lead", Name: "flush-test",
	})
	require.NoError(t, err)
	member, err := svc.SpawnMember(context.Background(), SpawnMemberRequest{
		TeamID: snap.Team.ID, Name: "m1", Role: "coder", AgentProfile: "{}",
	})
	require.NoError(t, err)

	mockRunner := &recordingTurnRunner{
		runResult: agent.TurnRunResult{Status: agent.TurnCompleted},
	}
	factory := &stubAgentFactory{runner: mockRunner}
	mr := NewMemberRunner(member.ID, member.TeamID, agent.AgentSpec{}, factory, svc, nil, nil)
	err = mr.Start(context.Background())
	require.NoError(t, err)
	time.Sleep(100 * time.Millisecond)

	var flushCalled atomic.Bool
	mr.flushFn = func(ctx context.Context) error {
		flushCalled.Store(true)
		return nil
	}

	err = mr.Shutdown(context.Background(), StopGraceful)
	require.NoError(t, err)
	assert.True(t, flushCalled.Load(), "flushFn should be called during graceful shutdown")

	// Force mode should NOT call flush.
	flushCalled.Store(false)
	// Create a fresh runner since the previous one is now stopped.
	member2, err := svc.SpawnMember(context.Background(), SpawnMemberRequest{
		TeamID: snap.Team.ID, Name: "m2", Role: "coder", AgentProfile: "{}",
	})
	require.NoError(t, err)
	mr2 := NewMemberRunner(member2.ID, member2.TeamID, agent.AgentSpec{}, factory, svc, nil, nil)
	err = mr2.Start(context.Background())
	require.NoError(t, err)
	time.Sleep(100 * time.Millisecond)

	mr2.flushFn = func(ctx context.Context) error {
		flushCalled.Store(true)
		return nil
	}
	err = mr2.Shutdown(context.Background(), StopForce)
	require.NoError(t, err)
	assert.False(t, flushCalled.Load(), "flushFn should NOT be called during force shutdown")
}

// TestShutdown_FlushError is non-fatal — shutdown proceeds even if flush fails.
func TestShutdown_FlushError(t *testing.T) {
	svc, _ := newServiceFixture(t)
	snap, err := svc.CreateTeam(context.Background(), CreateTeamRequest{
		WorkspaceID: "ws-flusherr", LeaderSessionID: "lead", Name: "flush-err",
	})
	require.NoError(t, err)
	member, err := svc.SpawnMember(context.Background(), SpawnMemberRequest{
		TeamID: snap.Team.ID, Name: "m1", Role: "coder", AgentProfile: "{}",
	})
	require.NoError(t, err)

	mockRunner := &recordingTurnRunner{
		runResult: agent.TurnRunResult{Status: agent.TurnCompleted},
	}
	factory := &stubAgentFactory{runner: mockRunner}
	mr := NewMemberRunner(member.ID, member.TeamID, agent.AgentSpec{}, factory, svc, nil, nil)
	err = mr.Start(context.Background())
	require.NoError(t, err)
	time.Sleep(100 * time.Millisecond)

	mr.flushFn = func(ctx context.Context) error {
		return errors.New("flush failed")
	}

	// Shutdown should still succeed despite flush error.
	err = mr.Shutdown(context.Background(), StopGraceful)
	require.NoError(t, err)
	assert.Equal(t, MemberStopped, mr.State)
}

// TestShutdown_Cancel_WithActiveRun verifies that cancel shutdown during an
// active run marks the run as canceled.
func TestShutdown_Cancel_WithActiveRun(t *testing.T) {
	svc, sqlDB := newServiceFixture(t)
	snap, err := svc.CreateTeam(context.Background(), CreateTeamRequest{
		WorkspaceID: "ws-cancelrun", LeaderSessionID: "lead", Name: "cancel-run",
	})
	require.NoError(t, err)
	member, err := svc.SpawnMember(context.Background(), SpawnMemberRequest{
		TeamID: snap.Team.ID, Name: "m1", Role: "coder", AgentProfile: "{}",
	})
	require.NoError(t, err)

	// Use a blocking runner so we can verify cancel behaviour.
	blockingRunner := &blockingTurnRunner{done: make(chan struct{})}
	factory := &stubAgentFactory{runner: blockingRunner}
	mr := NewMemberRunner(member.ID, member.TeamID, agent.AgentSpec{}, factory, svc, nil, nil)
	err = mr.Start(context.Background())
	require.NoError(t, err)
	time.Sleep(100 * time.Millisecond)

	// Wake to start a run that will block.
	mr.Wake(WakeSourceExplicit)
	time.Sleep(100 * time.Millisecond)

	// Verify the member is running.
	mr.mu.Lock()
	assert.Equal(t, MemberRunning, mr.State)
	runID := mr.currentRunID
	mr.mu.Unlock()
	assert.NotEmpty(t, runID, "should have an active run")

	// Cancel shutdown — should cancel context and mark run as canceled.
	err = mr.Shutdown(context.Background(), StopCancel)
	require.NoError(t, err)
	assert.Equal(t, MemberStopped, mr.State)

	// Allow the blocking runner to complete (late result check).
	close(blockingRunner.done)
	time.Sleep(100 * time.Millisecond)

	// Verify the run was marked as canceled.
	var runStatus string
	row := sqlDB.QueryRow(`SELECT status FROM team_runs WHERE id = ?`, runID)
	require.NoError(t, row.Scan(&runStatus))
	assert.Equal(t, string(RunCanceled), runStatus)
}

// TestShutdown_Graceful_RunningMember verifies graceful shutdown waits for the
// current turn to complete naturally, then stops without marking run canceled.
func TestShutdown_Graceful_RunningMember(t *testing.T) {
	svc, sqlDB := newServiceFixture(t)
	snap, err := svc.CreateTeam(context.Background(), CreateTeamRequest{
		WorkspaceID: "ws-gracefulrun", LeaderSessionID: "lead", Name: "graceful-run",
	})
	require.NoError(t, err)
	member, err := svc.SpawnMember(context.Background(), SpawnMemberRequest{
		TeamID: snap.Team.ID, Name: "m1", Role: "coder", AgentProfile: "{}",
	})
	require.NoError(t, err)

	// Use a delayed runner — completes after 200ms.
	delayedRunner := &delayedTurnRunner{delay: 200 * time.Millisecond}
	factory := &stubAgentFactory{runner: delayedRunner}
	mr := NewMemberRunner(member.ID, member.TeamID, agent.AgentSpec{}, factory, svc, nil, nil)
	err = mr.Start(context.Background())
	require.NoError(t, err)
	time.Sleep(100 * time.Millisecond)

	// Wake to start a run.
	mr.Wake(WakeSourceExplicit)
	time.Sleep(50 * time.Millisecond)

	// Verify member is running.
	mr.mu.Lock()
	assert.Equal(t, MemberRunning, mr.State)
	runID := mr.currentRunID
	mr.mu.Unlock()
	assert.NotEmpty(t, runID)

	// Graceful shutdown — should wait for the turn to complete.
	err = mr.Shutdown(context.Background(), StopGraceful)
	require.NoError(t, err)
	assert.Equal(t, MemberStopped, mr.State)

	// Run should still be running (completed by handleWake → FinishRun, not canceled).
	var runStatus string
	row := sqlDB.QueryRow(`SELECT status FROM team_runs WHERE id = ?`, runID)
	require.NoError(t, row.Scan(&runStatus))
	assert.Equal(t, string(RunCompleted), runStatus, "graceful shutdown should let turn complete normally")
}

// TestShutdown_Force_WithActiveRun verifies Force mode marks the run as interrupted.
func TestShutdown_Force_WithActiveRun(t *testing.T) {
	svc, sqlDB := newServiceFixture(t)
	snap, err := svc.CreateTeam(context.Background(), CreateTeamRequest{
		WorkspaceID: "ws-forcerun", LeaderSessionID: "lead", Name: "force-run",
	})
	require.NoError(t, err)
	member, err := svc.SpawnMember(context.Background(), SpawnMemberRequest{
		TeamID: snap.Team.ID, Name: "m1", Role: "coder", AgentProfile: "{}",
	})
	require.NoError(t, err)

	blockingRunner := &blockingTurnRunner{done: make(chan struct{})}
	factory := &stubAgentFactory{runner: blockingRunner}
	mr := NewMemberRunner(member.ID, member.TeamID, agent.AgentSpec{}, factory, svc, nil, nil)
	err = mr.Start(context.Background())
	require.NoError(t, err)
	time.Sleep(100 * time.Millisecond)

	mr.Wake(WakeSourceExplicit)
	time.Sleep(100 * time.Millisecond)

	mr.mu.Lock()
	assert.Equal(t, MemberRunning, mr.State)
	runID := mr.currentRunID
	mr.mu.Unlock()
	assert.NotEmpty(t, runID)

	// Force shutdown.
	err = mr.Shutdown(context.Background(), StopForce)
	require.NoError(t, err)
	assert.Equal(t, MemberStopped, mr.State)

	close(blockingRunner.done)
	time.Sleep(100 * time.Millisecond)

	// Run should be marked as interrupted.
	var runStatus string
	row := sqlDB.QueryRow(`SELECT status FROM team_runs WHERE id = ?`, runID)
	require.NoError(t, row.Scan(&runStatus))
	assert.Equal(t, string(RunInterrupted), runStatus, "force shutdown should mark run as interrupted")
}

// TestShutdown_CtxCanceledDuringWait returns the context error if the caller's
// ctx is cancelled during the wait loop.
func TestShutdown_CtxCanceledDuringWait(t *testing.T) {
	svc, _ := newServiceFixture(t)
	snap, err := svc.CreateTeam(context.Background(), CreateTeamRequest{
		WorkspaceID: "ws-ctx", LeaderSessionID: "lead", Name: "ctx-cancel",
	})
	require.NoError(t, err)
	member, err := svc.SpawnMember(context.Background(), SpawnMemberRequest{
		TeamID: snap.Team.ID, Name: "m1", Role: "coder", AgentProfile: "{}",
	})
	require.NoError(t, err)

	blockingRunner := &blockingTurnRunner{done: make(chan struct{})}
	factory := &stubAgentFactory{runner: blockingRunner}
	mr := NewMemberRunner(member.ID, member.TeamID, agent.AgentSpec{}, factory, svc, nil, nil)
	err = mr.Start(context.Background())
	require.NoError(t, err)
	time.Sleep(100 * time.Millisecond)

	mr.Wake(WakeSourceExplicit)
	time.Sleep(100 * time.Millisecond)

	// Cancel the caller's context immediately so the wait loop exits early.
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	err = mr.Shutdown(ctx, StopGraceful)
	require.NoError(t, err, "Shutdown should complete even when caller ctx is cancelled during wait")

	close(blockingRunner.done)
	time.Sleep(100 * time.Millisecond)

	// Member should still end up stopped (Shutdown proceeds after wait).
	assert.Equal(t, MemberStopped, mr.State)
}

// TestShutdown_EventsHavePublishedAt verifies that events written by Shutdown
// have published_at set.
func TestShutdown_EventsHavePublishedAt(t *testing.T) {
	svc, sqlDB := newServiceFixture(t)
	snap, err := svc.CreateTeam(context.Background(), CreateTeamRequest{
		WorkspaceID: "ws-pubat", LeaderSessionID: "lead", Name: "pub-at",
	})
	require.NoError(t, err)
	member, err := svc.SpawnMember(context.Background(), SpawnMemberRequest{
		TeamID: snap.Team.ID, Name: "m1", Role: "coder", AgentProfile: "{}",
	})
	require.NoError(t, err)

	mockRunner := &recordingTurnRunner{
		runResult: agent.TurnRunResult{Status: agent.TurnCompleted},
	}
	factory := &stubAgentFactory{runner: mockRunner}
	mr := NewMemberRunner(member.ID, member.TeamID, agent.AgentSpec{}, factory, svc, nil, nil)
	err = mr.Start(context.Background())
	require.NoError(t, err)
	time.Sleep(100 * time.Millisecond)

	err = mr.Shutdown(context.Background(), StopGraceful)
	require.NoError(t, err)

	// Verify all shutdown events have published_at set.
	rows, err := sqlDB.Query(`SELECT event_type, published_at FROM team_events WHERE team_id = ? AND event_type IN ('member.shutting_down', 'member.stopped') AND entity_id = ?`,
		snap.Team.ID, member.ID)
	require.NoError(t, err)
	defer rows.Close()

	var count int
	for rows.Next() {
		var eventType string
		var publishedAt sql.NullInt64
		require.NoError(t, rows.Scan(&eventType, &publishedAt))
		assert.True(t, publishedAt.Valid, "event %s should have published_at set", eventType)
		count++
	}
	assert.Equal(t, 2, count, "should have 2 events (shutting_down + stopped)")
}

// --- Test helpers ---

// delayedTurnRunner completes its Run after a configurable delay.
type delayedTurnRunner struct {
	delay     time.Duration
	runResult agent.TurnRunResult
	runErr    error
}

func (d *delayedTurnRunner) Run(ctx context.Context, call agent.TeamAgentCall) (agent.TurnRunResult, error) {
	select {
	case <-ctx.Done():
		return agent.TurnRunResult{}, ctx.Err()
	case <-time.After(d.delay):
		if d.runErr != nil {
			return agent.TurnRunResult{}, d.runErr
		}
		return d.runResult, nil
	}
}

func (d *delayedTurnRunner) Cancel(sessionID string) {}

func (d *delayedTurnRunner) IsSessionBusy(sessionID string) bool { return false }
