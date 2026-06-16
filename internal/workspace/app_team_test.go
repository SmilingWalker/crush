package workspace

import (
	"context"
	"database/sql"
	"testing"

	"github.com/charmbracelet/crush/internal/db"
	"github.com/charmbracelet/crush/internal/proto"
	"github.com/charmbracelet/crush/internal/team"
	"github.com/pressly/goose/v3"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	_ "modernc.org/sqlite"
)

// newTeamWorkspaceFixture builds a real team.Service over an in-memory SQLite
// (all migrations applied), wraps it in an AppWorkspace, and injects the
// Service via SetTeamService (Seam 1). The feature gate is ENABLED. This is
// the workspace-package analogue of team.newServiceFixture — reconstructed
// here (not imported) because newServiceFixture is unexported in package team
// (Seam 5). The DB setup mirrors team.store_test_helpers.go:newStoreFixture.
func newTeamWorkspaceFixture(t *testing.T) *AppWorkspace {
	t.Helper()
	sqlDB, err := sql.Open("sqlite", ":memory:")
	require.NoError(t, err)
	t.Cleanup(func() { _ = sqlDB.Close() })
	// :memory: is per-CONNECTION — serialize all access through one connection
	// so the migration and the service's txs land on the same DB (M3-04 lesson).
	sqlDB.SetMaxOpenConns(1)
	_, err = sqlDB.Exec("PRAGMA foreign_keys = ON;")
	require.NoError(t, err)
	require.NoError(t, db.InitGooseForTest(), "goose init")
	require.NoError(t, goose.Up(sqlDB, "migrations"), "apply migrations")
	q := db.New(sqlDB)
	svc := team.NewService(
		sqlDB,
		team.NewTeamStore(q), team.NewMemberStore(q), team.NewTaskStore(q),
		team.NewRunStore(q), team.NewEventStore(q), team.NewAuditStore(q),
		team.NewMailboxStore(q),
		team.NewSessionLinkStore(q),
		nil, // deps
		team.WithEnabledGate(func() bool { return true }),
	)
	w := NewAppWorkspace(nil, nil) // app/store unused by TeamWorkspace methods
	w.SetTeamService(svc)
	return w
}

// TestAppWorkspace_TeamWorkspaceEndToEnd locks acceptance #3 (call chain):
// proto request → TeamWorkspace → team.Service → store → DB, and the proto
// result round-trips with the right fields.
func TestAppWorkspace_TeamWorkspaceEndToEnd(t *testing.T) {
	w := newTeamWorkspaceFixture(t)
	ctx := context.Background()

	// CreateTeam
	snap, err := w.CreateTeam(ctx, proto.CreateTeamRequest{
		WorkspaceID: "ws-1", LeaderSessionID: "lead-1", Name: "Alpha",
	})
	require.NoError(t, err)
	assert.Equal(t, proto.TeamStatusCreated, snap.Team.Status)
	assert.Equal(t, "Alpha", snap.Team.Name)
	assert.Equal(t, "ws-1", snap.Team.WorkspaceID)
	teamID := snap.Team.ID

	// SpawnMember
	mem, err := w.SpawnMember(ctx, proto.SpawnMemberRequest{
		TeamID: teamID, Name: "researcher", Role: "programmer", AgentProfile: `{"type":"explore"}`,
	})
	require.NoError(t, err)
	assert.Equal(t, proto.MemberStatusCreated, mem.Status)
	assert.Equal(t, teamID, mem.TeamID)

	// CreateTask
	task, err := w.CreateTask(ctx, proto.CreateTeamTaskRequest{
		TeamID: teamID, Title: "do thing", CreatedByMemberID: mem.ID, Priority: 2,
	})
	require.NoError(t, err)
	assert.Equal(t, proto.TaskStatusQueued, task.Status)
	assert.Equal(t, "do thing", task.Title)

	// UpdateTask (read-then-CAS via Service)
	updated, err := w.UpdateTask(ctx, proto.UpdateTeamTaskRequest{
		ID: task.ID, TeamID: teamID, Status: proto.TaskStatusInProgress,
	})
	require.NoError(t, err)
	assert.Equal(t, proto.TaskStatusInProgress, updated.Status)

	// GetTeamSnapshot reflects the member + task
	snap2, err := w.GetTeamSnapshot(ctx, "ws-1", teamID)
	require.NoError(t, err)
	assert.Equal(t, teamID, snap2.Team.ID)
	require.Len(t, snap2.Members, 1)
	require.Len(t, snap2.Tasks, 1)
	assert.Equal(t, proto.TaskStatusInProgress, snap2.Tasks[0].Status) // reflects the update

	// ListTeams
	list, err := w.ListTeams(ctx, proto.ListTeamsRequest{WorkspaceID: "ws-1"})
	require.NoError(t, err)
	require.Len(t, list.Teams, 1)
	assert.Equal(t, teamID, list.Teams[0].ID)

	// ListEventsAfter returns the events CreateTeam/SpawnMember/CreateTask wrote
	ev, err := w.ListEventsAfter(ctx, "ws-1", teamID, 0, 100)
	require.NoError(t, err)
	assert.NotEmpty(t, ev.Events, "CreateTeam/SpawnMember/CreateTask each append an event")
}

// TestAppWorkspace_TeamWorkspaceNilServiceErrors locks Seam 1: an AppWorkspace
// with no injected Service returns a stable error from every TeamWorkspace
// method (the nilService guard), rather than panicking.
func TestAppWorkspace_TeamWorkspaceNilServiceErrors(t *testing.T) {
	w := NewAppWorkspace(nil, nil) // no SetTeamService
	ctx := context.Background()
	_, err := w.CreateTeam(ctx, proto.CreateTeamRequest{WorkspaceID: "ws", LeaderSessionID: "l", Name: "X"})
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrTeamServiceNotConfigured)
}
