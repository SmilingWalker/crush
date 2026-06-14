package team

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestStatus_Valid_AllConsts locks acceptance #1: every declared const of each
// Status type must report Valid()==true, and any string not in the const set
// must report false. The const-set slices (allTeamStatuses etc.) are the single
// source of truth — if a const is added without a case in Valid(), the
// allConsts loop here catches it.
func TestStatus_Valid_AllConsts(t *testing.T) {
	t.Run("TeamStatus", func(t *testing.T) {
		for _, s := range allTeamStatuses {
			assert.Truef(t, s.Valid(), "const %q should be Valid", s)
		}
		// Exhaustive set check: 8 values per master doc :285-295.
		assert.Len(t, allTeamStatuses, 8, "TeamStatus must have exactly 8 consts")
		// Bogus values must be invalid.
		for _, bad := range []TeamStatus{"", "CREATED", "paused ", "unknown", "archived "} {
			assert.Falsef(t, bad.Valid(), "bogus %q must be invalid", bad)
		}
	})

	t.Run("MemberStatus", func(t *testing.T) {
		for _, s := range allMemberStatuses {
			assert.Truef(t, s.Valid(), "const %q should be Valid", s)
		}
		assert.Len(t, allMemberStatuses, 11, "MemberStatus must have exactly 11 consts")
		for _, bad := range []MemberStatus{"", "RUNNING", "waiting-permission", "stopped_at"} {
			assert.Falsef(t, bad.Valid(), "bogus %q must be invalid", bad)
		}
	})

	t.Run("TaskStatus", func(t *testing.T) {
		for _, s := range allTaskStatuses {
			assert.Truef(t, s.Valid(), "const %q should be Valid", s)
		}
		assert.Len(t, allTaskStatuses, 7, "TaskStatus must have exactly 7 consts")
		for _, bad := range []TaskStatus{"", "IN_PROGRESS", "in-progress", "done"} {
			assert.Falsef(t, bad.Valid(), "bogus %q must be invalid", bad)
		}
	})

	t.Run("RunStatus", func(t *testing.T) {
		for _, s := range allRunStatuses {
			assert.Truef(t, s.Valid(), "const %q should be Valid", s)
		}
		assert.Len(t, allRunStatuses, 7, "RunStatus must have exactly 7 consts")
		for _, bad := range []RunStatus{"", "RUNNING", "interrupted ", "success"} {
			assert.Falsef(t, bad.Valid(), "bogus %q must be invalid", bad)
		}
	})
}

// assertTimeRoundTrip compares two time.Time values the way a JSON round-trip
// does: by wall-clock equality (time.Time.Equal), NOT reflect.DeepEqual. JSON
// marshaling emits RFC3339Nano in UTC and drops the monotonic reading, so a
// DeepEqual comparison would spuriously fail for inputs in a non-UTC location
// or carrying a monotonic clock. This helper encodes the correct semantics
// for acceptance #2 ("JSON round-trip consistent").
func assertTimeRoundTrip(t *testing.T, want, got time.Time, msg string) {
	t.Helper()
	assert.Truef(t, want.Equal(got), "%s: time round-trip mismatch want=%v got=%v", msg, want, got)
}

// roundTrip marshals v to JSON, unmarshals into targetPtr (a *T the caller
// controls), and returns the raw bytes for omitempty-key assertions.
func roundTrip(t *testing.T, v any, targetPtr any) []byte {
	t.Helper()
	b, err := json.Marshal(v)
	require.NoError(t, err)
	require.NoError(t, json.Unmarshal(b, targetPtr))
	return b
}

