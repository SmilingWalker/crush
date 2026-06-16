package team

import (
	"context"
	"database/sql"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// newServiceFixture returns a TeamService wired over an in-memory SQLite (all
// migrations applied via db.New — sidesteps the ListNewFiles drift), with the
// feature gate ENABLED. Tests that need the gate off build their own service.
func newServiceFixture(t *testing.T) (Service, *sql.DB) {
	t.Helper()
	sqlDB, q := newStoreFixture(t) // M3-04 helper: SetMaxOpenConns(1) + goose.Up + db.New
	svc := NewService(
		sqlDB,
		NewTeamStore(q), NewMemberStore(q), NewTaskStore(q),
		NewRunStore(q), NewEventStore(q), NewAuditStore(q),
		NewMailboxStore(q), NewDependencyStore(q),
		WithEnabledGate(func() bool { return true }),
	)
	return svc, sqlDB
}

func TestService_FeatureGateOffReturnsErrFeatureDisabled(t *testing.T) {
	sqlDB, q := newStoreFixture(t)
	svc := NewService(
		sqlDB,
		NewTeamStore(q), NewMemberStore(q), NewTaskStore(q),
		NewRunStore(q), NewEventStore(q), NewAuditStore(q),
		NewMailboxStore(q), NewDependencyStore(q),
		// NO WithEnabledGate → default disabled (Seam 1 safe default)
	)
	_, err := svc.CreateTeam(context.Background(), CreateTeamRequest{WorkspaceID: "ws", LeaderSessionID: "l", Name: "X"})
	require.ErrorIs(t, err, ErrFeatureDisabled, "gate off → ErrFeatureDisabled")
}

func TestService_CreateTeam_AtomicTeamEventAudit(t *testing.T) {
	svc, sqlDB := newServiceFixture(t)
	snap, err := svc.CreateTeam(context.Background(), CreateTeamRequest{
		WorkspaceID: "ws-1", LeaderSessionID: "lead-1", Name: "Alpha",
	})
	require.NoError(t, err)
	assert.Equal(t, TeamCreated, snap.Team.Status)
	assert.Equal(t, "Alpha", snap.Team.Name)

	// Acceptance #1: one tx wrote team + event + audit. Verify via direct reads.
	var eventCount, auditCount int
	row := sqlDB.QueryRow(`SELECT count(*) FROM team_events WHERE team_id = ?`, snap.Team.ID)
	require.NoError(t, row.Scan(&eventCount))
	assert.Equal(t, 1, eventCount, "CreateTeam wrote exactly 1 event (team.created)")
	row = sqlDB.QueryRow(`SELECT count(*) FROM team_audit_events WHERE team_id = ?`, snap.Team.ID)
	require.NoError(t, row.Scan(&auditCount))
	assert.Equal(t, 1, auditCount, "CreateTeam wrote exactly 1 audit row")
}

func TestService_GetTeamSnapshot_AndDebugSnapshot(t *testing.T) {
	svc, _ := newServiceFixture(t)
	ctx := context.Background()
	snap, err := svc.CreateTeam(ctx, CreateTeamRequest{WorkspaceID: "ws-1", LeaderSessionID: "l", Name: "Alpha"})
	require.NoError(t, err)

	got, err := svc.GetTeamSnapshot(ctx, "ws-1", snap.Team.ID)
	require.NoError(t, err)
	assert.Equal(t, snap.Team.ID, got.Team.ID)
	assert.Empty(t, got.Members)

	// Spawn a member + task → snapshot reflects them.
	m, err := svc.SpawnMember(ctx, SpawnMemberRequest{TeamID: snap.Team.ID, Name: "coder", Role: "programmer", AgentProfile: "{}"})
	require.NoError(t, err)
	_, err = svc.CreateTask(ctx, CreateTaskRequest{TeamID: snap.Team.ID, Title: "do thing", CreatedByMemberID: m.ID})
	require.NoError(t, err)

	got, err = svc.GetTeamSnapshot(ctx, "ws-1", snap.Team.ID)
	require.NoError(t, err)
	assert.Len(t, got.Members, 1)
	assert.Len(t, got.Tasks, 1)

	// DebugSnapshot adds events + audit (acceptance #2).
	debug, err := svc.DebugSnapshot(ctx, snap.Team.ID)
	require.NoError(t, err)
	assert.NotEmpty(t, debug.Events, "DebugSnapshot includes recent events")
	assert.NotEmpty(t, debug.Audit, "DebugSnapshot includes recent audit")
}

func TestService_ListTeams_ArchiveTeam(t *testing.T) {
	svc, _ := newServiceFixture(t)
	ctx := context.Background()
	_, _ = svc.CreateTeam(ctx, CreateTeamRequest{WorkspaceID: "ws-1", LeaderSessionID: "l", Name: "A"})
	t2, _ := svc.CreateTeam(ctx, CreateTeamRequest{WorkspaceID: "ws-1", LeaderSessionID: "l", Name: "B"})

	teams, err := svc.ListTeams(ctx, "ws-1", TeamFilter{})
	require.NoError(t, err)
	assert.Len(t, teams, 2)

	require.NoError(t, svc.ArchiveTeam(ctx, ArchiveTeamRequest{ID: t2.Team.ID}))
	teams, err = svc.ListTeams(ctx, "ws-1", TeamFilter{})
	require.NoError(t, err)
	assert.Len(t, teams, 1, "archived team excluded from ListTeams")
}

// TestService_UpdateTask_HappyPath exercises the read-then-CAS flow end-to-end.
// (The real ErrVersionConflict is covered at the store layer in M3-04; at the
// Service layer it cannot be deterministically triggered because UpdateTask
// reads the current version each call and CASes on it within one tx — a real
// conflict requires concurrent modification, impossible under SetMaxOpenConns(1).
// TestService_UpdateTask_PropagatesErrVersionConflict below uses a stub store
// to verify the Service propagates the sentinel without swallowing.)
func TestService_UpdateTask_HappyPath(t *testing.T) {
	svc, _ := newServiceFixture(t)
	ctx := context.Background()
	snap, _ := svc.CreateTeam(ctx, CreateTeamRequest{WorkspaceID: "ws", LeaderSessionID: "l", Name: "T"})
	task, err := svc.CreateTask(ctx, CreateTaskRequest{TeamID: snap.Team.ID, Title: "x", CreatedByMemberID: "lead"})
	require.NoError(t, err)

	// Happy path: UpdateTask reads v1, CASes on v1 → succeeds, version → 2.
	updated, err := svc.UpdateTask(ctx, UpdateTaskRequest{ID: task.ID, TeamID: snap.Team.ID, Status: TaskInProgress})
	require.NoError(t, err)
	assert.Equal(t, 2, updated.Version)
	assert.Equal(t, TaskInProgress, updated.Status)
}

// stubConflictTaskStore is a TaskStore whose UpdateTaskCAS always returns
// ErrVersionConflict. Used to verify the Service propagates the sentinel.
type stubConflictTaskStore struct {
	TaskStore
	conflictErr error
}

func (s *stubConflictTaskStore) GetTask(ctx context.Context, tx *sql.Tx, teamID, taskID string) (TeamTask, error) {
	return TeamTask{ID: taskID, TeamID: teamID, Version: 1}, nil
}

func (s *stubConflictTaskStore) UpdateTaskCAS(ctx context.Context, tx *sql.Tx, req UpdateTaskCASRequest) (TeamTask, error) {
	return TeamTask{}, s.conflictErr
}

// TestService_UpdateTask_PropagatesErrVersionConflict verifies the Service
// surfaces ErrVersionConflict from the store CAS unchanged (acceptance #3).
// A stub store forces the conflict deterministically (the real concurrent
// conflict is covered at the M3-04 store layer; at the Service layer under
// SetMaxOpenConns(1) it can't be naturally triggered — see TestService_UpdateTask_HappyPath).
func TestService_UpdateTask_PropagatesErrVersionConflict(t *testing.T) {
	sqlDB, q := newStoreFixture(t)
	svc := NewService(
		sqlDB,
		NewTeamStore(q), NewMemberStore(q), &stubConflictTaskStore{conflictErr: ErrVersionConflict},
		NewRunStore(q), NewEventStore(q), NewAuditStore(q),
		NewMailboxStore(q), NewDependencyStore(q),
		WithEnabledGate(func() bool { return true }),
	)
	_, err := svc.UpdateTask(context.Background(), UpdateTaskRequest{ID: "tk1", TeamID: "t1", Status: TaskInProgress})
	require.ErrorIs(t, err, ErrVersionConflict, "Service propagates ErrVersionConflict from the store CAS")
}

func TestService_ClaimNextTask_AndStartRun_FinishRun(t *testing.T) {
	svc, _ := newServiceFixture(t)
	ctx := context.Background()
	snap, _ := svc.CreateTeam(ctx, CreateTeamRequest{WorkspaceID: "ws", LeaderSessionID: "l", Name: "T"})
	m, err := svc.SpawnMember(ctx, SpawnMemberRequest{TeamID: snap.Team.ID, Name: "coder", Role: "programmer", AgentProfile: "{}"})
	require.NoError(t, err)
	_, err = svc.CreateTask(ctx, CreateTaskRequest{TeamID: snap.Team.ID, Title: "task", CreatedByMemberID: m.ID, Priority: 5})
	require.NoError(t, err)

	// Claim it.
	claimed, err := svc.ClaimNextTask(ctx, ClaimNextTaskRequest{TeamID: snap.Team.ID, AssigneeMemberID: m.ID, UpdatedAt: time.UnixMilli(1)})
	require.NoError(t, err)
	assert.Equal(t, TaskAssigned, claimed.Status)

	// Claim again → ErrNoTaskAvailable.
	_, err = svc.ClaimNextTask(ctx, ClaimNextTaskRequest{TeamID: snap.Team.ID, AssigneeMemberID: m.ID, UpdatedAt: time.UnixMilli(2)})
	require.ErrorIs(t, err, ErrNoTaskAvailable)

	// StartRun (now RunRunning per review fix) → FinishRun guards on 'running'
	// and actually completes (not a no-op).
	run, err := svc.StartRun(ctx, StartRunRequest{TeamID: snap.Team.ID, MemberID: m.ID, SessionID: "sess-1"})
	require.NoError(t, err)
	assert.Equal(t, RunRunning, run.Status, "StartRun sets RunRunning (review fix)")

	require.NoError(t, svc.HeartbeatRun(ctx, run.ID))
	require.NoError(t, svc.FinishRun(ctx, FinishRunRequest{TeamID: snap.Team.ID, RunID: run.ID, PromptTokens: 100, CompletionTokens: 50, CostMicros: 7}))
}

func TestService_UpdateMemberState_AppendsEvent(t *testing.T) {
	svc, sqlDB := newServiceFixture(t)
	ctx := context.Background()
	snap, _ := svc.CreateTeam(ctx, CreateTeamRequest{WorkspaceID: "ws", LeaderSessionID: "l", Name: "T"})
	m, err := svc.SpawnMember(ctx, SpawnMemberRequest{TeamID: snap.Team.ID, Name: "coder", Role: "programmer", AgentProfile: "{}"})
	require.NoError(t, err)

	// SpawnMember wrote 1 event (member.spawned). UpdateMemberState should add
	// a member.updated event (review fix: event-trail consistency).
	updated, err := svc.UpdateMemberState(ctx, UpdateMemberStateRequest{ID: m.ID, TeamID: snap.Team.ID, Status: MemberIdle})
	require.NoError(t, err)
	assert.Equal(t, MemberIdle, updated.Status)

	var eventCount int
	row := sqlDB.QueryRow(`SELECT count(*) FROM team_events WHERE team_id = ? AND event_type = 'member.updated'`, snap.Team.ID)
	require.NoError(t, row.Scan(&eventCount))
	assert.Equal(t, 1, eventCount, "UpdateMemberState appends a member.updated event")
}

func TestService_ListEventsAfter(t *testing.T) {
	svc, _ := newServiceFixture(t)
	ctx := context.Background()
	snap, _ := svc.CreateTeam(ctx, CreateTeamRequest{WorkspaceID: "ws", LeaderSessionID: "l", Name: "T"})

	// CreateTeam wrote 1 event at seq 2. ListEventsAfter(after=0) returns it.
	events, err := svc.ListEventsAfter(ctx, snap.Team.ID, 0, 50)
	require.NoError(t, err)
	assert.Len(t, events, 1)
	assert.Equal(t, "team.created", events[0].EventType)
}

func TestService_MarkRunTerminal(t *testing.T) {
	svc, _ := newServiceFixture(t)
	ctx := context.Background()
	snap, _ := svc.CreateTeam(ctx, CreateTeamRequest{WorkspaceID: "ws", LeaderSessionID: "l", Name: "T"})
	m, _ := svc.SpawnMember(ctx, SpawnMemberRequest{TeamID: snap.Team.ID, Name: "coder", Role: "programmer", AgentProfile: "{}"})
	run, err := svc.StartRun(ctx, StartRunRequest{TeamID: snap.Team.ID, MemberID: m.ID, SessionID: "sess-1"})
	require.NoError(t, err)
	require.Equal(t, RunRunning, run.Status)

	// MarkRunTerminal guards on status='running' (StartRun set RunRunning) → matches.
	require.NoError(t, svc.MarkRunTerminal(ctx, MarkRunTerminalRequest{TeamID: snap.Team.ID, RunID: run.ID, Status: RunFailed, Error: "boom", UsageStatus: "partial"}))
}
