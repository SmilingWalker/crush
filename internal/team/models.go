// models.go defines the M3 team-data-domain type layer: the four Status enums
// (TeamStatus/MemberStatus/TaskStatus/RunStatus) that gate the lifecycle of a
// team, its members, tasks, and runs, plus the six domain structs (Team,
// TeamMember, TeamTask, TeamRun, TeamEvent, AuditEvent) and the TeamSnapshot
// aggregate that the M3 stores (M3-04) and TeamService (M3-05) consume.
//
// These domain types are deliberately separate from the sqlc-generated package
// db row types: domain uses idiomatic Go (time.Time, *int64/*time.Time
// pointers for nullable columns, named string types for enums); package db
// uses SQLite-native types (int64 epoch millis, sql.NullInt64, bare string).
// M3-04 owns the to<DomainType>(row) translation between the layers.
//
// This file depends only on the standard library (time). It does not import
// package db, sqlc output, or the M2 delegate types.

package team

import "time"

// TeamStatus is the lifecycle state of a Team. Gates teams.status
// (migration 20260614000000_create_team_tables.sql:18, default 'created').
type TeamStatus string

const (
	TeamCreated   TeamStatus = "created"
	TeamRunning   TeamStatus = "running"
	TeamPaused    TeamStatus = "paused"
	TeamCanceling TeamStatus = "canceling"
	TeamStopped   TeamStatus = "stopped"
	TeamCompleted TeamStatus = "completed"
	TeamFailed    TeamStatus = "failed"
	TeamArchived  TeamStatus = "archived"
)

// allTeamStatuses is the single source of truth for the TeamStatus const set.
// Valid() and the tests both read it, so adding a const here without covering
// it in Valid() is caught by the round-trip table test.
var allTeamStatuses = []TeamStatus{
	TeamCreated, TeamRunning, TeamPaused, TeamCanceling,
	TeamStopped, TeamCompleted, TeamFailed, TeamArchived,
}

// Valid reports whether s is one of the declared TeamStatus consts.
func (s TeamStatus) Valid() bool {
	for _, v := range allTeamStatuses {
		if s == v {
			return true
		}
	}
	return false
}

// MemberStatus is the lifecycle state of a TeamMember. Gates
// team_members.status (migration :37, default 'created'). 11 values.
type MemberStatus string

const (
	MemberCreated           MemberStatus = "created"
	MemberStarting          MemberStatus = "starting"
	MemberIdle              MemberStatus = "idle"
	MemberQueued            MemberStatus = "queued"
	MemberRunning           MemberStatus = "running"
	MemberWaitingPermission MemberStatus = "waiting_permission"
	MemberBlocked           MemberStatus = "blocked"
	MemberCancelingTurn     MemberStatus = "canceling_turn"
	MemberShuttingDown      MemberStatus = "shutting_down"
	MemberStopped           MemberStatus = "stopped"
	MemberFailed            MemberStatus = "failed"
)

var allMemberStatuses = []MemberStatus{
	MemberCreated, MemberStarting, MemberIdle, MemberQueued, MemberRunning,
	MemberWaitingPermission, MemberBlocked, MemberCancelingTurn,
	MemberShuttingDown, MemberStopped, MemberFailed,
}

func (s MemberStatus) Valid() bool {
	for _, v := range allMemberStatuses {
		if s == v {
			return true
		}
	}
	return false
}

// TaskStatus is the lifecycle state of a TeamTask. Gates team_tasks.status
// (migration :56, default 'queued'). 7 values.
type TaskStatus string

const (
	TaskQueued     TaskStatus = "queued"
	TaskAssigned   TaskStatus = "assigned"
	TaskInProgress TaskStatus = "in_progress"
	TaskBlocked    TaskStatus = "blocked"
	TaskCompleted  TaskStatus = "completed"
	TaskFailed     TaskStatus = "failed"
	TaskCanceled   TaskStatus = "canceled"
)

var allTaskStatuses = []TaskStatus{
	TaskQueued, TaskAssigned, TaskInProgress, TaskBlocked,
	TaskCompleted, TaskFailed, TaskCanceled,
}

func (s TaskStatus) Valid() bool {
	for _, v := range allTaskStatuses {
		if s == v {
			return true
		}
	}
	return false
}

// RunStatus is the lifecycle state of a TeamRun. Gates team_runs.status
// (migration :73, default 'queued'). 7 values.
type RunStatus string

const (
	RunQueued            RunStatus = "queued"
	RunRunning           RunStatus = "running"
	RunWaitingPermission RunStatus = "waiting_permission"
	RunCompleted         RunStatus = "completed"
	RunFailed            RunStatus = "failed"
	RunCanceled          RunStatus = "canceled"
	RunInterrupted       RunStatus = "interrupted"
)

var allRunStatuses = []RunStatus{
	RunQueued, RunRunning, RunWaitingPermission, RunCompleted,
	RunFailed, RunCanceled, RunInterrupted,
}

func (s RunStatus) Valid() bool {
	for _, v := range allRunStatuses {
		if s == v {
			return true
		}
	}
	return false
}

// Team is the domain representation of a teams row. Nullable columns
// (max_cost/max_tokens/archived_at) are *int64/*time.Time so the nil case is
// distinguishable from a zero value; timestamps are time.Time (sqlc stores
// them as int64 epoch millis; M3-04 converts via time.UnixMilli).
type Team struct {
	ID              string     `json:"id"`
	WorkspaceID     string     `json:"workspace_id"`
	LeaderSessionID string     `json:"leader_session_id"`
	Name            string     `json:"name"`
	Description     string     `json:"description,omitempty"`
	Status          TeamStatus `json:"status"`
	Version         int        `json:"version"`
	MaxCost         *int64     `json:"max_cost,omitempty"`
	MaxTokens       *int64     `json:"max_tokens,omitempty"`
	CostSoFarMicros int64      `json:"cost_so_far_micros"`
	CreatedAt       time.Time  `json:"created_at"`
	UpdatedAt       time.Time  `json:"updated_at"`
	ArchivedAt      *time.Time `json:"archived_at,omitempty"`
}

// TeamMember is the domain representation of a team_members row. Nullable
// TEXT/INTEGER columns are pointers; version/last_event_seq/cost_so_far_micros
// are non-null so they stay plain int64.
type TeamMember struct {
	ID              string       `json:"id"`
	TeamID          string       `json:"team_id"`
	SessionID       *string      `json:"session_id,omitempty"`
	Name            string       `json:"name"`
	Role            string       `json:"role"`
	AgentProfile    string       `json:"agent_profile"` // JSON blob; opaque to domain
	ModelProvider   *string      `json:"model_provider,omitempty"`
	ModelName       *string      `json:"model_name,omitempty"`
	Status          MemberStatus `json:"status"`
	CurrentTaskID   *string      `json:"current_task_id,omitempty"`
	CurrentRunID    *string      `json:"current_run_id,omitempty"`
	CurrentToolName *string      `json:"current_tool_name,omitempty"`
	LastEventSeq    int64        `json:"last_event_seq"`
	MaxCost         *int64       `json:"max_cost,omitempty"`
	MaxTokens       *int64       `json:"max_tokens,omitempty"`
	CostSoFarMicros int64        `json:"cost_so_far_micros"`
	Version         int          `json:"version"`
	CreatedAt       time.Time    `json:"created_at"`
	UpdatedAt       time.Time    `json:"updated_at"`
	StoppedAt       *time.Time   `json:"stopped_at,omitempty"`
}
