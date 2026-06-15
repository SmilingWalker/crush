package team

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/charmbracelet/crush/internal/agent"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestValidTransitions_CoversAllMemberStatuses(t *testing.T) {
	// Every defined MemberStatus must be a key in validTransitions.
	for _, s := range allMemberStatuses {
		_, ok := validTransitions[s]
		assert.Truef(t, ok, "MemberStatus %q missing from validTransitions", s)
	}
	// The map must have exactly 11 keys (one per MemberStatus).
	assert.Len(t, validTransitions, 11, "validTransitions must cover all 11 MemberStatus values")
}

func TestValidTransitions_Allowed(t *testing.T) {
	tests := []struct {
		from MemberStatus
		to   MemberStatus
		ok   bool
	}{
		// created
		{MemberCreated, MemberStarting, true},
		{MemberCreated, MemberStopped, true},
		{MemberCreated, MemberIdle, false},
		// starting
		{MemberStarting, MemberIdle, true},
		{MemberStarting, MemberFailed, true},
		{MemberStarting, MemberStopped, false},
		// idle — most transitions
		{MemberIdle, MemberQueued, true},
		{MemberIdle, MemberRunning, true},
		{MemberIdle, MemberBlocked, true},
		{MemberIdle, MemberShuttingDown, true},
		{MemberIdle, MemberStopped, true},
		{MemberIdle, MemberFailed, false},
		// queued
		{MemberQueued, MemberRunning, true},
		{MemberQueued, MemberIdle, true},
		{MemberQueued, MemberShuttingDown, true},
		{MemberQueued, MemberFailed, false},
		// running
		{MemberRunning, MemberIdle, true},
		{MemberRunning, MemberWaitingPermission, true},
		{MemberRunning, MemberBlocked, true},
		{MemberRunning, MemberCancelingTurn, true},
		{MemberRunning, MemberFailed, true},
		{MemberRunning, MemberShuttingDown, true},
		{MemberRunning, MemberStopped, false},
		// waiting_permission
		{MemberWaitingPermission, MemberIdle, true},
		{MemberWaitingPermission, MemberBlocked, true},
		{MemberWaitingPermission, MemberCancelingTurn, true},
		{MemberWaitingPermission, MemberShuttingDown, true},
		{MemberWaitingPermission, MemberRunning, false},
		// blocked
		{MemberBlocked, MemberIdle, true},
		{MemberBlocked, MemberShuttingDown, true},
		{MemberBlocked, MemberStopped, true},
		{MemberBlocked, MemberRunning, false},
		// canceling_turn
		{MemberCancelingTurn, MemberIdle, true},
		{MemberCancelingTurn, MemberBlocked, true},
		{MemberCancelingTurn, MemberShuttingDown, true},
		{MemberCancelingTurn, MemberFailed, true},
		{MemberCancelingTurn, MemberRunning, false},
		// shutting_down
		{MemberShuttingDown, MemberStopped, true},
		{MemberShuttingDown, MemberFailed, true},
		{MemberShuttingDown, MemberIdle, false},
		// stopped (terminal)
		{MemberStopped, MemberIdle, false},
		{MemberStopped, MemberStarting, false},
		// failed
		{MemberFailed, MemberStopped, true},
		{MemberFailed, MemberIdle, false},
		{MemberFailed, MemberStarting, false},
	}
	for _, tt := range tests {
		t.Run(string(tt.from)+"->"+string(tt.to), func(t *testing.T) {
			allowed := validTransitions[tt.from]
			found := false
			for _, a := range allowed {
				if a == tt.to {
					found = true
					break
				}
			}
			if tt.ok {
				assert.True(t, found, "transition %s->%s should be allowed", tt.from, tt.to)
			} else {
				assert.False(t, found, "transition %s->%s should NOT be allowed", tt.from, tt.to)
			}
		})
	}
}

func TestWakeSource_Consts(t *testing.T) {
	sources := []WakeSource{WakeSourceMailbox, WakeSourceTask, WakeSourceExplicit, WakeSourceRecovery, WakeSourceDependency}
	seen := map[WakeSource]bool{}
	for _, s := range sources {
		assert.False(t, seen[s], "duplicate WakeSource value %d", s)
		seen[s] = true
	}
	assert.Len(t, sources, 5)
}

// --- Task 3: transitionLocked, Wake, Stop ---

