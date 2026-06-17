// leader_tools_test.go — M4.5: Leader Tools unit tests
//
// Covers the 4 leader tools (team_create, team_spawn_member, team_list, team_status)
// with stub TeamRunner and in-memory SQLite Service.

package team

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"charm.land/fantasy"
	"github.com/charmbracelet/crush/internal/agent"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// stubTeamRunner implements TeamRunner for leader tool unit tests.
type stubTeamRunner struct {
	startTeamCalled  bool
	spawnMemberErr   error
	spawnedMember    TeamMember
	statusResult     TeamRuntimeStatus
}

func (s *stubTeamRunner) StartTeam(ctx context.Context, teamID string) error {
	s.startTeamCalled = true
	return nil
}
func (s *stubTeamRunner) StopTeam(ctx context.Context, teamID string, mode StopMode) error { return nil }
func (s *stubTeamRunner) SpawnMember(ctx context.Context, teamID, name, role, agentProfile string, spec agent.AgentSpec) (TeamMember, error) {
	return s.spawnedMember, s.spawnMemberErr
}
func (s *stubTeamRunner) StopMember(ctx context.Context, teamID, memberID string, mode StopMode) error { return nil }
func (s *stubTeamRunner) CancelMemberTurn(ctx context.Context, req CancelMemberTurnRequest) error { return nil }
func (s *stubTeamRunner) WakeMember(ctx context.Context, teamID, memberID string, source WakeSource) error {
	return nil
}
func (s *stubTeamRunner) Status(ctx context.Context, teamID string) (TeamRuntimeStatus, error) {
	return s.statusResult, nil
}

// marshalLeaderParams is a test helper for leader tool JSON input.
func marshalLeaderParams(t *testing.T, v any) string {
	t.Helper()
	b, err := json.Marshal(v)
	require.NoError(t, err)
	return string(b)
}

func TestTeamCreate_Success(t *testing.T) {
	svc, _ := newServiceFixture(t)
	ctx := context.Background()
	runner := &stubTeamRunner{}

	tool := NewTeamCreateTool(svc, runner, func() string { return "test-leader-session" })
	input := marshalLeaderParams(t, map[string]string{
		"name":        "my-team",
		"description": "test",
	})
	resp, err := tool.Run(ctx, fantasy.ToolCall{ID: "c1", Name: TeamCreateToolName, Input: input})
	require.NoError(t, err)
	assert.False(t, resp.IsError)
	assert.Contains(t, resp.Content, "my-team")
	assert.Contains(t, resp.Content, "team_spawn_member")
	assert.True(t, runner.startTeamCalled)
}

func TestTeamCreate_EmptyName(t *testing.T) {
	svc, _ := newServiceFixture(t)
	tool := NewTeamCreateTool(svc, &stubTeamRunner{}, func() string { return "test-leader-session" })
	input := marshalLeaderParams(t, map[string]string{
		"name": "",
	})
	resp, err := tool.Run(context.Background(), fantasy.ToolCall{ID: "c2", Name: TeamCreateToolName, Input: input})
	assert.NoError(t, err)
	assert.True(t, resp.IsError)
	assert.Contains(t, resp.Content, "name is required")
}

func TestTeamSpawnMember_Success(t *testing.T) {
	svc, _ := newServiceFixture(t)
	ctx := context.Background()

	snap, err := svc.CreateTeam(ctx, CreateTeamRequest{
		WorkspaceID: "default", LeaderSessionID: "", Name: "T",
	})
	require.NoError(t, err)

	runner := &stubTeamRunner{
		spawnedMember: TeamMember{ID: "m1", Name: "coder", Role: "programmer"},
	}
	tool := NewTeamSpawnMemberTool(svc, runner)
	input := marshalLeaderParams(t, map[string]string{
		"team_id": snap.Team.ID,
		"name":    "coder",
		"role":    "programmer",
	})
	resp, err := tool.Run(ctx, fantasy.ToolCall{ID: "c3", Name: TeamSpawnMemberToolName, Input: input})
	require.NoError(t, err)
	assert.False(t, resp.IsError)
	assert.Contains(t, resp.Content, "coder")
	assert.True(t, strings.Contains(resp.Content, "spawned") || strings.Contains(resp.Content, "Spawned"),
		"response should mention spawned: %s", resp.Content)
}

