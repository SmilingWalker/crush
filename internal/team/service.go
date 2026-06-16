// service.go is the M3-05 TeamService facade. It orchestrates the 6 M3-04
// stores behind a domain-typed Service API, owns transaction lifecycle
// (BeginTx/defer-Rollback/Commit), writes team+event+audit atomically, builds
// the TeamSnapshot/DebugSnapshot read-models, and feature-gates the whole
// surface behind an `enabled func() bool` (Seam 1: decouples from config.Options;
// M3-08 wires config.Options.IsAgentTeamEnabled into WithEnabledGate).

package team

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
)

// ErrFeatureDisabled is returned by every Service method when the feature gate
// is off. Until M3-08 wires the flag, the default gate is off (safe default).
var ErrFeatureDisabled = errors.New("agent-team feature is disabled")

// --- request / filter types (Seam 3: M3-05 owns these) ---

// CreateTeamRequest carries the user-settable fields for creating a team. ID,
// status, version, cost, timestamps are server-set.
type CreateTeamRequest struct {
	WorkspaceID     string `json:"workspace_id"`
	LeaderSessionID string `json:"leader_session_id"`
	Name            string `json:"name"`
	Description     string `json:"description,omitempty"`
	MaxCost         *int64 `json:"max_cost,omitempty"`
	MaxTokens       *int64 `json:"max_tokens,omitempty"`
}

// ArchiveTeamRequest archives a team (soft-delete via status='archived').
type ArchiveTeamRequest struct {
	ID string `json:"id"`
}

// TeamFilter narrows ListTeams. Empty filter = all non-archived teams in the
// workspace (the store's default).
type TeamFilter struct {
	IncludeArchived bool `json:"include_archived,omitempty"`
}

// SpawnMemberRequest creates a member in a team.
type SpawnMemberRequest struct {
	TeamID       string `json:"team_id"`
	Name         string `json:"name"`
	Role         string `json:"role"`
	AgentProfile string `json:"agent_profile"`
}

// UpdateMemberStateRequest transitions a member's state via CAS.
type UpdateMemberStateRequest struct {
	ID       string       `json:"id"`
	TeamID   string       `json:"team_id"`
	Status   MemberStatus `json:"status"`
	TaskID   *string      `json:"task_id,omitempty"`
	RunID    *string      `json:"run_id,omitempty"`
	ToolName *string      `json:"tool_name,omitempty"`
}

// CreateTaskRequest creates a queued task in a team.
type CreateTaskRequest struct {
	TeamID            string `json:"team_id"`
	Title             string `json:"title"`
	Description       string `json:"description,omitempty"`
	CreatedByMemberID string `json:"created_by_member_id"`
	Priority          int    `json:"priority,omitempty"`
}

// UpdateTaskRequest is the user-facing task update (read-then-CAS, Seam 7).
// The caller does NOT pass a version; the Service reads the current version and
// CASes on it. A concurrent modification yields ErrVersionConflict.
type UpdateTaskRequest struct {
	ID               string     `json:"id"`
	TeamID           string     `json:"team_id"`
	Status           TaskStatus `json:"status"`
	AssigneeMemberID *string    `json:"assignee_member_id,omitempty"`
	ResultSummary    *string    `json:"result_summary,omitempty"`
}

// StartRunRequest starts a run (member executing a session, optionally a task).
type StartRunRequest struct {
	TeamID    string  `json:"team_id"`
	MemberID  string  `json:"member_id"`
	TaskID    *string `json:"task_id,omitempty"`
	SessionID string  `json:"session_id"`
}

// FinishRunRequest finalizes a run with usage/cost.
type FinishRunRequest struct {
	TeamID           string `json:"team_id"`
	RunID            string `json:"run_id"`
	PromptTokens     int64  `json:"prompt_tokens"`
	CompletionTokens int64  `json:"completion_tokens"`
	CostMicros       int64  `json:"cost_micros"`
}

// MarkRunTerminalRequest moves a run to a terminal status with an error.
// M4-08 adds ExpectedStatus so callers can specify what status the run must
// currently be in (widens the guard beyond hard-coded 'running').
type MarkRunTerminalRequest struct {
	TeamID         string    `json:"team_id"`
	RunID          string    `json:"run_id"`
	Status         RunStatus `json:"status"`
	ExpectedStatus RunStatus `json:"expected_status,omitempty"`
	Error          string    `json:"error,omitempty"`
	UsageStatus    string    `json:"usage_status,omitempty"`
}

