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

// TeamTask is the domain representation of a team_tasks row. assignee_member_id
// is nullable (a queued task has no assignee); created_by_member_id is NOT NULL
// so it stays plain string. result_summary/completed_at are nullable.
type TeamTask struct {
	ID                string     `json:"id"`
	TeamID            string     `json:"team_id"`
	Title             string     `json:"title"`
	Description       string     `json:"description,omitempty"`
	Status            TaskStatus `json:"status"`
	AssigneeMemberID  *string    `json:"assignee_member_id,omitempty"`
	CreatedByMemberID string     `json:"created_by_member_id"`
	Priority          int        `json:"priority"`
	Version           int        `json:"version"`
	ResultSummary     *string    `json:"result_summary,omitempty"`
	CreatedAt         time.Time  `json:"created_at"`
	UpdatedAt         time.Time  `json:"updated_at"`
	CompletedAt       *time.Time `json:"completed_at,omitempty"`
}

// TeamRun is the domain representation of a team_runs row. task_id is nullable
// (a run may not be tied to a task); heartbeat_at/started_at/finished_at are
// nullable epoch columns; token/cost columns are nullable INTEGER. UsageStatus
// is a free string ("final"|"partial"|"unknown", data-contract doc :126), NOT
// a Valid()-bearing enum.
type TeamRun struct {
	ID               string     `json:"id"`
	TeamID           string     `json:"team_id"`
	MemberID         string     `json:"member_id"`
	TaskID           *string    `json:"task_id,omitempty"`
	SessionID        string     `json:"session_id"`
	Status           RunStatus  `json:"status"`
	Attempt          int        `json:"attempt"`
	HeartbeatAt      *time.Time `json:"heartbeat_at,omitempty"`
	StartedAt        *time.Time `json:"started_at,omitempty"`
	FinishedAt       *time.Time `json:"finished_at,omitempty"`
	PromptTokens     *int64     `json:"prompt_tokens,omitempty"`
	CompletionTokens *int64     `json:"completion_tokens,omitempty"`
	CostMicros       *int64     `json:"cost_micros,omitempty"`
	UsageStatus      string     `json:"usage_status,omitempty"`
	Error            string     `json:"error,omitempty"`
}

// TeamEvent is the domain representation of a team_events row. The (TeamID,
// Seq) pair is the logical identity — Seq is the per-team monotonic counter
// sourced from team_event_counters (M3-04 NextEventSeq). ID is the event's own
// PK. payload_json is an opaque JSON blob string.
type TeamEvent struct {
	Seq           int64      `json:"seq"`
	ID            string     `json:"id"`
	WorkspaceID   string     `json:"workspace_id"`
	TeamID        string     `json:"team_id"`
	EventType     string     `json:"event_type"`
	EntityType    string     `json:"entity_type"`
	EntityID      string     `json:"entity_id"`
	ActorMemberID *string    `json:"actor_member_id,omitempty"`
	TaskID        *string    `json:"task_id,omitempty"`
	RunID         *string    `json:"run_id,omitempty"`
	MessageID     *string    `json:"message_id,omitempty"`
	PayloadJSON   *string    `json:"payload_json,omitempty"`
	PublishedAt   *time.Time `json:"published_at,omitempty"`
	CreatedAt     time.Time  `json:"created_at"`
}

// AuditEvent is the domain representation of a team_audit_events row. Audit is
// append-only — no Status/Valid(). All optional columns are *string; EventType
// is the only required non-key TEXT. input_hash is the only field kept as a
// plain pointer-to-string (NOT a typed hash) to stay persistence-agnostic.
type AuditEvent struct {
	ID           string    `json:"id"`
	WorkspaceID  string    `json:"workspace_id"`
	TeamID       string    `json:"team_id"`
	MemberID     *string   `json:"member_id,omitempty"`
	TaskID       *string   `json:"task_id,omitempty"`
	RunID        *string   `json:"run_id,omitempty"`
	SessionID    *string   `json:"session_id,omitempty"`
	ToolCallID   *string   `json:"tool_call_id,omitempty"`
	EventType    string    `json:"event_type"`
	Action       *string   `json:"action,omitempty"`
	ResourceType *string   `json:"resource_type,omitempty"`
	ResourceRef  *string   `json:"resource_ref,omitempty"`
	InputHash    *string   `json:"input_hash,omitempty"`
	Summary      *string   `json:"summary,omitempty"`
	Decision     *string   `json:"decision,omitempty"`
	Scope        *string   `json:"scope,omitempty"`
	CreatedAt    time.Time `json:"created_at"`
}

// TeamSnapshot is the read-model aggregate the API/UI serves for a team at a
// point in time (master doc :367-373). It bundles the team, its members, its
// tasks, its runs, and a rolled-up Cost total (micros). Slice fields are nil
// when empty (omitempty), so a fresh snapshot marshals compactly.
type TeamSnapshot struct {
	Team    Team         `json:"team"`
	Members []TeamMember `json:"members,omitempty"`
	Tasks   []TeamTask   `json:"tasks,omitempty"`
	Runs    []TeamRun    `json:"runs,omitempty"`
	Cost    int64        `json:"cost"`
}

// RecipientType selects the recipient resolution strategy for SendMessage.
// M4-04 master doc :468-476.
type RecipientType string

const (
	RecipientDirect    RecipientType = "direct"
	RecipientBroadcast RecipientType = "broadcast"
	RecipientRole      RecipientType = "role"
)

var allRecipientTypes = []RecipientType{
	RecipientDirect, RecipientBroadcast, RecipientRole,
}

func (r RecipientType) Valid() bool {
	for _, v := range allRecipientTypes {
		if r == v {
			return true
		}
	}
	return false
}

// MessageKind classifies a mailbox message for prompt-building and UI.
// M4-04 master doc :478-485.
type MessageKind string

const (
	KindMessage         MessageKind = "message"
	KindTaskAssignment  MessageKind = "task_assignment"
	KindTaskStatus      MessageKind = "task_status"
	KindShutdownRequest MessageKind = "shutdown_request"
	KindShutdownAck     MessageKind = "shutdown_ack"
)

var allMessageKinds = []MessageKind{
	KindMessage, KindTaskAssignment, KindTaskStatus,
	KindShutdownRequest, KindShutdownAck,
}

func (k MessageKind) Valid() bool {
	for _, v := range allMessageKinds {
		if k == v {
			return true
		}
	}
	return false
}

// MailboxMessage is the domain representation of a team_mailbox_messages row.
// Payload is an opaque JSON blob (string at the domain layer).
type MailboxMessage struct {
	ID           string      `json:"id"`
	TeamID       string      `json:"team_id"`
	FromMemberID string      `json:"from_member_id"`
	Kind         MessageKind `json:"kind"`
	Summary      string      `json:"summary"`
	Payload      string      `json:"payload"`
	CreatedAt    time.Time   `json:"created_at"`
}

// MessageReceipt is the domain representation of a team_message_receipts row.
// DeliveredAt/ReadAt are nullable epoch millis → *time.Time.
type MessageReceipt struct {
	ID          string     `json:"id"`
	MessageID   string     `json:"message_id"`
	ToMemberID  string     `json:"to_member_id"`
	DeliveredAt *time.Time `json:"delivered_at,omitempty"`
	ReadAt      *time.Time `json:"read_at,omitempty"`
}