func TestMemberRunner_transitionLocked_ValidAndInvalid(t *testing.T) {
	m := &MemberRunner{ID: "m1", TeamID: "t1", State: MemberCreated, wakeCh: make(chan WakeSource, 1)}
	// Valid: created -> starting
	m.transitionLocked(MemberStarting)
	assert.Equal(t, MemberStarting, m.State)
	// Invalid: starting -> stopped (not allowed)
	m.transitionLocked(MemberStopped)
	assert.Equal(t, MemberStarting, m.State) // unchanged
}

func TestMemberRunner_Wake_Enqueues(t *testing.T) {
	m := &MemberRunner{ID: "m1", wakeCh: make(chan WakeSource, 1)}
	m.Wake(WakeSourceTask)
	select {
	case src := <-m.wakeCh:
		assert.Equal(t, WakeSourceTask, src)
	default:
		t.Fatal("expected wake source on channel")
	}
}

func TestMemberRunner_Wake_ChannelFullDrops(t *testing.T) {
	m := &MemberRunner{ID: "m1", wakeCh: make(chan WakeSource, 1)}
	m.Wake(WakeSourceTask)     // fills the buffer
	m.Wake(WakeSourceExplicit) // should drop (channel full, no panic)
	// Drain and verify only the first arrived.
	src := <-m.wakeCh
	assert.Equal(t, WakeSourceTask, src)
}

func TestMemberRunner_Stop(t *testing.T) {
	m := &MemberRunner{ID: "m1", State: MemberIdle, wakeCh: make(chan WakeSource, 1)}
	m.ctx, m.cancel = context.WithCancel(context.Background())
	m.Stop()
	// After Stop, context should be canceled and state -> stopped.
	select {
	case <-m.ctx.Done():
		// expected
	default:
		t.Fatal("context should be canceled after Stop")
	}
	assert.Equal(t, MemberStopped, m.State)
}

// --- Task 4: Lifecycle integration with real Service + mock TurnRunner ---

// recordingTurnRunner records Run calls for test assertions.
type recordingTurnRunner struct {
	runCalls  []agent.TeamAgentCall
	runResult agent.TurnRunResult
	runErr    error
	busy      bool
}

func (m *recordingTurnRunner) Run(ctx context.Context, call agent.TeamAgentCall) (agent.TurnRunResult, error) {
	m.runCalls = append(m.runCalls, call)
	return m.runResult, m.runErr
}

func (m *recordingTurnRunner) Cancel(sessionID string) {}

func (m *recordingTurnRunner) IsSessionBusy(sessionID string) bool { return m.busy }

// stubAgentFactory always returns the same mock TurnRunner.
type stubAgentFactory struct{ runner agent.TurnRunner }

func (f *stubAgentFactory) BuildRunner(ctx context.Context, spec agent.AgentSpec) (agent.TurnRunner, error) {
	return f.runner, nil
}

func TestMemberRunner_Start_IdleLoop_Wake_Run_Success(t *testing.T) {
	svc, _ := newServiceFixture(t)

	// Create a team first, then spawn a member.
	snap, err := svc.CreateTeam(context.Background(), CreateTeamRequest{
		WorkspaceID: "ws-1", LeaderSessionID: "lead-1", Name: "test-team",
	})
	require.NoError(t, err)

	member, err := svc.SpawnMember(context.Background(), SpawnMemberRequest{
		TeamID: snap.Team.ID, Name: "test-member", Role: "coder", AgentProfile: "{}",
	})
	require.NoError(t, err)

	mockRunner := &recordingTurnRunner{
		runResult: agent.TurnRunResult{Status: agent.TurnCompleted},
	}
	factory := &stubAgentFactory{runner: mockRunner}

	mr := NewMemberRunner(member.ID, member.TeamID, agent.AgentSpec{}, factory, svc)

	// Start should load DB state, enter idle, launch loop.
	err = mr.Start(context.Background())
	require.NoError(t, err)
	assert.Equal(t, MemberIdle, mr.State)

	// Wake the runner.
	mr.Wake(WakeSourceExplicit)

	// Wait for the turn to complete (loop processes wake, runs, returns to idle).
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		mr.mu.Lock()
		calls := len(mockRunner.runCalls)
		mr.mu.Unlock()
		if calls >= 1 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	// Give it a moment to finish the post-run transition.
	time.Sleep(100 * time.Millisecond)

	mr.mu.Lock()
	defer mr.mu.Unlock()
	assert.Equal(t, MemberIdle, mr.State, "should return to idle after turn")
	require.Len(t, mockRunner.runCalls, 1)
	assert.Equal(t, mr.sessionID, mockRunner.runCalls[0].SessionID)
	assert.NotEmpty(t, mockRunner.runCalls[0].PromptEnvelope, "prompt should not be empty (stub)")
}