func TestTeamSpawnMember_MissingTeamID(t *testing.T) {
	svc, _ := newServiceFixture(t)
	tool := NewTeamSpawnMemberTool(svc, &stubTeamRunner{})
	input := marshalLeaderParams(t, map[string]string{
		"name": "x",
		"role": "p",
	})
	resp, err := tool.Run(context.Background(), fantasy.ToolCall{ID: "c4", Name: TeamSpawnMemberToolName, Input: input})
	assert.NoError(t, err)
	assert.True(t, resp.IsError)
	assert.Contains(t, resp.Content, "team_id")
}

func TestTeamSpawnMember_MissingName(t *testing.T) {
	svc, _ := newServiceFixture(t)
	tool := NewTeamSpawnMemberTool(svc, &stubTeamRunner{})
	input := marshalLeaderParams(t, map[string]string{
		"team_id": "t1",
		"role":    "p",
	})
	resp, err := tool.Run(context.Background(), fantasy.ToolCall{ID: "c5", Name: TeamSpawnMemberToolName, Input: input})
	assert.NoError(t, err)
	assert.True(t, resp.IsError)
	assert.Contains(t, resp.Content, "name is required")
}

func TestTeamList_Success(t *testing.T) {
	svc, _ := newServiceFixture(t)
	ctx := context.Background()
	_, err := svc.CreateTeam(ctx, CreateTeamRequest{
		WorkspaceID: "default", LeaderSessionID: "", Name: "alpha",
	})
	require.NoError(t, err)

	tool := NewTeamListTool(svc)
	input := marshalLeaderParams(t, map[string]string{
		"workspace_id": "default",
	})
	resp, err := tool.Run(ctx, fantasy.ToolCall{ID: "c6", Name: TeamListToolName, Input: input})
	require.NoError(t, err)
	assert.False(t, resp.IsError)
	assert.Contains(t, resp.Content, "alpha")
}

func TestTeamList_Empty(t *testing.T) {
	svc, _ := newServiceFixture(t)
	tool := NewTeamListTool(svc)
	input := marshalLeaderParams(t, map[string]string{
		"workspace_id": "nonexistent",
	})
	resp, err := tool.Run(context.Background(), fantasy.ToolCall{ID: "c7", Name: TeamListToolName, Input: input})
	require.NoError(t, err)
	assert.False(t, resp.IsError)
	assert.Contains(t, resp.Content, "No teams found")
}

func TestTeamStatus_Success(t *testing.T) {
	runner := &stubTeamRunner{
		statusResult: TeamRuntimeStatus{
			TeamID: "t1",
			Members: map[string]MemberRuntimeState{
				"m1": {State: MemberIdle, Role: "programmer"},
				"m2": {State: MemberRunning, Role: "reviewer"},
			},
			ActiveRuns: 1,
		},
	}
	tool := NewTeamStatusTool(runner)
	input := marshalLeaderParams(t, map[string]string{
		"team_id": "t1",
	})
	resp, err := tool.Run(context.Background(), fantasy.ToolCall{ID: "c8", Name: TeamStatusToolName, Input: input})
	require.NoError(t, err)
	assert.False(t, resp.IsError)
	assert.Contains(t, resp.Content, "m1")
	assert.Contains(t, resp.Content, "programmer")
	assert.Contains(t, resp.Content, "1 active")
}

func TestTeamStatus_MissingTeamID(t *testing.T) {
	tool := NewTeamStatusTool(&stubTeamRunner{})
	input := marshalLeaderParams(t, map[string]string{})
	resp, err := tool.Run(context.Background(), fantasy.ToolCall{ID: "c9", Name: TeamStatusToolName, Input: input})
	require.NoError(t, err)
	assert.True(t, resp.IsError)
	assert.Contains(t, resp.Content, "team_id is required")
}
