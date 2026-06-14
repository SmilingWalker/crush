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

// TestTeamTask_JSONRoundTrip locks acceptance #2 for TeamTask. Nullable
// assignee_member_id/result_summary/completed_at are pointers; the
// non-nullable created_by_member_id stays plain string. priority is int.
func TestTeamTask_JSONRoundTrip(t *testing.T) {
	created := time.Date(2026, 6, 14, 12, 0, 0, 0, time.UTC)
	updated := created.Add(time.Hour)
	completed := created.Add(2 * time.Hour)
	assignee := "member-1"
	summary := "shipped"

	t.Run("all_fields_populated", func(t *testing.T) {
		in := TeamTask{
			ID: "task-1", TeamID: "team-1", Title: "do the thing",
			Description: "with details", Status: TaskInProgress,
			AssigneeMemberID: &assignee, CreatedByMemberID: "member-0",
			Priority: 5, Version: 2, ResultSummary: &summary,
			CreatedAt: created, UpdatedAt: updated, CompletedAt: &completed,
		}
		var out TeamTask
		roundTrip(t, in, &out)
		assert.Equal(t, in.ID, out.ID)
		assert.Equal(t, in.TeamID, out.TeamID)
		assert.Equal(t, in.Title, out.Title)
		assert.Equal(t, in.Description, out.Description)
		assert.Equal(t, TaskInProgress, out.Status)
		require.NotNil(t, out.AssigneeMemberID)
		assert.Equal(t, assignee, *out.AssigneeMemberID)
		assert.Equal(t, "member-0", out.CreatedByMemberID)
		assert.Equal(t, 5, out.Priority)
		assert.Equal(t, 2, out.Version)
		require.NotNil(t, out.ResultSummary)
		assert.Equal(t, summary, *out.ResultSummary)
		assertTimeRoundTrip(t, in.CreatedAt, out.CreatedAt, "CreatedAt")
		assertTimeRoundTrip(t, in.UpdatedAt, out.UpdatedAt, "UpdatedAt")
		require.NotNil(t, out.CompletedAt)
		assertTimeRoundTrip(t, completed, *out.CompletedAt, "CompletedAt")
	})

	t.Run("nullable_all_nil_and_omitempty", func(t *testing.T) {
		in := TeamTask{
			ID: "task-2", TeamID: "team-1", Title: "queued task",
			Status: TaskQueued, CreatedByMemberID: "member-0", Priority: 0,
			Version: 1, CreatedAt: created, UpdatedAt: updated,
		}
		var out TeamTask
		raw := roundTrip(t, in, &out)
		assert.Nil(t, out.AssigneeMemberID)
		assert.Nil(t, out.ResultSummary)
		assert.Nil(t, out.CompletedAt)
		assert.Equal(t, "", out.Description)
		var keys map[string]json.RawMessage
		require.NoError(t, json.Unmarshal(raw, &keys))
		for _, absent := range []string{"description", "assignee_member_id", "result_summary", "completed_at"} {
			_, present := keys[absent]
			assert.Falsef(t, present, "nullable field %q must be absent when nil", absent)
		}
	})
}