func TestMemberRunner_handleWake_BusyPreservesWakeup(t *testing.T) {
	svc, _ := newServiceFixture(t)
	snap, err := svc.CreateTeam(context.Background(), CreateTeamRequest{
		WorkspaceID: "ws-2", LeaderSessionID: "lead-2", Name: "busy-team",
	})
	require.NoError(t, err)
	member, err := svc.SpawnMember(context.Background(), SpawnMemberRequest{
		TeamID: snap.Team.ID, Name: "busy-member", Role: "coder", AgentProfile: "{}",
	})
	require.NoError(t, err)

	mockRunner := &recordingTurnRunner{}
	factory := &stubAgentFactory{runner: mockRunner}
	mr := NewMemberRunner(member.ID, member.TeamID, agent.AgentSpec{}, factory, svc)
	err = mr.Start(context.Background())
	require.NoError(t, err)

	// Manually set state to running (simulating mid-turn).
	mr.mu.Lock()
	mr.State = MemberRunning
	mr.mu.Unlock()

	// Wake while running — should not enqueue a second turn.
	mr.Wake(WakeSourceTask)

	// After a short wait, verify no run calls were made.
	time.Sleep(200 * time.Millisecond)
	mr.mu.Lock()
	defer mr.mu.Unlock()
	assert.Equal(t, 0, len(mockRunner.runCalls), "no runs while busy")
}

func TestMemberRunner_handleWake_RunError(t *testing.T) {
	svc, _ := newServiceFixture(t)
	snap, err := svc.CreateTeam(context.Background(), CreateTeamRequest{
		WorkspaceID: "ws-3", LeaderSessionID: "lead-3", Name: "error-team",
	})
	require.NoError(t, err)
	member, err := svc.SpawnMember(context.Background(), SpawnMemberRequest{
		TeamID: snap.Team.ID, Name: "error-member", Role: "coder", AgentProfile: "{}",
	})
	require.NoError(t, err)

	mockRunner := &recordingTurnRunner{
		runResult: agent.TurnRunResult{Status: agent.TurnFailed},
		runErr:    errors.New("LLM error"),
	}
	factory := &stubAgentFactory{runner: mockRunner}
	mr := NewMemberRunner(member.ID, member.TeamID, agent.AgentSpec{}, factory, svc)
	err = mr.Start(context.Background())
	require.NoError(t, err)

	mr.Wake(WakeSourceTask)

	// Wait for the failed turn to complete.
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		mr.mu.Lock()
		calls := len(mockRunner.runCalls)
		mr.mu.Unlock()
		if calls >= 1 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	// Give it a moment to finish the post-run transition.
	time.Sleep(100 * time.Millisecond)
	mr.mu.Lock()
	defer mr.mu.Unlock()
	// Run error -> member should be in failed state.
	assert.Equal(t, MemberFailed, mr.State)
}

func TestMemberRunner_Start_AlreadyStartedReturnsError(t *testing.T) {
	svc, _ := newServiceFixture(t)
	snap, err := svc.CreateTeam(context.Background(), CreateTeamRequest{
		WorkspaceID: "ws-4", LeaderSessionID: "lead-4", Name: "start-team",
	})
	require.NoError(t, err)
	member, err := svc.SpawnMember(context.Background(), SpawnMemberRequest{
		TeamID: snap.Team.ID, Name: "double-start", Role: "coder", AgentProfile: "{}",
	})
	require.NoError(t, err)

	mr := NewMemberRunner(member.ID, member.TeamID, agent.AgentSpec{},
		&stubAgentFactory{runner: &recordingTurnRunner{}}, svc)
	err = mr.Start(context.Background())
	require.NoError(t, err)

	// Second Start should fail.
	err = mr.Start(context.Background())
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "already started")
}

func TestMemberRunner_actorCtx(t *testing.T) {
	mr := &MemberRunner{ID: "m1", TeamID: "t1", Role: "coder", Spec: agent.AgentSpec{}}
	ctx := mr.actorCtx()
	assert.Equal(t, "m1", ctx.MemberID)
	assert.Equal(t, "t1", ctx.TeamID)
	assert.Equal(t, "coder", ctx.MemberRole)
}

func TestMemberRunner_buildPrompt(t *testing.T) {
	mr := &MemberRunner{ID: "m1", Role: "coder"}
	prompt := mr.buildPrompt(WakeSourceExplicit)
	assert.Contains(t, prompt, "explicit")
	assert.Contains(t, prompt, "m1")
}