// TaskFilter narrows ListTasks. Empty filter = all tasks (store default).
type TaskFilter struct{}

// DebugSnapshot is the read-model the M3-09 Debug UI serves (Seam 2). It
// bundles the TeamSnapshot with the most recent events and audit rows.
type DebugSnapshot struct {
	TeamSnapshot
	Events []TeamEvent  `json:"events,omitempty"`
	Audit  []AuditEvent `json:"audit,omitempty"`
}

// Service is the M3-05 TeamService facade (master doc :455-478).
type Service interface {
	CreateTeam(ctx context.Context, req CreateTeamRequest) (TeamSnapshot, error)
	GetTeamSnapshot(ctx context.Context, workspaceID, teamID string) (TeamSnapshot, error)
	ListTeams(ctx context.Context, workspaceID string, filter TeamFilter) ([]Team, error)
	ArchiveTeam(ctx context.Context, req ArchiveTeamRequest) error

	SpawnMember(ctx context.Context, req SpawnMemberRequest) (TeamMember, error)
	UpdateMemberState(ctx context.Context, req UpdateMemberStateRequest) (TeamMember, error)
	ListMembers(ctx context.Context, teamID string) ([]TeamMember, error)

	CreateTask(ctx context.Context, req CreateTaskRequest) (TeamTask, error)
	GetTask(ctx context.Context, teamID, taskID string) (TeamTask, error)
	UpdateTask(ctx context.Context, req UpdateTaskRequest) (TeamTask, error)
	ClaimNextTask(ctx context.Context, req ClaimNextTaskRequest) (TeamTask, error)
	ListTasks(ctx context.Context, teamID string, filter TaskFilter) ([]TeamTask, error)

	StartRun(ctx context.Context, req StartRunRequest) (TeamRun, error)
	HeartbeatRun(ctx context.Context, runID string) error
	FinishRun(ctx context.Context, req FinishRunRequest) error
	MarkRunTerminal(ctx context.Context, req MarkRunTerminalRequest) error
	FindStaleRuns(ctx context.Context, cutoff time.Time) ([]TeamRun, error)
	GetTeamCost(ctx context.Context, teamID string) (TeamCost, error)

	SendMessage(ctx context.Context, req SendMessageRequest) ([]string, error)
	GetUnreadMessages(ctx context.Context, memberID string, limit int) ([]MailboxMessage, error)

		RecoverMemberSession(ctx context.Context, teamID, memberID string) (string, error)

	ListEventsAfter(ctx context.Context, teamID string, afterSeq int64, limit int) ([]TeamEvent, error)
	DebugSnapshot(ctx context.Context, teamID string) (DebugSnapshot, error)
}

// --- service struct + constructor (Seam 4) ---

type teamService struct {
	db      *sql.DB
	teams   TeamStore
	members MemberStore
	tasks   TaskStore
	runs    RunStore
	events  EventStore
	audits  AuditStore
	mailbox MailboxStore
	links   SessionLinkStore
	enabled func() bool
}

// ServiceOption configures a teamService.
type ServiceOption func(*teamService)

// WithEnabledGate sets the feature gate. Default (no option) = always disabled
// (returns ErrFeatureDisabled). M3-08 wires config.Options.IsAgentTeamEnabled.
func WithEnabledGate(fn func() bool) ServiceOption {
	return func(s *teamService) { s.enabled = fn }
}

// NewService builds a Service over the given *sql.DB + 8 stores. The feature
// gate defaults to disabled; pass WithEnabledGate to enable.
func NewService(db *sql.DB, teams TeamStore, members MemberStore, tasks TaskStore, runs RunStore, events EventStore, audits AuditStore, mailbox MailboxStore, links SessionLinkStore, opts ...ServiceOption) Service {
	s := &teamService{
		db:      db,
		teams:   teams,
		members: members,
		tasks:   tasks,
		runs:    runs,
		events:  events,
		audits:  audits,
		mailbox: mailbox,
		links:   links,
		enabled: func() bool { return false }, // safe default: disabled until wired
	}
	for _, opt := range opts {
		opt(s)
	}
	return s
}