// TestTeamRun_JSONRoundTrip locks acceptance #2 for TeamRun. Nullable epoch
// columns (heartbeat_at/started_at/finished_at) are *time.Time; nullable token
// /cost columns are *int64. UsageStatus is a free string (final/partial/
// unknown per data-contract doc :126) — NOT a Valid()-bearing enum.
func TestTeamRun_JSONRoundTrip(t *testing.T) {
	hb := time.Date(2026, 6, 14, 12, 0, 0, 0, time.UTC)
	started := hb.Add(-time.Minute)
	finished := hb.Add(time.Minute)
	promptTok := int64(1200)
	completionTok := int64(300)
	cost := int64(4500)

	t.Run("all_fields_populated", func(t *testing.T) {
		in := TeamRun{
			ID: "run-1", TeamID: "team-1", MemberID: "member-1", TaskID: nil,
			SessionID: "sess-1", Status: RunRunning, Attempt: 1,
			HeartbeatAt: &hb, StartedAt: &started, FinishedAt: &finished,
			PromptTokens: &promptTok, CompletionTokens: &completionTok,
			CostMicros: &cost, UsageStatus: "partial", Error: "",
		}
		var out TeamRun
		roundTrip(t, in, &out)
		assert.Equal(t, in.ID, out.ID)
		assert.Equal(t, in.TeamID, out.TeamID)
		assert.Equal(t, in.MemberID, out.MemberID)
		assert.Nil(t, out.TaskID) // task_id nullable
		assert.Equal(t, "sess-1", out.SessionID)
		assert.Equal(t, RunRunning, out.Status)
		assert.Equal(t, 1, out.Attempt)
		require.NotNil(t, out.HeartbeatAt)
		assertTimeRoundTrip(t, hb, *out.HeartbeatAt, "HeartbeatAt")
		require.NotNil(t, out.StartedAt)
		assertTimeRoundTrip(t, started, *out.StartedAt, "StartedAt")
		require.NotNil(t, out.FinishedAt)
		assertTimeRoundTrip(t, finished, *out.FinishedAt, "FinishedAt")
		require.NotNil(t, out.PromptTokens)
		assert.Equal(t, promptTok, *out.PromptTokens)
		require.NotNil(t, out.CompletionTokens)
		assert.Equal(t, completionTok, *out.CompletionTokens)
		require.NotNil(t, out.CostMicros)
		assert.Equal(t, cost, *out.CostMicros)
		assert.Equal(t, "partial", out.UsageStatus)
	})

	t.Run("nullable_all_nil_queued_run", func(t *testing.T) {
		in := TeamRun{
			ID: "run-2", TeamID: "team-1", MemberID: "member-1",
			SessionID: "sess-2", Status: RunQueued, Attempt: 1,
		}
		var out TeamRun
		raw := roundTrip(t, in, &out)
		for _, p := range []interface{}{out.TaskID, out.HeartbeatAt, out.StartedAt,
			out.FinishedAt, out.PromptTokens, out.CompletionTokens, out.CostMicros} {
			assert.Nil(t, p)
		}
		assert.Equal(t, "", out.UsageStatus)
		assert.Equal(t, "", out.Error)
		var keys map[string]json.RawMessage
		require.NoError(t, json.Unmarshal(raw, &keys))
		for _, absent := range []string{"task_id", "heartbeat_at", "started_at",
			"finished_at", "prompt_tokens", "completion_tokens", "cost_micros",
			"usage_status", "error"} {
			_, present := keys[absent]
			assert.Falsef(t, present, "nullable field %q must be absent when nil", absent)
		}
	})
}

// TestTeamEvent_JSONRoundTrip locks acceptance #2 for TeamEvent. The (team_id,
// seq) pair is the logical identity (seq is the per-team monotonic counter);
// id is the event's own PK. payload_json is an opaque JSON blob string.
func TestTeamEvent_JSONRoundTrip(t *testing.T) {
	created := time.Date(2026, 6, 14, 12, 0, 0, 0, time.UTC)
	published := created.Add(time.Second)
	actor := "member-1"
	taskID := "task-1"
	runID := "run-1"
	msgID := "msg-1"
	payload := `{"diff":"abc"}`
	pubPtr := published

	t.Run("all_fields_populated", func(t *testing.T) {
		in := TeamEvent{
			Seq: 42, ID: "evt-1", WorkspaceID: "ws-1", TeamID: "team-1",
			EventType: "task.updated", EntityType: "task", EntityID: "task-1",
			ActorMemberID: &actor, TaskID: &taskID, RunID: &runID,
			MessageID: &msgID, PayloadJSON: &payload, PublishedAt: &pubPtr,
			CreatedAt: created,
		}
		var out TeamEvent
		roundTrip(t, in, &out)
		assert.Equal(t, int64(42), out.Seq)
		assert.Equal(t, "evt-1", out.ID)
		assert.Equal(t, "ws-1", out.WorkspaceID)
		assert.Equal(t, "team-1", out.TeamID)
		assert.Equal(t, "task.updated", out.EventType)
		assert.Equal(t, "task", out.EntityType)
		assert.Equal(t, "task-1", out.EntityID)
		require.NotNil(t, out.ActorMemberID)
		assert.Equal(t, actor, *out.ActorMemberID)
		require.NotNil(t, out.TaskID)
		assert.Equal(t, taskID, *out.TaskID)
		require.NotNil(t, out.RunID)
		assert.Equal(t, runID, *out.RunID)
		require.NotNil(t, out.MessageID)
		assert.Equal(t, msgID, *out.MessageID)
		require.NotNil(t, out.PayloadJSON)
		assert.Equal(t, payload, *out.PayloadJSON)
		require.NotNil(t, out.PublishedAt)
		assertTimeRoundTrip(t, published, *out.PublishedAt, "PublishedAt")
		assertTimeRoundTrip(t, created, out.CreatedAt, "CreatedAt")
	})

	t.Run("nullable_all_nil", func(t *testing.T) {
		in := TeamEvent{
			Seq: 1, ID: "evt-2", WorkspaceID: "ws-1", TeamID: "team-1",
			EventType: "team.created", EntityType: "team", EntityID: "team-1",
			CreatedAt: created,
		}
		var out TeamEvent
		raw := roundTrip(t, in, &out)
		for _, p := range []interface{}{out.ActorMemberID, out.TaskID, out.RunID,
			out.MessageID, out.PayloadJSON, out.PublishedAt} {
			assert.Nil(t, p)
		}
		var keys map[string]json.RawMessage
		require.NoError(t, json.Unmarshal(raw, &keys))
		for _, absent := range []string{"actor_member_id", "task_id", "run_id",
			"message_id", "payload_json", "published_at"} {
			_, present := keys[absent]
			assert.Falsef(t, present, "nullable field %q must be absent when nil", absent)
		}
	})
}