// TestTeam_JSONRoundTrip locks acceptance #2 for Team: every field round-trips,
// nullable pointers (MaxCost/MaxTokens/ArchivedAt) survive both set and nil,
// and omitempty drops the absent keys. Timestamps compared via Equal (UTC
// normalization on marshal).
func TestTeam_JSONRoundTrip(t *testing.T) {
	created := time.Date(2026, 6, 14, 12, 0, 0, 0, time.UTC)
	updated := created.Add(time.Hour)
	archived := created.Add(2 * time.Hour)
	maxCost := int64(5000000) // 5 USD in micros
	maxTokens := int64(1000000)

	t.Run("all_fields_populated", func(t *testing.T) {
		in := Team{
			ID:              "team-1",
			WorkspaceID:     "ws-1",
			LeaderSessionID: "sess-leader",
			Name:            "squad",
			Description:     "a team",
			Status:          TeamRunning,
			Version:         3,
			MaxCost:         &maxCost,
			MaxTokens:       &maxTokens,
			CostSoFarMicros: 1234,
			CreatedAt:       created,
			UpdatedAt:       updated,
			ArchivedAt:      &archived,
		}
		var out Team
		raw := roundTrip(t, in, &out)
		assert.Equal(t, in.ID, out.ID)
		assert.Equal(t, in.WorkspaceID, out.WorkspaceID)
		assert.Equal(t, in.LeaderSessionID, out.LeaderSessionID)
		assert.Equal(t, in.Name, out.Name)
		assert.Equal(t, in.Description, out.Description)
		assert.Equal(t, in.Status, out.Status)
		assert.Equal(t, in.Version, out.Version)
		require.NotNil(t, out.MaxCost)
		assert.Equal(t, maxCost, *out.MaxCost)
		require.NotNil(t, out.MaxTokens)
		assert.Equal(t, maxTokens, *out.MaxTokens)
		assert.Equal(t, in.CostSoFarMicros, out.CostSoFarMicros)
		assertTimeRoundTrip(t, in.CreatedAt, out.CreatedAt, "CreatedAt")
		assertTimeRoundTrip(t, in.UpdatedAt, out.UpdatedAt, "UpdatedAt")
		require.NotNil(t, out.ArchivedAt)
		assertTimeRoundTrip(t, archived, *out.ArchivedAt, "ArchivedAt")
		// status is a named string type — JSON value is the bare string.
		var probe struct {
			Status string `json:"status"`
		}
		require.NoError(t, json.Unmarshal(raw, &probe))
		assert.Equal(t, "running", probe.Status)
	})

	t.Run("nullable_pointers_nil_and_omitempty", func(t *testing.T) {
		in := Team{
			ID: "team-2", WorkspaceID: "ws-1", LeaderSessionID: "s",
			Name: "bare", Status: TeamCreated, Version: 1,
			CreatedAt: created, UpdatedAt: updated,
			// MaxCost, MaxTokens, ArchivedAt nil; Description "".
		}
		var out Team
		raw := roundTrip(t, in, &out)
		assert.Nil(t, out.MaxCost)
		assert.Nil(t, out.MaxTokens)
		assert.Nil(t, out.ArchivedAt)
		assert.Equal(t, "", out.Description)
		// omitempty: absent keys must not appear in the marshaled JSON.
		var keys map[string]json.RawMessage
		require.NoError(t, json.Unmarshal(raw, &keys))
		for _, absent := range []string{"max_cost", "max_tokens", "archived_at", "description"} {
			_, present := keys[absent]
			assert.Falsef(t, present, "omitempty field %q should be absent when zero/nil", absent)
		}
	})

	t.Run("zero_cost_pointer_survives", func(t *testing.T) {
		// omitempty does NOT drop a non-nil pointer to zero — only nil.
		zero := int64(0)
		in := Team{
			ID: "team-3", WorkspaceID: "ws-1", LeaderSessionID: "s",
			Name: "zero", Status: TeamCreated, Version: 1,
			MaxCost: &zero, CreatedAt: created, UpdatedAt: updated,
		}
		var out Team
		raw := roundTrip(t, in, &out)
		require.NotNil(t, out.MaxCost)
		assert.Equal(t, int64(0), *out.MaxCost)
		var keys map[string]json.RawMessage
		require.NoError(t, json.Unmarshal(raw, &keys))
		_, present := keys["max_cost"]
		assert.True(t, present, "non-nil pointer to 0 must survive omitempty")
	})
}

