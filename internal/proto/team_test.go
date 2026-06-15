package proto

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// assertTimeRT compares two time.Time by wall-clock equality — JSON marshal
// normalizes to UTC RFC3339Nano and drops the monotonic reading, so a
// reflect.DeepEqual comparison would spuriously fail. (Same semantics as the
// M3-03 domain round-trip tests.)
func assertTimeRT(t *testing.T, want, got time.Time, msg string) {
	t.Helper()
	assert.Truef(t, want.Equal(got), "%s: time round-trip mismatch want=%v got=%v", msg, want, got)
}

func rt(t *testing.T, v any, target any) []byte {
	t.Helper()
	b, err := json.Marshal(v)
	require.NoError(t, err)
	require.NoError(t, json.Unmarshal(b, target))
	return b
}

// TestProtoTeam_JSONRoundTrip locks acceptance #2 (proto side): every field on
// proto.Team round-trips through JSON, including nullable pointers and the
// named-string Status. proto.Team must serialize identically to team.Team so
// the domain→proto converter is a pure field copy.
func TestProtoTeam_JSONRoundTrip(t *testing.T) {
	created := time.Date(2026, 6, 15, 12, 0, 0, 0, time.UTC)
	updated := created.Add(time.Hour)
	archived := created.Add(2 * time.Hour)
	maxCost := int64(5000000)
	maxTokens := int64(1000000)

	t.Run("all_fields_populated", func(t *testing.T) {
		in := Team{
			ID:              "team-1",
			WorkspaceID:     "ws-1",
			LeaderSessionID: "sess-leader",
			Name:            "squad",
			Description:     "a team",
			Status:          TeamStatusRunning,
			Version:         3,
			MaxCost:         &maxCost,
			MaxTokens:       &maxTokens,
			CostSoFarMicros: 1234,
			CreatedAt:       created,
			UpdatedAt:       updated,
			ArchivedAt:      &archived,
		}
		var out Team
		rt(t, in, &out)
		assert.Equal(t, in.ID, out.ID)
		assert.Equal(t, in.WorkspaceID, out.WorkspaceID)
		assert.Equal(t, in.LeaderSessionID, out.LeaderSessionID)
		assert.Equal(t, in.Name, out.Name)
		assert.Equal(t, in.Description, out.Description)
		assert.Equal(t, TeamStatusRunning, out.Status)
		assert.Equal(t, in.Version, out.Version)
		require.NotNil(t, out.MaxCost)
		assert.Equal(t, maxCost, *out.MaxCost)
		require.NotNil(t, out.MaxTokens)
		assert.Equal(t, maxTokens, *out.MaxTokens)
		assert.Equal(t, in.CostSoFarMicros, out.CostSoFarMicros)
		assertTimeRT(t, in.CreatedAt, out.CreatedAt, "CreatedAt")
		assertTimeRT(t, in.UpdatedAt, out.UpdatedAt, "UpdatedAt")
		require.NotNil(t, out.ArchivedAt)
		assertTimeRT(t, archived, *out.ArchivedAt, "ArchivedAt")
	})

	t.Run("nullable_nil_and_omitempty", func(t *testing.T) {
		in := Team{
			ID: "team-2", WorkspaceID: "ws-1", LeaderSessionID: "s",
			Name: "bare", Status: TeamStatusCreated, Version: 1,
			CreatedAt: created, UpdatedAt: updated,
		}
		var out Team
		raw := rt(t, in, &out)
		assert.Nil(t, out.MaxCost)
		assert.Nil(t, out.MaxTokens)
		assert.Nil(t, out.ArchivedAt)
		assert.Equal(t, "", out.Description)
		var keys map[string]json.RawMessage
		require.NoError(t, json.Unmarshal(raw, &keys))
		for _, absent := range []string{"max_cost", "max_tokens", "archived_at", "description"} {
			_, present := keys[absent]
			assert.Falsef(t, present, "omitempty field %q should be absent when zero/nil", absent)
		}
	})
}

