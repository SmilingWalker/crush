package team

import (
	"context"
	"runtime"
	"testing"
	"time"

	"github.com/charmbracelet/crush/internal/agent"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestStopMode_Consts(t *testing.T) {
	assert.Equal(t, StopMode(0), StopGraceful)
	assert.Equal(t, StopMode(1), StopCancel)
	assert.Equal(t, StopMode(2), StopForce)
	modes := []StopMode{StopGraceful, StopCancel, StopForce}
	seen := map[StopMode]bool{}
	for _, m := range modes {
		assert.False(t, seen[m], "duplicate StopMode %d", m)
		seen[m] = true
	}
	assert.Len(t, modes, 3)
}

func TestTeamRuntimeStatus_ZeroValue(t *testing.T) {
	status := TeamRuntimeStatus{}
	assert.Empty(t, status.Members)
	assert.Equal(t, 0, status.ActiveRuns)
}

func TestCancelMemberTurnRequest_Fields(t *testing.T) {
	req := CancelMemberTurnRequest{
		TeamID:      "t1",
		MemberID:    "m1",
		RequestedBy: "leader",
		Reason:      "test",
	}
	assert.Equal(t, "t1", req.TeamID)
	assert.Equal(t, "m1", req.MemberID)
	assert.Equal(t, "leader", req.RequestedBy)
	assert.Equal(t, "test", req.Reason)
}

func TestTeamRunner_InterfaceCompliance(t *testing.T) {
	// Compile-time check: *teamRunner satisfies TeamRunner
	var _ TeamRunner = (*teamRunner)(nil)
}

func TestNewTeamRunner(t *testing.T) {
	svc, _ := newServiceFixture(t)
	factory := &stubAgentFactory{}
	tr := NewTeamRunner(svc, factory)
	require.NotNil(t, tr)
}

// --- Task 2: SpawnMember ---

func TestTeamRunner_SpawnMember_E2E(t *testing.T) {
	svc, _ := newServiceFixture(t)
	snap, err := svc.CreateTeam(context.Background(), CreateTeamRequest{
		WorkspaceID: "ws-1", LeaderSessionID: "lead-1", Name: "spawn-test",
	})
	require.NoError(t, err)

	mockRunner := &recordingTurnRunner{
		runResult: agent.TurnRunResult{Status: agent.TurnCompleted},
	}
	factory := &stubAgentFactory{runner: mockRunner}
	tr := NewTeamRunner(svc, factory)

	member, err := tr.SpawnMember(context.Background(), snap.Team.ID, "test-member", "coder", "{}", agent.AgentSpec{})
	require.NoError(t, err)
	assert.NotEmpty(t, member.ID)
	assert.Equal(t, "test-member", member.Name)
	assert.Equal(t, "coder", member.Role)

	// Wait for the background goroutine to start and settle.
	time.Sleep(200 * time.Millisecond)

	// Verify member is registered and status reflects idle after start.
	status, err := tr.Status(context.Background(), snap.Team.ID)
	require.NoError(t, err)
	ms, ok := status.Members[member.ID]
	require.True(t, ok, "member %s should be registered", member.ID)
	assert.Equal(t, MemberIdle, ms.State)
}

// --- Task 3: StartTeam / StopMember / StopTeam ---

func TestTeamRunner_StopMember_Graceful(t *testing.T) {
	svc, _ := newServiceFixture(t)
	snap, err := svc.CreateTeam(context.Background(), CreateTeamRequest{
		WorkspaceID: "ws-stopm", LeaderSessionID: "lead-stopm", Name: "stop-member-test",
	})
	require.NoError(t, err)

	mockRunner := &recordingTurnRunner{
		runResult: agent.TurnRunResult{Status: agent.TurnCompleted},
	}
	factory := &stubAgentFactory{runner: mockRunner}
	tr := NewTeamRunner(svc, factory)

	member, err := tr.SpawnMember(context.Background(), snap.Team.ID, "m1", "coder", "{}", agent.AgentSpec{})
	require.NoError(t, err)

	time.Sleep(100 * time.Millisecond)

	// Stop the member gracefully.
	err = tr.StopMember(context.Background(), snap.Team.ID, member.ID, StopGraceful)
	require.NoError(t, err)

	// Verify member is stopped.
	status, err := tr.Status(context.Background(), snap.Team.ID)
	require.NoError(t, err)
	ms, ok := status.Members[member.ID]
	require.True(t, ok)
	assert.Equal(t, MemberStopped, ms.State)
}

func TestTeamRunner_StopMember_Cancel(t *testing.T) {
	svc, _ := newServiceFixture(t)
	snap, err := svc.CreateTeam(context.Background(), CreateTeamRequest{
		WorkspaceID: "ws-stopmc", LeaderSessionID: "lead-stopmc", Name: "stop-cancel-test",
	})
	require.NoError(t, err)

	mockRunner := &recordingTurnRunner{
		runResult: agent.TurnRunResult{Status: agent.TurnCompleted},
	}
	factory := &stubAgentFactory{runner: mockRunner}
	tr := NewTeamRunner(svc, factory)

	member, err := tr.SpawnMember(context.Background(), snap.Team.ID, "m1", "coder", "{}", agent.AgentSpec{})
	require.NoError(t, err)

	time.Sleep(100 * time.Millisecond)

	err = tr.StopMember(context.Background(), snap.Team.ID, member.ID, StopCancel)
	require.NoError(t, err)

	status, err := tr.Status(context.Background(), snap.Team.ID)
	require.NoError(t, err)
	ms := status.Members[member.ID]
	assert.Equal(t, MemberStopped, ms.State)
}

func TestTeamRunner_StartTeam_LoadsExistingMembers(t *testing.T) {
	svc, _ := newServiceFixture(t)
	snap, err := svc.CreateTeam(context.Background(), CreateTeamRequest{
		WorkspaceID: "ws-start", LeaderSessionID: "lead-start", Name: "start-team-test",
	})
	require.NoError(t, err)

	// Pre-create members in DB (simulates existing team members from a prior session).
	m1, err := svc.SpawnMember(context.Background(), SpawnMemberRequest{
		TeamID: snap.Team.ID, Name: "m1", Role: "coder", AgentProfile: "{}",
	})
	require.NoError(t, err)
	m2, err := svc.SpawnMember(context.Background(), SpawnMemberRequest{
		TeamID: snap.Team.ID, Name: "m2", Role: "reviewer", AgentProfile: "{}",
	})
	require.NoError(t, err)

	mockRunner := &recordingTurnRunner{
		runResult: agent.TurnRunResult{Status: agent.TurnCompleted},
	}
	factory := &stubAgentFactory{runner: mockRunner}
	tr := NewTeamRunner(svc, factory)

	err = tr.StartTeam(context.Background(), snap.Team.ID)
	require.NoError(t, err)

	time.Sleep(200 * time.Millisecond)

	status, err := tr.Status(context.Background(), snap.Team.ID)
	require.NoError(t, err)
	assert.Len(t, status.Members, 2)
	assert.Contains(t, status.Members, m1.ID)
	assert.Contains(t, status.Members, m2.ID)
}

func TestTeamRunner_StopTeam_AllMembersStopped(t *testing.T) {
	svc, _ := newServiceFixture(t)
	snap, err := svc.CreateTeam(context.Background(), CreateTeamRequest{
		WorkspaceID: "ws-stopteam", LeaderSessionID: "lead-stopteam", Name: "stopteam-test",
	})
	require.NoError(t, err)

	mockRunner := &recordingTurnRunner{
		runResult: agent.TurnRunResult{Status: agent.TurnCompleted},
	}
	factory := &stubAgentFactory{runner: mockRunner}
	tr := NewTeamRunner(svc, factory)

	_, err = tr.SpawnMember(context.Background(), snap.Team.ID, "m1", "coder", "{}", agent.AgentSpec{})
	require.NoError(t, err)
	_, err = tr.SpawnMember(context.Background(), snap.Team.ID, "m2", "reviewer", "{}", agent.AgentSpec{})
	require.NoError(t, err)

	time.Sleep(100 * time.Millisecond)

	// Stop the whole team.
	err = tr.StopTeam(context.Background(), snap.Team.ID, StopCancel)
	require.NoError(t, err)

	status, err := tr.Status(context.Background(), snap.Team.ID)
	require.NoError(t, err)
	for id, ms := range status.Members {
		assert.True(t, ms.State == MemberStopped || ms.State == MemberFailed,
			"member %s should be terminal, got %s", id, ms.State)
	}
}

// --- Task 4: CancelMemberTurn / Status ---

func TestTeamRunner_CancelMemberTurn_Idempotent(t *testing.T) {
	svc, _ := newServiceFixture(t)
	snap, err := svc.CreateTeam(context.Background(), CreateTeamRequest{
		WorkspaceID: "ws-cancel", LeaderSessionID: "lead-cancel", Name: "cancel-test",
	})
	require.NoError(t, err)

	mockRunner := &recordingTurnRunner{
		runResult: agent.TurnRunResult{Status: agent.TurnCompleted},
	}
	factory := &stubAgentFactory{runner: mockRunner}
	tr := NewTeamRunner(svc, factory)

	member, err := tr.SpawnMember(context.Background(), snap.Team.ID, "m1", "coder", "{}", agent.AgentSpec{})
	require.NoError(t, err)

	time.Sleep(100 * time.Millisecond)

	req := CancelMemberTurnRequest{
		TeamID:      snap.Team.ID,
		MemberID:    member.ID,
		RequestedBy: "leader",
		Reason:      "test cancel",
	}

	// First cancel should succeed.
	err = tr.CancelMemberTurn(context.Background(), req)
	require.NoError(t, err)

	// Second cancel should be idempotent (no error, no panic).
	err = tr.CancelMemberTurn(context.Background(), req)
	require.NoError(t, err)
}

func TestTeamRunner_Status_ReflectsMemberState(t *testing.T) {
	svc, _ := newServiceFixture(t)
	snap, err := svc.CreateTeam(context.Background(), CreateTeamRequest{
		WorkspaceID: "ws-status", LeaderSessionID: "lead-status", Name: "status-test",
	})
	require.NoError(t, err)

	mockRunner := &recordingTurnRunner{
		runResult: agent.TurnRunResult{Status: agent.TurnCompleted},
	}
	factory := &stubAgentFactory{runner: mockRunner}
	tr := NewTeamRunner(svc, factory)

	member, err := tr.SpawnMember(context.Background(), snap.Team.ID, "m1", "coder", "{}", agent.AgentSpec{})
	require.NoError(t, err)

	time.Sleep(100 * time.Millisecond)

	status, err := tr.Status(context.Background(), snap.Team.ID)
	require.NoError(t, err)
	assert.Equal(t, snap.Team.ID, status.TeamID)

	ms, ok := status.Members[member.ID]
	require.True(t, ok)
	assert.Equal(t, "coder", ms.Role)
	assert.Equal(t, MemberIdle, ms.State)
}

// --- Task 5: Goroutine leak + late-run safety ---

// blockingTurnRunner blocks Run() until either done is closed or ctx is cancelled.
type blockingTurnRunner struct {
	done      chan struct{}
	runResult agent.TurnRunResult
}

func (m *blockingTurnRunner) Run(ctx context.Context, call agent.TeamAgentCall) (agent.TurnRunResult, error) {
	select {
	case <-ctx.Done():
		return agent.TurnRunResult{}, ctx.Err()
	case <-m.done:
		return m.runResult, nil
	}
}

func (m *blockingTurnRunner) Cancel(sessionID string) {}

func (m *blockingTurnRunner) IsSessionBusy(sessionID string) bool { return false }

func TestTeamRunner_SpawnMember_NoGoroutineLeak(t *testing.T) {
	svc, _ := newServiceFixture(t)
	snap, err := svc.CreateTeam(context.Background(), CreateTeamRequest{
		WorkspaceID: "ws-leak", LeaderSessionID: "lead-leak", Name: "leak-test",
	})
	require.NoError(t, err)

	mockRunner := &recordingTurnRunner{
		runResult: agent.TurnRunResult{Status: agent.TurnCompleted},
	}
	factory := &stubAgentFactory{runner: mockRunner}
	tr := NewTeamRunner(svc, factory)

	before := runtime.NumGoroutine()

	member, err := tr.SpawnMember(context.Background(), snap.Team.ID, "m1", "coder", "{}", agent.AgentSpec{})
	require.NoError(t, err)

	time.Sleep(200 * time.Millisecond)

	err = tr.StopMember(context.Background(), snap.Team.ID, member.ID, StopCancel)
	require.NoError(t, err)

	time.Sleep(200 * time.Millisecond)

	after := runtime.NumGoroutine()
	assert.LessOrEqual(t, after-before, 3, "goroutine leak: before=%d after=%d delta=%d", before, after, after-before)
}

func TestTeamRunner_LateRunDoesNotOverwriteCanceledState(t *testing.T) {
	svc, _ := newServiceFixture(t)
	snap, err := svc.CreateTeam(context.Background(), CreateTeamRequest{
		WorkspaceID: "ws-late", LeaderSessionID: "lead-late", Name: "late-run-test",
	})
	require.NoError(t, err)

	blockingRunner := &blockingTurnRunner{done: make(chan struct{})}
	factory := &stubAgentFactory{runner: blockingRunner}
	tr := NewTeamRunner(svc, factory)

	member, err := tr.SpawnMember(context.Background(), snap.Team.ID, "m1", "coder", "{}", agent.AgentSpec{})
	require.NoError(t, err)

	time.Sleep(100 * time.Millisecond)

	// Wake the member — it will block in Run().
	trImpl := tr.(*teamRunner)
	trImpl.mu.RLock()
	mr := trImpl.members[member.ID]
	trImpl.mu.RUnlock()
	mr.Wake(WakeSourceExplicit)

	time.Sleep(100 * time.Millisecond)

	// Cancel the member while it's running.
	err = tr.CancelMemberTurn(context.Background(), CancelMemberTurnRequest{
		TeamID:      snap.Team.ID,
		MemberID:    member.ID,
		RequestedBy: "leader",
		Reason:      "test cancel",
		Timeout:     5 * time.Second,
	})
	require.NoError(t, err)

	// Now let the Run complete (late result).
	close(blockingRunner.done)

	time.Sleep(200 * time.Millisecond)

	// Verify member is still in terminal state (stopped).
	status, err := tr.Status(context.Background(), snap.Team.ID)
	require.NoError(t, err)
	ms := status.Members[member.ID]
	assert.Equal(t, MemberStopped, ms.State, "late run should not overwrite stopped state")
}

// --- Task 6: Edge cases ---

func TestTeamRunner_StopMember_NotFound(t *testing.T) {
	svc, _ := newServiceFixture(t)
	tr := NewTeamRunner(svc, &stubAgentFactory{})
	err := tr.StopMember(context.Background(), "no-such-team", "no-such-member", StopCancel)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not found")
}

func TestTeamRunner_CancelMemberTurn_NotFound(t *testing.T) {
	svc, _ := newServiceFixture(t)
	tr := NewTeamRunner(svc, &stubAgentFactory{})
	err := tr.CancelMemberTurn(context.Background(), CancelMemberTurnRequest{
		TeamID: "x", MemberID: "y",
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not found")
}

func TestTeamRunner_StartTeam_NoMembers(t *testing.T) {
	svc, _ := newServiceFixture(t)
	snap, err := svc.CreateTeam(context.Background(), CreateTeamRequest{
		WorkspaceID: "ws-empty", LeaderSessionID: "lead-empty", Name: "empty-team",
	})
	require.NoError(t, err)
	tr := NewTeamRunner(svc, &stubAgentFactory{})
	err = tr.StartTeam(context.Background(), snap.Team.ID)
	require.NoError(t, err)
	status, err := tr.Status(context.Background(), snap.Team.ID)
	require.NoError(t, err)
	assert.Empty(t, status.Members)
}

func TestTeamRunner_Status_EmptyTeam(t *testing.T) {
	svc, _ := newServiceFixture(t)
	tr := NewTeamRunner(svc, &stubAgentFactory{})
	status, err := tr.Status(context.Background(), "no-such-team")
	require.NoError(t, err)
	assert.Empty(t, status.Members)
	assert.Equal(t, 0, status.ActiveRuns)
}
