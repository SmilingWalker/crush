// team.go defines the M3 team-data-domain wire (proto) types: the 6 entity
// types (Team/TeamMember/TeamTask/TeamRun/TeamEvent/TeamSnapshot), the 4
// Status string types, the request types, and the ListTeamsResponse /
// TeamEventsResponse paginated wrappers. These are the serialization boundary
// the M3-07 API routes + client SDK marshal across the HTTP/SSE wire.
//
// proto is deliberately a SEPARATE type set from package team's domain types
// (same field shapes, different package): domain carries business semantics
// (Valid() methods, idiomatic Go); proto is plain structs + JSON tags. The
// converters live in internal/workspace/team.go. proto does NOT import
// package team — the 4 Status types are re-declared here as proto-owned
// string types so the wire layer is self-contained.

package proto

import "time"

// --- Status types (proto-owned; mirror team.TeamStatus etc.) ---

type TeamStatus string

const (
	TeamStatusCreated   TeamStatus = "created"
	TeamStatusRunning   TeamStatus = "running"
	TeamStatusPaused    TeamStatus = "paused"
	TeamStatusCanceling TeamStatus = "canceling"
	TeamStatusStopped   TeamStatus = "stopped"
	TeamStatusCompleted TeamStatus = "completed"
	TeamStatusFailed    TeamStatus = "failed"
	TeamStatusArchived  TeamStatus = "archived"
)

type MemberStatus string

const (
	MemberStatusCreated           MemberStatus = "created"
	MemberStatusStarting          MemberStatus = "starting"
	MemberStatusIdle              MemberStatus = "idle"
	MemberStatusQueued            MemberStatus = "queued"
	MemberStatusRunning           MemberStatus = "running"
	MemberStatusWaitingPermission MemberStatus = "waiting_permission"
	MemberStatusBlocked           MemberStatus = "blocked"
	MemberStatusCancelingTurn     MemberStatus = "canceling_turn"
	MemberStatusShuttingDown      MemberStatus = "shutting_down"
	MemberStatusStopped           MemberStatus = "stopped"
	MemberStatusFailed            MemberStatus = "failed"
)

type TaskStatus string

const (
	TaskStatusQueued     TaskStatus = "queued"
	TaskStatusAssigned   TaskStatus = "assigned"
	TaskStatusInProgress TaskStatus = "in_progress"
	TaskStatusBlocked    TaskStatus = "blocked"
	TaskStatusCompleted  TaskStatus = "completed"
	TaskStatusFailed     TaskStatus = "failed"
	TaskStatusCanceled   TaskStatus = "canceled"
)

type RunStatus string

const (
	RunStatusQueued            RunStatus = "queued"
	RunStatusRunning           RunStatus = "running"
	RunStatusWaitingPermission RunStatus = "waiting_permission"
	RunStatusCompleted         RunStatus = "completed"
	RunStatusFailed            RunStatus = "failed"
	RunStatusCanceled          RunStatus = "canceled"
	RunStatusInterrupted       RunStatus = "interrupted"
)

// --- entity types (mirror team.* field-for-field; same JSON shape) ---

// Team is the wire representation of a team.
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

// TeamMember is the wire representation of a team member.
type TeamMember struct {
	ID              string       `json:"id"`
	TeamID          string       `json:"team_id"`
	SessionID       *string      `json:"session_id,omitempty"`
	Name            string       `json:"name"`
	Role            string       `json:"role"`
	AgentProfile    string       `json:"agent_profile"`
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

// TeamTask is the wire representation of a team task.
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

// TeamRun is the wire representation of a team run.
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

// TeamEvent is the wire representation of a team event (the event-sourced
// audit trail). Seq is the per-team monotonic counter.
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

// TeamSnapshot is the read-model aggregate the API/UI serves for a team.
type TeamSnapshot struct {
	Team    Team         `json:"team"`
	Members []TeamMember `json:"members,omitempty"`
	Tasks   []TeamTask   `json:"tasks,omitempty"`
	Runs    []TeamRun    `json:"runs,omitempty"`
	Cost    int64        `json:"cost"`
}

// --- response wrappers (paginated; no cursor token yet — YAGNI) ---

// ListTeamsResponse wraps a team list.
type ListTeamsResponse struct {
	Teams []Team `json:"teams,omitempty"`
}

// TeamEventsResponse wraps an event list.
type TeamEventsResponse struct {
	Events []TeamEvent `json:"events,omitempty"`
}

// --- request types (wire → Service; fields map team.*Request minus server-set) ---

// CreateTeamRequest carries the user-settable fields for creating a team.
type CreateTeamRequest struct {
	WorkspaceID     string `json:"workspace_id"`
	LeaderSessionID string `json:"leader_session_id"`
	Name            string `json:"name"`
	Description     string `json:"description,omitempty"`
	MaxCost         *int64 `json:"max_cost,omitempty"`
	MaxTokens       *int64 `json:"max_tokens,omitempty"`
}

// ListTeamsRequest narrows ListTeams. Empty filter = all non-archived teams.
type ListTeamsRequest struct {
	WorkspaceID     string `json:"workspace_id"`
	IncludeArchived bool   `json:"include_archived,omitempty"`
}

// SpawnMemberRequest creates a member in a team.
type SpawnMemberRequest struct {
	TeamID       string `json:"team_id"`
	Name         string `json:"name"`
	Role         string `json:"role"`
	AgentProfile string `json:"agent_profile"`
}

// CreateTeamTaskRequest creates a queued task in a team.
type CreateTeamTaskRequest struct {
	TeamID            string `json:"team_id"`
	Title             string `json:"title"`
	Description       string `json:"description,omitempty"`
	CreatedByMemberID string `json:"created_by_member_id"`
	Priority          int    `json:"priority,omitempty"`
}

// UpdateTeamTaskRequest is the user-facing task update (read-then-CAS).
type UpdateTeamTaskRequest struct {
	ID               string     `json:"id"`
	TeamID           string     `json:"team_id"`
	Status           TaskStatus `json:"status"`
	AssigneeMemberID *string    `json:"assignee_member_id,omitempty"`
	ResultSummary    *string    `json:"result_summary,omitempty"`
}
