package team

import (
	"database/sql"
	"testing"
	"time"

	"github.com/charmbracelet/crush/internal/db"
	"github.com/stretchr/testify/assert"
)

// --- null/pointer/time helper round-trips ---

func TestNullStrRoundTrip(t *testing.T) {
	s := "hello"
	assert.Nil(t, nullStrToStrPtr(sql.NullString{}))
	assert.Equal(t, &s, nullStrToStrPtr(sql.NullString{String: "hello", Valid: true}))
	assert.Equal(t, sql.NullString{}, strPtrToNullStr(nil))
	assert.Equal(t, sql.NullString{String: "hello", Valid: true}, strPtrToNullStr(&s))
}

func TestNullInt64RoundTrip(t *testing.T) {
	v := int64(42)
	assert.Nil(t, nullInt64ToInt64Ptr(sql.NullInt64{}))
	assert.Equal(t, &v, nullInt64ToInt64Ptr(sql.NullInt64{Int64: 42, Valid: true}))
	assert.Equal(t, sql.NullInt64{}, int64PtrToNullInt64(nil))
	assert.Equal(t, sql.NullInt64{Int64: 42, Valid: true}, int64PtrToNullInt64(&v))
}

func TestNullInt64ToTimePtrRoundTrip(t *testing.T) {
	ts := time.UnixMilli(1700000000000)
	got := nullInt64ToTimePtr(sql.NullInt64{Int64: 1700000000000, Valid: true})
	assert.Equal(t, ts, *got)
	assert.Nil(t, nullInt64ToTimePtr(sql.NullInt64{}))
	assert.Equal(t, sql.NullInt64{Int64: 1700000000000, Valid: true}, timePtrToNullInt64(&ts))
	assert.Equal(t, sql.NullInt64{}, timePtrToNullInt64(nil))
}

func TestStrPtrWithDefault(t *testing.T) {
	assert.Equal(t, "", strPtrWithDefault(nil))
	s := "x"
	assert.Equal(t, "x", strPtrWithDefault(&s))
}

func TestStrPtrOrNil(t *testing.T) {
	assert.Nil(t, strPtrOrNil(""))
	p := strPtrOrNil("set")
	assert.Equal(t, "set", *p)
}

// --- per-converter tests (all 6, nullable fields + type casts) ---

func TestToTeamNullablesAndCasts(t *testing.T) {
	row := db.Team{
		ID: "t1", WorkspaceID: "ws", LeaderSessionID: "lead", Name: "Alpha",
		Status: "created", Version: 1, CostSoFarMicros: 0,
		CreatedAt: 1700000000000, UpdatedAt: 1700000000000,
		// Description, MaxCost, MaxTokens, ArchivedAt all NULL
	}
	got := toTeam(row)
	assert.Equal(t, TeamCreated, got.Status)
	assert.Equal(t, 1, got.Version) // int64 → int cast
	assert.Equal(t, "", got.Description)
	assert.Nil(t, got.MaxCost)
	assert.Nil(t, got.MaxTokens)
	assert.Nil(t, got.ArchivedAt)
	assert.Equal(t, time.UnixMilli(1700000000000), got.CreatedAt)
}

func TestToTeamMemberNullablesAndCasts(t *testing.T) {
	row := db.TeamMember{
		ID: "m1", TeamID: "t1", Name: "coder", Role: "programmer", AgentProfile: "{}",
		Status: "created", LastEventSeq: 7, CostSoFarMicros: 99, Version: 3,
		CreatedAt: 100, UpdatedAt: 200,
		// SessionID, ModelProvider, ModelName, CurrentTaskID/RunID/ToolName,
		// MaxCost, MaxTokens, StoppedAt all NULL
	}
	got := toTeamMember(row)
	assert.Equal(t, MemberCreated, got.Status)
	assert.Equal(t, 3, got.Version) // int64 → int
	assert.Nil(t, got.SessionID)
	assert.Nil(t, got.CurrentTaskID)
	assert.Nil(t, got.MaxCost)
	assert.Nil(t, got.StoppedAt)
	assert.Equal(t, int64(7), got.LastEventSeq)  // non-null int64 stays int64
	assert.Equal(t, int64(99), got.CostSoFarMicros)

	// Set the NULLs and re-verify round-trip semantics.
	row.SessionID = sql.NullString{String: "sess-1", Valid: true}
	row.MaxCost = sql.NullInt64{Int64: 5000, Valid: true}
	row.StoppedAt = sql.NullInt64{Int64: 999, Valid: true}
	got = toTeamMember(row)
	assert.Equal(t, "sess-1", *got.SessionID)
	assert.Equal(t, int64(5000), *got.MaxCost)
	assert.Equal(t, time.UnixMilli(999), *got.StoppedAt)
}