// enabledGuard returns ErrFeatureDisabled if the gate is off.
func (s *teamService) enabledGuard() error {
	if !s.enabled() {
		return ErrFeatureDisabled
	}
	return nil
}

// now is overridable in tests via a package-level var (default time.Now).
var now = func() time.Time { return time.Now() }

// --- team + member methods ---

// CreateTeam creates a team and atomically appends a team.created event + an
// audit row, all in one tx (acceptance #1). Returns the fresh team snapshot.
func (s *teamService) CreateTeam(ctx context.Context, req CreateTeamRequest) (TeamSnapshot, error) {
	if err := s.enabledGuard(); err != nil {
		return TeamSnapshot{}, err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return TeamSnapshot{}, fmt.Errorf("begin tx: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	ts := now()
	team, err := s.teams.CreateTeam(ctx, tx, Team{
		ID:              uuid.New().String(),
		WorkspaceID:     req.WorkspaceID,
		LeaderSessionID: req.LeaderSessionID,
		Name:            req.Name,
		Description:     req.Description,
		Status:          TeamCreated,
		MaxCost:         req.MaxCost,
		MaxTokens:       req.MaxTokens,
		CreatedAt:       ts,
		UpdatedAt:       ts,
	})
	if err != nil {
		return TeamSnapshot{}, err
	}

	// Append team.created event (Seam 6: event uses the RETURNED seq directly,
	// not seq-1 — master doc :504 is off-by-one; M3-02 counter returns the seq
	// the event should use).
	seq, err := s.events.NextEventSeq(ctx, tx, team.ID, ts)
	if err != nil {
		return TeamSnapshot{}, fmt.Errorf("alloc event seq: %w", err)
	}
	if err := s.events.AppendEvent(ctx, tx, TeamEvent{
		Seq: seq, ID: uuid.New().String(), WorkspaceID: team.WorkspaceID, TeamID: team.ID,
		EventType: "team.created", EntityType: "team", EntityID: team.ID, CreatedAt: ts,
	}); err != nil {
		return TeamSnapshot{}, fmt.Errorf("append event: %w", err)
	}

	// Append audit row.
	if err := s.audits.AppendAudit(ctx, tx, AuditEvent{
		ID: uuid.New().String(), WorkspaceID: team.WorkspaceID, TeamID: team.ID,
		EventType: "team.created", Action: strPtrOrNil("create_team"), CreatedAt: ts,
	}); err != nil {
		return TeamSnapshot{}, fmt.Errorf("append audit: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return TeamSnapshot{}, fmt.Errorf("commit: %w", err)
	}
	return s.buildSnapshot(ctx, team.WorkspaceID, team.ID)
}

// buildSnapshot opens a read tx and assembles the TeamSnapshot from the team +
// its members/tasks/runs (acceptance #2 helper).
func (s *teamService) buildSnapshot(ctx context.Context, workspaceID, teamID string) (TeamSnapshot, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return TeamSnapshot{}, fmt.Errorf("begin snapshot tx: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	team, err := s.teams.GetTeam(ctx, tx, teamID)
	if err != nil {
		return TeamSnapshot{}, err
	}
	members, err := s.members.ListMembers(ctx, tx, teamID)
	if err != nil {
		return TeamSnapshot{}, err
	}
	tasks, err := s.tasks.ListTasks(ctx, tx, teamID)
	if err != nil {
		return TeamSnapshot{}, err
	}
	runs, err := s.findRunsForSnapshot(ctx, tx, teamID)
	if err != nil {
		return TeamSnapshot{}, err
	}
	var cost int64
	for _, r := range runs {
		if r.CostMicros != nil {
			cost += *r.CostMicros
		}
	}
	if err := tx.Commit(); err != nil {
		return TeamSnapshot{}, fmt.Errorf("commit snapshot: %w", err)
	}
	return TeamSnapshot{Team: team, Members: members, Tasks: tasks, Runs: runs, Cost: cost}, nil
}

// findRunsForSnapshot returns the runs for a team. BEST-EFFORT: RunStore has no
// ListRuns method, so this uses FindStaleRuns with a far-future cutoff as a
// "list all" proxy. CAVEAT: FindStaleRuns filters `heartbeat_at < cutoff AND
// status IN (...)`, so runs with a NULL heartbeat_at are excluded — the
// snapshot's Runs slice may be incomplete. Flagged for a future ListRuns query
// (small M3-04 follow-up) if exact run lists are needed.
func (s *teamService) findRunsForSnapshot(ctx context.Context, tx *sql.Tx, teamID string) ([]TeamRun, error) {
	all, err := s.runs.FindStaleRuns(ctx, tx, now().Add(100*365*24*time.Hour))
	if err != nil {
		return nil, err
	}
	out := make([]TeamRun, 0)
	for _, r := range all {
		if r.TeamID == teamID {
			out = append(out, r)
		}
	}
	return out, nil
}

func (s *teamService) GetTeamSnapshot(ctx context.Context, workspaceID, teamID string) (TeamSnapshot, error) {
	if err := s.enabledGuard(); err != nil {
		return TeamSnapshot{}, err
	}
	return s.buildSnapshot(ctx, workspaceID, teamID)
}

func (s *teamService) ListTeams(ctx context.Context, workspaceID string, filter TeamFilter) ([]Team, error) {
	if err := s.enabledGuard(); err != nil {
		return nil, err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("begin tx: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	teams, err := s.teams.ListTeams(ctx, tx, workspaceID)
	if err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("commit: %w", err)
	}
	return teams, nil
}

func (s *teamService) ArchiveTeam(ctx context.Context, req ArchiveTeamRequest) error {
	if err := s.enabledGuard(); err != nil {
		return err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	ts := now()
	if err := s.teams.ArchiveTeam(ctx, tx, req.ID, ts); err != nil {
		return err
	}
	seq, err := s.events.NextEventSeq(ctx, tx, req.ID, ts)
	if err != nil {
		return fmt.Errorf("alloc event seq: %w", err)
	}
	if err := s.events.AppendEvent(ctx, tx, TeamEvent{
		Seq: seq, ID: uuid.New().String(), TeamID: req.ID,
		EventType: "team.archived", EntityType: "team", EntityID: req.ID, CreatedAt: ts,
	}); err != nil {
		return fmt.Errorf("append event: %w", err)
	}
	return tx.Commit()
}

func (s *teamService) SpawnMember(ctx context.Context, req SpawnMemberRequest) (TeamMember, error) {
	if err := s.enabledGuard(); err != nil {
		return TeamMember{}, err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return TeamMember{}, fmt.Errorf("begin tx: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	ts := now()
	m, err := s.members.CreateMember(ctx, tx, TeamMember{
		ID: uuid.New().String(), TeamID: req.TeamID, Name: req.Name, Role: req.Role,
		AgentProfile: req.AgentProfile, Status: MemberCreated, CreatedAt: ts, UpdatedAt: ts,
	})
	if err != nil {
		return TeamMember{}, err
	}
	seq, err := s.events.NextEventSeq(ctx, tx, req.TeamID, ts)
	if err != nil {
		return TeamMember{}, fmt.Errorf("alloc event seq: %w", err)
	}
	if err := s.events.AppendEvent(ctx, tx, TeamEvent{
		Seq: seq, ID: uuid.New().String(), TeamID: req.TeamID, ActorMemberID: &m.ID,
		EventType: "member.spawned", EntityType: "member", EntityID: m.ID, CreatedAt: ts,
	}); err != nil {
		return TeamMember{}, fmt.Errorf("append event: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return TeamMember{}, fmt.Errorf("commit: %w", err)
	}
	return m, nil
}

// UpdateMemberState transitions a member via read-then-CAS and appends a
// member.updated event for event-trail consistency (matches CreateTeam /
// SpawnMember / CreateTask / StartRun, all of which AppendEvent).
func (s *teamService) UpdateMemberState(ctx context.Context, req UpdateMemberStateRequest) (TeamMember, error) {
	if err := s.enabledGuard(); err != nil {
		return TeamMember{}, err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return TeamMember{}, fmt.Errorf("begin tx: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	current, err := s.members.GetMember(ctx, tx, req.ID)
	if err != nil {
		return TeamMember{}, err
	}
	current.Status = req.Status
	current.CurrentTaskID = req.TaskID
	current.CurrentRunID = req.RunID
	current.CurrentToolName = req.ToolName
	current.UpdatedAt = now()
	updated, err := s.members.UpdateMemberCAS(ctx, tx, current, current.Version)
	if err != nil {
		return TeamMember{}, err
	}
	ts := now()
	seq, err := s.events.NextEventSeq(ctx, tx, req.TeamID, ts)
	if err != nil {
		return TeamMember{}, fmt.Errorf("alloc event seq: %w", err)
	}
	if err := s.events.AppendEvent(ctx, tx, TeamEvent{
		Seq: seq, ID: uuid.New().String(), TeamID: req.TeamID, ActorMemberID: &updated.ID,
		EventType: "member.updated", EntityType: "member", EntityID: updated.ID, CreatedAt: ts,
	}); err != nil {
		return TeamMember{}, fmt.Errorf("append event: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return TeamMember{}, fmt.Errorf("commit: %w", err)
	}
	return updated, nil
}

func (s *teamService) ListMembers(ctx context.Context, teamID string) ([]TeamMember, error) {
	if err := s.enabledGuard(); err != nil {
		return nil, err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("begin tx: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	members, err := s.members.ListMembers(ctx, tx, teamID)
	if err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("commit: %w", err)
	}
	return members, nil
}

// --- task methods ---

func (s *teamService) CreateTask(ctx context.Context, req CreateTaskRequest) (TeamTask, error) {
	if err := s.enabledGuard(); err != nil {
		return TeamTask{}, err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return TeamTask{}, fmt.Errorf("begin tx: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	ts := now()
	task, err := s.tasks.CreateTask(ctx, tx, TeamTask{
		ID: uuid.New().String(), TeamID: req.TeamID, Title: req.Title, Description: req.Description,
		CreatedByMemberID: req.CreatedByMemberID, Priority: req.Priority,
		Status: TaskQueued, CreatedAt: ts, UpdatedAt: ts,
	})
	if err != nil {
		return TeamTask{}, err
	}
	seq, err := s.events.NextEventSeq(ctx, tx, req.TeamID, ts)
	if err != nil {
		return TeamTask{}, fmt.Errorf("alloc event seq: %w", err)
	}
	if err := s.events.AppendEvent(ctx, tx, TeamEvent{
		Seq: seq, ID: uuid.New().String(), TeamID: req.TeamID, TaskID: &task.ID,
		EventType: "task.created", EntityType: "task", EntityID: task.ID, CreatedAt: ts,
	}); err != nil {
		return TeamTask{}, fmt.Errorf("append event: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return TeamTask{}, fmt.Errorf("commit: %w", err)
	}
	return task, nil
}

func (s *teamService) GetTask(ctx context.Context, teamID, taskID string) (TeamTask, error) {
	if err := s.enabledGuard(); err != nil {
		return TeamTask{}, err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return TeamTask{}, fmt.Errorf("begin tx: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	task, err := s.tasks.GetTask(ctx, tx, teamID, taskID)
	if err != nil {
		return TeamTask{}, err
	}
	if err := tx.Commit(); err != nil {
		return TeamTask{}, fmt.Errorf("commit: %w", err)
	}
	return task, nil
}

// UpdateTask is the user-facing update: read current version, then CAS (Seam 7).
// A concurrent modification between read and CAS yields ErrVersionConflict
// (acceptance #3). Single attempt — retry is the caller's responsibility.
func (s *teamService) UpdateTask(ctx context.Context, req UpdateTaskRequest) (TeamTask, error) {
	if err := s.enabledGuard(); err != nil {
		return TeamTask{}, err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return TeamTask{}, fmt.Errorf("begin tx: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	current, err := s.tasks.GetTask(ctx, tx, req.TeamID, req.ID)
	if err != nil {
		return TeamTask{}, err
	}
	updated, err := s.tasks.UpdateTaskCAS(ctx, tx, UpdateTaskCASRequest{
		ID: req.ID, TeamID: req.TeamID, Status: req.Status,
		AssigneeMemberID: req.AssigneeMemberID, ResultSummary: req.ResultSummary,
		UpdatedAt: now(), ExpectedVersion: current.Version,
	})
	if err != nil {
		return TeamTask{}, err // ErrVersionConflict propagates as-is
	}
	if err := tx.Commit(); err != nil {
		return TeamTask{}, fmt.Errorf("commit: %w", err)
	}
	return updated, nil
}

func (s *teamService) ClaimNextTask(ctx context.Context, req ClaimNextTaskRequest) (TeamTask, error) {
	if err := s.enabledGuard(); err != nil {
		return TeamTask{}, err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return TeamTask{}, fmt.Errorf("begin tx: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	task, err := s.tasks.ClaimNextTask(ctx, tx, req)
	if err != nil {
		return TeamTask{}, err // ErrNoTaskAvailable propagates
	}
	if err := tx.Commit(); err != nil {
		return TeamTask{}, fmt.Errorf("commit: %w", err)
	}
	return task, nil
}

func (s *teamService) ListTasks(ctx context.Context, teamID string, filter TaskFilter) ([]TeamTask, error) {
	if err := s.enabledGuard(); err != nil {
		return nil, err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("begin tx: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	tasks, err := s.tasks.ListTasks(ctx, tx, teamID)
	if err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("commit: %w", err)
	}
	return tasks, nil
}

// --- run methods ---

// StartRun inserts a run and immediately transitions it to 'running' — a run,
// once started, is running (FinishRun/MarkRunTerminal's store guard is
// `WHERE status='running'`; a queued run would never transition). InsertRun
// hardcodes the initial row status to 'queued' (M3-04), so StartRun flips it to
// 'running' in the same tx via a direct UPDATE, then returns the running row.
func (s *teamService) StartRun(ctx context.Context, req StartRunRequest) (TeamRun, error) {
	if err := s.enabledGuard(); err != nil {
		return TeamRun{}, err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return TeamRun{}, fmt.Errorf("begin tx: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	ts := now()
	run, err := s.runs.StartRun(ctx, tx, TeamRun{
		ID: uuid.New().String(), TeamID: req.TeamID, MemberID: req.MemberID,
		TaskID: req.TaskID, SessionID: req.SessionID, Status: RunRunning,
		HeartbeatAt: &ts,
	})
	if err != nil {
		return TeamRun{}, err
	}
	// InsertRun hardcodes status='queued'; flip to 'running' so the row
	// matches the Service's StartRun contract and FinishRun/MarkRunTerminal's
	// `WHERE status='running'` guard will match.
	if _, err := tx.ExecContext(ctx, `UPDATE team_runs SET status = 'running' WHERE id = ? AND team_id = ?`, run.ID, run.TeamID); err != nil {
		return TeamRun{}, fmt.Errorf("set run running: %w", err)
	}
	run.Status = RunRunning
	seq, err := s.events.NextEventSeq(ctx, tx, req.TeamID, ts)
	if err != nil {
		return TeamRun{}, fmt.Errorf("alloc event seq: %w", err)
	}
	if err := s.events.AppendEvent(ctx, tx, TeamEvent{
		Seq: seq, ID: uuid.New().String(), TeamID: req.TeamID, RunID: &run.ID,
		EventType: "run.started", EntityType: "run", EntityID: run.ID, CreatedAt: ts,
	}); err != nil {
		return TeamRun{}, fmt.Errorf("append event: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return TeamRun{}, fmt.Errorf("commit: %w", err)
	}
	return run, nil
}

func (s *teamService) HeartbeatRun(ctx context.Context, runID string) error {
	if err := s.enabledGuard(); err != nil {
		return err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	if err := s.runs.UpdateRunHeartbeat(ctx, tx, runID, now()); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *teamService) FinishRun(ctx context.Context, req FinishRunRequest) error {
	if err := s.enabledGuard(); err != nil {
		return err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	if err := s.runs.FinishRun(ctx, tx, req.TeamID, req.RunID, req.PromptTokens, req.CompletionTokens, req.CostMicros, now()); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *teamService) MarkRunTerminal(ctx context.Context, req MarkRunTerminalRequest) error {
	if err := s.enabledGuard(); err != nil {
		return err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	expected := req.ExpectedStatus
	if expected == "" {
		expected = RunRunning // backward-compatible default (M4-03 behaviour)
	}
	if err := s.runs.MarkRunTerminal(ctx, tx, req.TeamID, req.RunID, req.Status, expected, req.Error, req.UsageStatus, now()); err != nil {
		return err
	}
	return tx.Commit()
}

// FindStaleRuns returns runs whose heartbeat_at is before cutoff AND status is
// in ('running','waiting_permission','queued'). Surfaced from RunStore to the
// Service interface for M4-03's RecoverStaleRuns (Seam 9).
func (s *teamService) FindStaleRuns(ctx context.Context, cutoff time.Time) ([]TeamRun, error) {
	if err := s.enabledGuard(); err != nil {
		return nil, err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("begin tx: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	runs, err := s.runs.FindStaleRuns(ctx, tx, cutoff)
	if err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("commit: %w", err)
	}
	return runs, nil
}

// GetTeamCost reads all runs for a team (via FindStaleRuns proxy with a
// far-future cutoff as a "list all" workaround), converts them to UsageRecords,
// aggregates by member, then rolls up into a TeamCost. Same read pattern as
// buildSnapshot/findRunsForSnapshot — no new store method required.
// M4-12 Cost Accounting.
func (s *teamService) GetTeamCost(ctx context.Context, teamID string) (TeamCost, error) {
	if err := s.enabledGuard(); err != nil {
		return TeamCost{}, err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return TeamCost{}, fmt.Errorf("begin tx: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	all, err := s.runs.FindStaleRuns(ctx, tx, now().Add(100*365*24*time.Hour))
	if err != nil {
		return TeamCost{}, err
	}
	// Filter to this team's runs only.
	teamRuns := make([]TeamRun, 0)
	for _, r := range all {
		if r.TeamID == teamID {
			teamRuns = append(teamRuns, r)
		}
	}
	if err := tx.Commit(); err != nil {
		return TeamCost{}, fmt.Errorf("commit: %w", err)
	}

	members := AggregateByMember(teamRuns)
	return AggregateTeamCost(teamID, members), nil
}

// --- session link methods ---

// RecoverMemberSession returns the most recent session_id linked to a member.
// Returns ("", nil) if no link exists (member has no linked session yet).
// M4-10 Session Links.
func (s *teamService) RecoverMemberSession(ctx context.Context, teamID, memberID string) (string, error) {
	if err := s.enabledGuard(); err != nil {
		return "", err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return "", fmt.Errorf("begin tx: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	link, err := s.links.GetSessionLinkByMember(ctx, tx, teamID, memberID)
	if err != nil {
		return "", err
	}
	if err := tx.Commit(); err != nil {
		return "", fmt.Errorf("commit: %w", err)
	}
	return link.SessionID, nil
}

// --- event + snapshot + debug methods ---

func (s *teamService) ListEventsAfter(ctx context.Context, teamID string, afterSeq int64, limit int) ([]TeamEvent, error) {
	if err := s.enabledGuard(); err != nil {
		return nil, err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("begin tx: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	events, err := s.events.ListEventsAfter(ctx, tx, teamID, afterSeq, limit)
	if err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("commit: %w", err)
	}
	return events, nil
}

// DebugSnapshot returns the team snapshot + the last 50 events + last 50 audit
// rows (Seam 2, acceptance #2). All reads in one tx.
func (s *teamService) DebugSnapshot(ctx context.Context, teamID string) (DebugSnapshot, error) {
	if err := s.enabledGuard(); err != nil {
		return DebugSnapshot{}, err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return DebugSnapshot{}, fmt.Errorf("begin tx: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	team, err := s.teams.GetTeam(ctx, tx, teamID)
	if err != nil {
		return DebugSnapshot{}, err
	}
	members, err := s.members.ListMembers(ctx, tx, teamID)
	if err != nil {
		return DebugSnapshot{}, err
	}
	tasks, err := s.tasks.ListTasks(ctx, tx, teamID)
	if err != nil {
		return DebugSnapshot{}, err
	}
	runs, err := s.findRunsForSnapshot(ctx, tx, teamID)
	if err != nil {
		return DebugSnapshot{}, err
	}
	events, err := s.events.ListEventsAfter(ctx, tx, teamID, 0, 50)
	if err != nil {
		return DebugSnapshot{}, err
	}
	audit, err := s.audits.ListAudit(ctx, tx, teamID, 50)
	if err != nil {
		return DebugSnapshot{}, err
	}
	var cost int64
	for _, r := range runs {
		if r.CostMicros != nil {
			cost += *r.CostMicros
		}
	}
	if err := tx.Commit(); err != nil {
		return DebugSnapshot{}, fmt.Errorf("commit: %w", err)
	}
	return DebugSnapshot{
		TeamSnapshot: TeamSnapshot{Team: team, Members: members, Tasks: tasks, Runs: runs, Cost: cost},
		Events:       events,
		Audit:        audit,
	}, nil
}
