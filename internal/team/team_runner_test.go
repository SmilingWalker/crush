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

	// Wait for the background goroutine to start and complete the turn.
	time.Sleep(200 * time.Millisecond)

	// Verify member is registered and status reflects idle after turn.
	status, err := tr.Status(context.Background(), snap.Team.ID)
	require.NoError(t, err)
	ms, ok := status.Members[member.ID]
	require.True(t, ok, "member %s should be registered", member.ID)
	assert.Equal(t, MemberIdle, ms.State)
}