// TestAuditEvent_JSONRoundTrip locks acceptance #2 for AuditEvent. Audit is
// append-only (no Status/Valid()), so every optional column is a *string
// pointer. created_at is the only timestamp.
func TestAuditEvent_JSONRoundTrip(t *testing.T) {
	created := time.Date(2026, 6, 14, 12, 0, 0, 0, time.UTC)
	member := "member-1"
	task := "task-1"
	run := "run-1"
	sess := "sess-1"
	toolCall := "tc-1"
	action := "bash.exec"
	resType := "file"
	resRef := "/etc/passwd"
	inputHash := "deadbeef"
	summary := "ran ls"
	decision := "allowed"
	scope := "read-only"

	t.Run("all_fields_populated", func(t *testing.T) {
		in := AuditEvent{
			ID: "aud-1", WorkspaceID: "ws-1", TeamID: "team-1",
			MemberID: &member, TaskID: &task, RunID: &run, SessionID: &sess,
			ToolCallID: &toolCall, EventType: "tool.call", Action: &action,
			ResourceType: &resType, ResourceRef: &resRef, InputHash: &inputHash,
			Summary: &summary, Decision: &decision, Scope: &scope,
			CreatedAt: created,
		}
		var out AuditEvent
		roundTrip(t, in, &out)
		assert.Equal(t, "aud-1", out.ID)
		assert.Equal(t, "ws-1", out.WorkspaceID)
		assert.Equal(t, "team-1", out.TeamID)
		require.NotNil(t, out.MemberID)
		assert.Equal(t, member, *out.MemberID)
		require.NotNil(t, out.TaskID)
		assert.Equal(t, task, *out.TaskID)
		require.NotNil(t, out.RunID)
		assert.Equal(t, run, *out.RunID)
		require.NotNil(t, out.SessionID)
		assert.Equal(t, sess, *out.SessionID)
		require.NotNil(t, out.ToolCallID)
		assert.Equal(t, toolCall, *out.ToolCallID)
		assert.Equal(t, "tool.call", out.EventType)
		require.NotNil(t, out.Action)
		assert.Equal(t, action, *out.Action)
		require.NotNil(t, out.ResourceType)
		assert.Equal(t, resType, *out.ResourceType)
		require.NotNil(t, out.ResourceRef)
		assert.Equal(t, resRef, *out.ResourceRef)
		require.NotNil(t, out.InputHash)
		assert.Equal(t, inputHash, *out.InputHash)
		require.NotNil(t, out.Summary)
		assert.Equal(t, summary, *out.Summary)
		require.NotNil(t, out.Decision)
		assert.Equal(t, decision, *out.Decision)
		require.NotNil(t, out.Scope)
		assert.Equal(t, scope, *out.Scope)
		assertTimeRoundTrip(t, created, out.CreatedAt, "CreatedAt")
	})

	t.Run("only_required_fields", func(t *testing.T) {
		in := AuditEvent{
			ID: "aud-2", WorkspaceID: "ws-1", TeamID: "team-1",
			EventType: "team.created", CreatedAt: created,
		}
		var out AuditEvent
		raw := roundTrip(t, in, &out)
		for _, p := range []interface{}{out.MemberID, out.TaskID, out.RunID,
			out.SessionID, out.ToolCallID, out.Action, out.ResourceType,
			out.ResourceRef, out.InputHash, out.Summary, out.Decision, out.Scope} {
			assert.Nil(t, p)
		}
		var keys map[string]json.RawMessage
		require.NoError(t, json.Unmarshal(raw, &keys))
		for _, absent := range []string{"member_id", "task_id", "run_id", "session_id",
			"tool_call_id", "action", "resource_type", "resource_ref",
			"input_hash", "summary", "decision", "scope"} {
			_, present := keys[absent]
			assert.Falsef(t, present, "nullable field %q must be absent when nil", absent)
		}
	})
}