// TestTeamMember_JSONRoundTrip locks acceptance #2 for TeamMember. Every column
// from team_members (migration :28-49) maps to a domain field; nullable
// session_id / model_provider / model_name / current_* / max_* / stopped_at
// survive both set and nil.
func TestTeamMember_JSONRoundTrip(t *testing.T) {
	created := time.Date(2026, 6, 14, 12, 0, 0, 0, time.UTC)
	updated := created.Add(time.Hour)
	stopped := created.Add(2 * time.Hour)
	sess := "sess-1"
	prov := "anthropic"
	model := "claude-opus-4-8"
	taskID := "task-9"
	runID := "run-1"
	tool := "bash"
	maxCost := int64(2000000)
	maxTok := int64(500000)

	t.Run("all_fields_populated", func(t *testing.T) {
		in := TeamMember{
			ID: "member-1", TeamID: "team-1", SessionID: &sess,
			Name: "researcher", Role: "programmer", AgentProfile: `{"type":"explore"}`,
			ModelProvider: &prov, ModelName: &model, Status: MemberRunning,
			CurrentTaskID: &taskID, CurrentRunID: &runID, CurrentToolName: &tool,
			LastEventSeq: 42, MaxCost: &maxCost, MaxTokens: &maxTok,
			CostSoFarMicros: 999, Version: 2, CreatedAt: created, UpdatedAt: updated,
			StoppedAt: &stopped,
		}
		var out TeamMember
		roundTrip(t, in, &out)
		assert.Equal(t, in.ID, out.ID)
		assert.Equal(t, in.TeamID, out.TeamID)
		require.NotNil(t, out.SessionID)
		assert.Equal(t, sess, *out.SessionID)
		assert.Equal(t, in.Name, out.Name)
		assert.Equal(t, in.Role, out.Role)
		assert.Equal(t, in.AgentProfile, out.AgentProfile)
		require.NotNil(t, out.ModelProvider)
		assert.Equal(t, prov, *out.ModelProvider)
		require.NotNil(t, out.ModelName)
		assert.Equal(t, model, *out.ModelName)
		assert.Equal(t, MemberRunning, out.Status)
		require.NotNil(t, out.CurrentTaskID)
		assert.Equal(t, taskID, *out.CurrentTaskID)
		require.NotNil(t, out.CurrentRunID)
		assert.Equal(t, runID, *out.CurrentRunID)
		require.NotNil(t, out.CurrentToolName)
		assert.Equal(t, tool, *out.CurrentToolName)
		assert.Equal(t, int64(42), out.LastEventSeq)
		require.NotNil(t, out.MaxCost)
		assert.Equal(t, maxCost, *out.MaxCost)
		require.NotNil(t, out.MaxTokens)
		assert.Equal(t, maxTok, *out.MaxTokens)
		assert.Equal(t, in.CostSoFarMicros, out.CostSoFarMicros)
		assert.Equal(t, in.Version, out.Version)
		assertTimeRoundTrip(t, in.CreatedAt, out.CreatedAt, "CreatedAt")
		assertTimeRoundTrip(t, in.UpdatedAt, out.UpdatedAt, "UpdatedAt")
		require.NotNil(t, out.StoppedAt)
		assertTimeRoundTrip(t, stopped, *out.StoppedAt, "StoppedAt")
	})

	t.Run("nullable_all_nil", func(t *testing.T) {
		in := TeamMember{
			ID: "member-2", TeamID: "team-1", Name: "bare", Role: "programmer",
			AgentProfile: `{"type":"general-purpose"}`, Status: MemberCreated,
			LastEventSeq: 0, CostSoFarMicros: 0, Version: 1,
			CreatedAt: created, UpdatedAt: updated,
		}
		var out TeamMember
		raw := roundTrip(t, in, &out)
		for _, p := range []interface{}{out.SessionID, out.ModelProvider, out.ModelName,
			out.CurrentTaskID, out.CurrentRunID, out.CurrentToolName,
			out.MaxCost, out.MaxTokens, out.StoppedAt} {
			assert.Nil(t, p)
		}
		var keys map[string]json.RawMessage
		require.NoError(t, json.Unmarshal(raw, &keys))
		for _, absent := range []string{"session_id", "model_provider", "model_name",
			"current_task_id", "current_run_id", "current_tool_name",
			"max_cost", "max_tokens", "stopped_at"} {
			_, present := keys[absent]
			assert.Falsef(t, present, "nullable field %q must be absent when nil", absent)
		}
	})
}
