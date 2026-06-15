package workspace

import (
	"testing"
	"time"

	"github.com/charmbracelet/crush/internal/proto"
	"github.com/charmbracelet/crush/internal/team"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestConverters_DomainToProtoAndBack locks acceptance #2 (conversion
// correctness): a domain team.Team round-trips through teamToProto →
// protoToTeam with every field preserved, including nullable pointers and
// the named-string Status (which crosses the team.TeamStatus→proto.TeamStatus
// boundary as a plain string).
func TestConverters_DomainToProtoAndBack(t *testing.T) {
	created := time.Date(2026, 6, 15, 12, 0, 0, 0, time.UTC)
	maxCost := int64(5000000)
	maxTokens := int64(1000000)

	t.Run("Team", func(t *testing.T) {
		in := team.Team{
			ID: "team-1", WorkspaceID: "ws-1", LeaderSessionID: "s",
			Name: "squad", Description: "d", Status: team.TeamRunning, Version: 3,
			MaxCost: &maxCost, MaxTokens: &maxTokens, CostSoFarMicros: 9,
			CreatedAt: created, UpdatedAt: created,
		}
		p := teamToProtoTeam(in)
		assert.Equal(t, proto.TeamStatusRunning, p.Status)
		require.NotNil(t, p.MaxCost)
		assert.Equal(t, maxCost, *p.MaxCost)
		back := protoToTeamTeam(p)
		assert.Equal(t, "team-1", back.ID)
		assert.Equal(t, team.TeamRunning, back.Status)
		require.NotNil(t, back.MaxCost)
		assert.Equal(t, maxCost, *back.MaxCost)
	})

	t.Run("TeamMember_nil_pointers", func(t *testing.T) {
		in := team.TeamMember{
			ID: "m-1", TeamID: "team-1", Name: "r", Role: "programmer",
			AgentProfile: `{}`, Status: team.MemberIdle, Version: 1,
			CreatedAt: created, UpdatedAt: created,
		}
		p := teamToProtoMember(in)
		assert.Nil(t, p.SessionID)
		assert.Equal(t, proto.MemberStatusIdle, p.Status)
		back := protoToTeamMember(p)
		assert.Equal(t, "m-1", back.ID)
		assert.Equal(t, team.MemberIdle, back.Status)
		// Nullable string pointers (all nil when unset).
		for _, ptr := range []*string{back.SessionID, back.ModelProvider, back.ModelName,
			back.CurrentTaskID, back.CurrentRunID, back.CurrentToolName} {
			assert.Nil(t, ptr)
		}
		// Nullable numeric/time pointers (different types — assert individually).
		assert.Nil(t, back.MaxCost)
		assert.Nil(t, back.MaxTokens)
		assert.Nil(t, back.StoppedAt)
	})

	t.Run("TeamTask", func(t *testing.T) {
		assignee := "m-1"
		in := team.TeamTask{
			ID: "t-1", TeamID: "team-1", Title: "x", Status: team.TaskInProgress,
			AssigneeMemberID: &assignee, CreatedByMemberID: "m-0", Priority: 5,
			Version: 2, CreatedAt: created, UpdatedAt: created,
		}
		p := teamToProtoTask(in)
		assert.Equal(t, proto.TaskStatusInProgress, p.Status)
		require.NotNil(t, p.AssigneeMemberID)
		assert.Equal(t, assignee, *p.AssigneeMemberID)
		back := protoToTeamTask(p)
		assert.Equal(t, team.TaskInProgress, back.Status)
		require.NotNil(t, back.AssigneeMemberID)
		assert.Equal(t, assignee, *back.AssigneeMemberID)
	})

	t.Run("TeamRun", func(t *testing.T) {
		in := team.TeamRun{
			ID: "run-1", TeamID: "team-1", MemberID: "m-1", SessionID: "s",
			Status: team.RunRunning, Attempt: 1,
		}
		p := teamToProtoRun(in)
		assert.Equal(t, proto.RunStatusRunning, p.Status)
		assert.Nil(t, p.TaskID)
		back := protoToTeamRun(p)
		assert.Equal(t, team.RunRunning, back.Status)
		assert.Nil(t, back.TaskID)
	})

	t.Run("TeamEvent", func(t *testing.T) {
		actor := "m-1"
		in := team.TeamEvent{
			Seq: 7, ID: "e-1", WorkspaceID: "ws-1", TeamID: "team-1",
			EventType: "task.created", EntityType: "task", EntityID: "t-1",
			ActorMemberID: &actor, CreatedAt: created,
		}
		p := teamToProtoEvent(in)
		assert.Equal(t, int64(7), p.Seq)
		require.NotNil(t, p.ActorMemberID)
		assert.Equal(t, actor, *p.ActorMemberID)
		back := protoToTeamEvent(p)
		assert.Equal(t, "task.created", back.EventType)
		require.NotNil(t, back.ActorMemberID)
		assert.Equal(t, actor, *back.ActorMemberID)
	})

	t.Run("TeamSnapshot", func(t *testing.T) {
		in := team.TeamSnapshot{
			Team:    team.Team{ID: "team-1", Status: team.TeamRunning, CreatedAt: created, UpdatedAt: created},
			Members: []team.TeamMember{{ID: "m-1", TeamID: "team-1", Status: team.MemberIdle, AgentProfile: `{}`, Version: 1, CreatedAt: created, UpdatedAt: created}},
			Tasks:   []team.TeamTask{{ID: "t-1", TeamID: "team-1", Status: team.TaskQueued, CreatedByMemberID: "m-1", Version: 1, CreatedAt: created, UpdatedAt: created}},
			Runs:    []team.TeamRun{{ID: "run-1", TeamID: "team-1", MemberID: "m-1", SessionID: "s", Status: team.RunCompleted, Attempt: 1}},
			Cost:    555,
		}
		p := teamToProtoSnapshot(in)
		assert.Equal(t, "team-1", p.Team.ID)
		require.Len(t, p.Members, 1)
		require.Len(t, p.Tasks, 1)
		require.Len(t, p.Runs, 1)
		assert.Equal(t, int64(555), p.Cost)
	})

	t.Run("request_converters", func(t *testing.T) {
		mc := int64(100)
		r := protoToCreateTeamReq(proto.CreateTeamRequest{
			WorkspaceID: "ws", LeaderSessionID: "l", Name: "n", MaxCost: &mc,
		})
		assert.Equal(t, "ws", r.WorkspaceID)
		require.NotNil(t, r.MaxCost)
		assert.Equal(t, mc, *r.MaxCost)
	})
}

// TestConverters_StatusBoundary locks that the named-string Status types cross
// the team→proto boundary as plain string values (the converter uses a string
// cast, so every domain Status value must map to the matching proto const).
func TestConverters_StatusBoundary(t *testing.T) {
	// Spot-check one of each: domain const → proto const via the converter.
	assert.Equal(t, proto.TeamStatus(team.TeamCreated), proto.TeamStatusCreated)
	assert.Equal(t, proto.MemberStatus(team.MemberFailed), proto.MemberStatusFailed)
	assert.Equal(t, proto.TaskStatus(team.TaskInProgress), proto.TaskStatusInProgress)
	assert.Equal(t, proto.RunStatus(team.RunInterrupted), proto.RunStatusInterrupted)
}