// TestTeamSnapshot_JSONRoundTrip locks acceptance #2 for the aggregate. The
// snapshot embeds slices of the domain structs and a Cost total; the round-trip
// must preserve every nested field (slices compare element-wise via assert).
func TestTeamSnapshot_JSONRoundTrip(t *testing.T) {
	now := time.Date(2026, 6, 14, 12, 0, 0, 0, time.UTC)
	in := TeamSnapshot{
		Team: Team{ID: "team-1", WorkspaceID: "ws-1", LeaderSessionID: "s",
			Name: "squad", Status: TeamRunning, Version: 1,
			CreatedAt: now, UpdatedAt: now},
		Members: []TeamMember{
			{ID: "m-1", TeamID: "team-1", Name: "r", Role: "programmer",
				AgentProfile: `{}`, Status: MemberIdle, Version: 1,
				CreatedAt: now, UpdatedAt: now},
		},
		Tasks: []TeamTask{
			{ID: "t-1", TeamID: "team-1", Title: "x", Status: TaskQueued,
				CreatedByMemberID: "m-1", Version: 1, CreatedAt: now, UpdatedAt: now},
		},
		Runs: []TeamRun{
			{ID: "run-1", TeamID: "team-1", MemberID: "m-1", SessionID: "s",
				Status: RunCompleted, Attempt: 1},
		},
		Cost: 9876,
	}
	var out TeamSnapshot
	roundTrip(t, in, &out)
	assert.Equal(t, "team-1", out.Team.ID)
	require.Len(t, out.Members, 1)
	assert.Equal(t, "m-1", out.Members[0].ID)
	require.Len(t, out.Tasks, 1)
	assert.Equal(t, "t-1", out.Tasks[0].ID)
	require.Len(t, out.Runs, 1)
	assert.Equal(t, "run-1", out.Runs[0].ID)
	assert.Equal(t, int64(9876), out.Cost)

	// Empty-snapshot round-trip (zero-value slices marshal to absent then
	// unmarshal to nil — NOT to a non-nil empty slice).
	var emptyOut TeamSnapshot
	raw := roundTrip(t, TeamSnapshot{}, &emptyOut)
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

// TestStatus_ConstSetExhaustsValid is the DRY guard for Seam 3: every const
// in the all<Type>Statuses slice must be Valid(), and the slice must not be
// empty. Because Valid() iterates the same slice, a const accidentally
// omitted from the slice (but used in code) would silently pass other tests
// — this test fails if the slice is empty or any member is invalid.
func TestStatus_ConstSetExhaustsValid(t *testing.T) {
	t.Run("team", func(t *testing.T) {
		require.NotEmpty(t, allTeamStatuses)
		for _, s := range allTeamStatuses {
			require.True(t, TeamStatus(s).Valid())
		}
	})
	t.Run("member", func(t *testing.T) {
		require.NotEmpty(t, allMemberStatuses)
		for _, s := range allMemberStatuses {
			require.True(t, MemberStatus(s).Valid())
		}
	})
	t.Run("task", func(t *testing.T) {
		require.NotEmpty(t, allTaskStatuses)
		for _, s := range allTaskStatuses {
			require.True(t, TaskStatus(s).Valid())
		}
	})
	t.Run("run", func(t *testing.T) {
		require.NotEmpty(t, allRunStatuses)
		for _, s := range allRunStatuses {
			require.True(t, RunStatus(s).Valid())
		}
	})
}
