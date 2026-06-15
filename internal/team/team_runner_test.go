package team

import (
	"context"
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