// TestProtoSnapshot_JSONRoundTrip locks the aggregate (embeds slices of the
// entity types). The empty-snapshot case asserts slices unmarshal to nil
// (omitempty drops the absent keys), matching M3-03's TeamSnapshot semantics.
func TestProtoSnapshot_JSONRoundTrip(t *testing.T) {
	now := time.Date(2026, 6, 15, 12, 0, 0, 0, time.UTC)
	in := TeamSnapshot{
		Team: Team{ID: "team-1", WorkspaceID: "ws-1", LeaderSessionID: "s",
			Name: "squad", Status: TeamStatusRunning, Version: 1,
			CreatedAt: now, UpdatedAt: now},
		Members: []TeamMember{{ID: "m-1", TeamID: "team-1", Name: "r", Role: "programmer",
			AgentProfile: `{}`, Status: MemberStatusIdle, Version: 1, CreatedAt: now, UpdatedAt: now}},
		Tasks: []TeamTask{{ID: "t-1", TeamID: "team-1", Title: "x",
			Status: TaskStatusQueued, CreatedByMemberID: "m-1", Version: 1, CreatedAt: now, UpdatedAt: now}},
		Runs: []TeamRun{{ID: "run-1", TeamID: "team-1", MemberID: "m-1", SessionID: "s",
			Status: RunStatusCompleted, Attempt: 1}},
		Cost: 9876,
	}
	var out TeamSnapshot
	rt(t, in, &out)
	assert.Equal(t, "team-1", out.Team.ID)
	require.Len(t, out.Members, 1)
	assert.Equal(t, "m-1", out.Members[0].ID)
	require.Len(t, out.Tasks, 1)
	assert.Equal(t, "t-1", out.Tasks[0].ID)
	require.Len(t, out.Runs, 1)
	assert.Equal(t, "run-1", out.Runs[0].ID)
	assert.Equal(t, int64(9876), out.Cost)

	var emptyOut TeamSnapshot
	raw := rt(t, TeamSnapshot{}, &emptyOut)
	assert.Nil(t, emptyOut.Members)
	assert.Nil(t, emptyOut.Tasks)
	assert.Nil(t, emptyOut.Runs)
	var keys map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(raw, &keys))
	for _, absent := range []string{"members", "tasks", "runs"} {
		_, present := keys[absent]
		assert.Falsef(t, present, "empty slice field %q should be absent", absent)
	}
}

// TestProtoResponseWrappers_JSONRoundTrip locks the two paginated wrappers
// (acceptance #2): they carry only the slice today (no cursor token — YAGNI).
func TestProtoResponseWrappers_JSONRoundTrip(t *testing.T) {
	t.Run("ListTeamsResponse", func(t *testing.T) {
		in := ListTeamsResponse{Teams: []Team{{ID: "a"}, {ID: "b"}}}
		var out ListTeamsResponse
		rt(t, in, &out)
		require.Len(t, out.Teams, 2)
		assert.Equal(t, "a", out.Teams[0].ID)
	})
	t.Run("TeamEventsResponse", func(t *testing.T) {
		in := TeamEventsResponse{Events: []TeamEvent{{ID: "e1", Seq: 1}}}
		var out TeamEventsResponse
		rt(t, in, &out)
		require.Len(t, out.Events, 1)
		assert.Equal(t, int64(1), out.Events[0].Seq)
	})
	t.Run("empty_wrappers", func(t *testing.T) {
		var out ListTeamsResponse
		raw := rt(t, ListTeamsResponse{}, &out)
		assert.Nil(t, out.Teams)
		var keys map[string]json.RawMessage
		require.NoError(t, json.Unmarshal(raw, &keys))
		_, present := keys["teams"]
		assert.False(t, present, "empty teams slice should be absent (omitempty)")
	})
}
