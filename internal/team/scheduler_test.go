package team

import (
	"context"
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
