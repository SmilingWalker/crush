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
// This file depends only on the standard library (time, added in Task 2 once
// the first struct lands). It does not import package db, sqlc output, or the
// M2 delegate types.

package team

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