func TestToTeamTaskStatusAndPriorityCast(t *testing.T) {
	row := db.TeamTask{
		ID: "tk1", TeamID: "t1", Title: "x", Status: "queued",
		CreatedByMemberID: "m1", Priority: 5, Version: 1,
		CreatedAt: 1, UpdatedAt: 1,
	}
	got := toTeamTask(row)
	assert.Equal(t, TaskQueued, got.Status)
	assert.Equal(t, 5, got.Priority) // int64 → int
	assert.Nil(t, got.AssigneeMemberID)
	assert.Equal(t, "", got.Description) // NULL → ""

	row.ResultSummary = sql.NullString{String: "done", Valid: true}
	row.CompletedAt = sql.NullInt64{Int64: 555, Valid: true}
	got = toTeamTask(row)
	assert.Equal(t, "done", *got.ResultSummary)
	assert.Equal(t, time.UnixMilli(555), *got.CompletedAt)
}

func TestToTeamRunNullablesAndCasts(t *testing.T) {
	row := db.TeamRun{
		ID: "r1", TeamID: "t1", MemberID: "m1", SessionID: "sess-1",
		Status: "running", Attempt: 2,
		// TaskID, HeartbeatAt, StartedAt, FinishedAt, PromptTokens,
		// CompletionTokens, CostMicros, UsageStatus, Error all NULL
	}
	got := toTeamRun(row)
	assert.Equal(t, RunRunning, got.Status)
	assert.Equal(t, 2, got.Attempt) // int64 → int
	assert.Nil(t, got.TaskID)
	assert.Nil(t, got.HeartbeatAt)
	assert.Nil(t, got.PromptTokens)
	assert.Equal(t, "", got.UsageStatus) // NULL → "" (withDefault)
	assert.Equal(t, "", got.Error)

	row.HeartbeatAt = sql.NullInt64{Int64: 777, Valid: true}
	row.PromptTokens = sql.NullInt64{Int64: 1234, Valid: true}
	row.UsageStatus = sql.NullString{String: "final", Valid: true}
	got = toTeamRun(row)
	assert.Equal(t, time.UnixMilli(777), *got.HeartbeatAt)
	assert.Equal(t, int64(1234), *got.PromptTokens)
	assert.Equal(t, "final", got.UsageStatus)
}

func TestToTeamEventNullablesAndCasts(t *testing.T) {
	row := db.TeamEvent{
		Seq: 3, ID: "e1", WorkspaceID: "ws", TeamID: "t1",
		EventType: "team.created", EntityType: "team", EntityID: "t1",
		CreatedAt: 100,
	}
	got := toTeamEvent(row)
	assert.Equal(t, int64(3), got.Seq)
	assert.Nil(t, got.ActorMemberID)
	assert.Nil(t, got.TaskID)
	assert.Nil(t, got.PayloadJSON)
	assert.Nil(t, got.PublishedAt)
	assert.Equal(t, time.UnixMilli(100), got.CreatedAt)

	row.ActorMemberID = sql.NullString{String: "m1", Valid: true}
	row.PublishedAt = sql.NullInt64{Int64: 200, Valid: true}
	got = toTeamEvent(row)
	assert.Equal(t, "m1", *got.ActorMemberID)
	assert.Equal(t, time.UnixMilli(200), *got.PublishedAt)
}

func TestToAuditEventNullablesAndCasts(t *testing.T) {
	row := db.TeamAuditEvent{
		ID: "a1", WorkspaceID: "ws", TeamID: "t1", EventType: "tool.call", CreatedAt: 300,
	}
	got := toAuditEvent(row)
	assert.Equal(t, "tool.call", got.EventType)
	assert.Nil(t, got.MemberID)
	assert.Nil(t, got.ToolCallID)
	assert.Nil(t, got.Action)
	assert.Nil(t, got.Decision)
	assert.Equal(t, time.UnixMilli(300), got.CreatedAt)

	row.MemberID = sql.NullString{String: "m1", Valid: true}
	row.ToolCallID = sql.NullString{String: "tc-1", Valid: true}
	row.Decision = sql.NullString{String: "allow", Valid: true}
	got = toAuditEvent(row)
	assert.Equal(t, "m1", *got.MemberID)
	assert.Equal(t, "tc-1", *got.ToolCallID)
	assert.Equal(t, "allow", *got.Decision)
}
