// team_test.go covers the M3-07 team API handlers. Tests use a mix of
// lightweight controller-isolated tests (404/400/501) and a DB-backed
// integration path that exercises the full CreateTeam → GetTeamSnapshot →
// SpawnMember → CreateTask → UpdateTask → ListEventsAfter → ListTeams
// pipeline through the HTTP handlers.
package server

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/charmbracelet/crush/internal/app"
	"github.com/charmbracelet/crush/internal/backend"
	"github.com/charmbracelet/crush/internal/config"
	"github.com/charmbracelet/crush/internal/db"
	"github.com/charmbracelet/crush/internal/proto"
	"github.com/charmbracelet/crush/internal/team"
	"github.com/google/uuid"
	"github.com/pressly/goose/v3"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	_ "modernc.org/sqlite"
)

// newTeamTestService builds a team.Service over an in-memory SQLite DB with the
// feature gate ENABLED. Returns the raw sqlDB so the caller can close it.
func newTeamTestService(t *testing.T) (team.Service, *sql.DB) {
	t.Helper()
	sqlDB, err := sql.Open("sqlite", ":memory:")
	require.NoError(t, err)
	t.Cleanup(func() { _ = sqlDB.Close() })
	sqlDB.SetMaxOpenConns(1) // :memory: databases are per-connection
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
	return svc, sqlDB
}

// newTeamTestController builds a controllerV1 whose backend contains a synthetic
// workspace backed by a real app.App + team.Service + config.ConfigStore. The
// workspace ID and config store are returned so tests can reference them.
func newTeamTestController(t *testing.T) (*controllerV1, *backend.Workspace, *config.ConfigStore, func()) {
	t.Helper()

	svc, _ := newTeamTestService(t)

	// Build a minimal config store. Team handlers do not call Config() on the
	// AppWorkspace (only team methods), so a nil store would also work. We
	// provide one anyway so downstream methods that DO access config (e.g.
	// snapshot unmarshalling if extended later) don't panic on nil dereference.
	store, err := config.Load(t.TempDir(), t.TempDir(), false)
	require.NoError(t, err)

	ctx, cancel := context.WithCancel(context.Background())
	a := app.NewForTest(ctx)
	a.SetTeamServiceForTest(svc) // inject the real team service

	cleanup := func() {
		cancel()
		a.ShutdownForTest()
	}

	c := newTestController()

	// Build workspace the same way newE2EHarness does.
	ws := &backend.Workspace{
		ID:   uuid.New().String(),
		Path: t.TempDir(),
		App:  a,
		Cfg:  store,
	}
	backend.InsertWorkspaceForTest(c.backend, ws)

	return c, ws, store, cleanup
}

// requireJSONBody decodes the response body as a proto.Error and asserts
// the expected HTTP status code and that the message contains the needle.
func requireErrorBody(t *testing.T, rec *httptest.ResponseRecorder, status int, needle string) {
	t.Helper()
	require.Equal(t, status, rec.Code)
	var perr proto.Error
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &perr))
	assert.Contains(t, perr.Message, needle)
}

func TestPostWorkspaceTeams_NotFound(t *testing.T) {
	t.Parallel()
	c := newTestController()
	body, err := json.Marshal(proto.CreateTeamRequest{Name: "test"})
	require.NoError(t, err)
	req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/v1/workspaces/nonexistent/teams", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.SetPathValue("id", "nonexistent")
	rec := httptest.NewRecorder()
	c.handlePostWorkspaceTeams(rec, req)
	requireErrorBody(t, rec, http.StatusNotFound, "workspace")
}

func TestPostWorkspaceTeams_BadBody(t *testing.T) {
	t.Parallel()
	c, ws, _, cleanup := newTeamTestController(t)
	defer cleanup()

	req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/v1/workspaces/"+ws.ID+"/teams", bytes.NewReader([]byte("{")))
	req.Header.Set("Content-Type", "application/json")
	req.SetPathValue("id", ws.ID)
	rec := httptest.NewRecorder()
	c.handlePostWorkspaceTeams(rec, req)
	requireErrorBody(t, rec, http.StatusBadRequest, "decode")
}

func TestPostWorkspaceTeams_Success(t *testing.T) {
	t.Parallel()
	c, ws, _, cleanup := newTeamTestController(t)
	defer cleanup()

	body, err := json.Marshal(proto.CreateTeamRequest{Name: "new-team", Description: "desc"})
	require.NoError(t, err)
	req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/v1/workspaces/"+ws.ID+"/teams", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.SetPathValue("id", ws.ID)
	rec := httptest.NewRecorder()
	c.handlePostWorkspaceTeams(rec, req)

	require.Equal(t, http.StatusCreated, rec.Code)
	var snap proto.TeamSnapshot
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &snap))
	assert.Equal(t, "new-team", snap.Team.Name)
	assert.Equal(t, "desc", snap.Team.Description)
	assert.NotEmpty(t, snap.Team.ID)
}

func TestGetWorkspaceTeams_NotFound(t *testing.T) {
	t.Parallel()
	c := newTestController()
	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/v1/workspaces/nonexistent/teams", nil)
	req.SetPathValue("id", "nonexistent")
	rec := httptest.NewRecorder()
	c.handleGetWorkspaceTeams(rec, req)
	requireErrorBody(t, rec, http.StatusNotFound, "workspace")
}

func TestGetWorkspaceTeams_List(t *testing.T) {
	t.Parallel()
	c, ws, _, cleanup := newTeamTestController(t)
	defer cleanup()

	// Create a team first.
	body, err := json.Marshal(proto.CreateTeamRequest{Name: "alpha"})
	require.NoError(t, err)
	req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/v1/workspaces/"+ws.ID+"/teams", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.SetPathValue("id", ws.ID)
	rec := httptest.NewRecorder()
	c.handlePostWorkspaceTeams(rec, req)
	require.Equal(t, http.StatusCreated, rec.Code)

	// List teams.
	req2 := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/v1/workspaces/"+ws.ID+"/teams", nil)
	req2.SetPathValue("id", ws.ID)
	rec2 := httptest.NewRecorder()
	c.handleGetWorkspaceTeams(rec2, req2)

	require.Equal(t, http.StatusOK, rec2.Code)
	var resp proto.ListTeamsResponse
	require.NoError(t, json.Unmarshal(rec2.Body.Bytes(), &resp))
	assert.Len(t, resp.Teams, 1)
	assert.Equal(t, "alpha", resp.Teams[0].Name)
}

func TestGetWorkspaceTeam_NotFound(t *testing.T) {
	t.Parallel()
	c := newTestController()
	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/v1/workspaces/nonexistent/teams/t1", nil)
	req.SetPathValue("id", "nonexistent")
	req.SetPathValue("team_id", "t1")
	rec := httptest.NewRecorder()
	c.handleGetWorkspaceTeam(rec, req)
	requireErrorBody(t, rec, http.StatusNotFound, "workspace")
}

func TestGetWorkspaceTeam_Snapshot(t *testing.T) {
	t.Parallel()
	c, ws, _, cleanup := newTeamTestController(t)
	defer cleanup()

	// Create a team.
	body, err := json.Marshal(proto.CreateTeamRequest{Name: "alpha"})
	require.NoError(t, err)
	req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/v1/workspaces/"+ws.ID+"/teams", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.SetPathValue("id", ws.ID)
	rec := httptest.NewRecorder()
	c.handlePostWorkspaceTeams(rec, req)
	require.Equal(t, http.StatusCreated, rec.Code)
	var created proto.TeamSnapshot
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &created))

	// Get the snapshot.
	req2 := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/v1/workspaces/"+ws.ID+"/teams/"+created.Team.ID, nil)
	req2.SetPathValue("id", ws.ID)
	req2.SetPathValue("team_id", created.Team.ID)
	rec2 := httptest.NewRecorder()
	c.handleGetWorkspaceTeam(rec2, req2)

	require.Equal(t, http.StatusOK, rec2.Code)
	var snap proto.TeamSnapshot
	require.NoError(t, json.Unmarshal(rec2.Body.Bytes(), &snap))
	assert.Equal(t, created.Team.ID, snap.Team.ID)
	assert.Equal(t, "alpha", snap.Team.Name)
}

func TestGetWorkspaceTeamSnapshot_DelegatesToGetTeam(t *testing.T) {
	t.Parallel()
	c, ws, _, cleanup := newTeamTestController(t)
	defer cleanup()

	body, err := json.Marshal(proto.CreateTeamRequest{Name: "beta"})
	require.NoError(t, err)
	req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/v1/workspaces/"+ws.ID+"/teams", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.SetPathValue("id", ws.ID)
	rec := httptest.NewRecorder()
	c.handlePostWorkspaceTeams(rec, req)
	require.Equal(t, http.StatusCreated, rec.Code)
	var created proto.TeamSnapshot
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &created))

	// /snapshot route should return the same result.
	req2 := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/v1/workspaces/"+ws.ID+"/teams/"+created.Team.ID+"/snapshot", nil)
	req2.SetPathValue("id", ws.ID)
	req2.SetPathValue("team_id", created.Team.ID)
	rec2 := httptest.NewRecorder()
	c.handleGetWorkspaceTeamSnapshot(rec2, req2)

	require.Equal(t, http.StatusOK, rec2.Code)
	var snap proto.TeamSnapshot
	require.NoError(t, json.Unmarshal(rec2.Body.Bytes(), &snap))
	assert.Equal(t, created.Team.ID, snap.Team.ID)
}

func TestPostWorkspaceTeamMembers_NotFound(t *testing.T) {
	t.Parallel()
	c := newTestController()
	body, err := json.Marshal(proto.SpawnMemberRequest{Name: "m1", Role: "programmer"})
	require.NoError(t, err)
	req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/v1/workspaces/nonexistent/teams/t1/members", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.SetPathValue("id", "nonexistent")
	req.SetPathValue("team_id", "t1")
	rec := httptest.NewRecorder()
	c.handlePostWorkspaceTeamMembers(rec, req)
	requireErrorBody(t, rec, http.StatusNotFound, "workspace")
}

func TestPostWorkspaceTeamMembers_Success(t *testing.T) {
	t.Parallel()
	c, ws, _, cleanup := newTeamTestController(t)
	defer cleanup()

	// Create a team first.
	body, _ := json.Marshal(proto.CreateTeamRequest{Name: "alpha"})
	req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/v1/workspaces/"+ws.ID+"/teams", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.SetPathValue("id", ws.ID)
	rec := httptest.NewRecorder()
	c.handlePostWorkspaceTeams(rec, req)
	require.Equal(t, http.StatusCreated, rec.Code)
	var created proto.TeamSnapshot
	json.Unmarshal(rec.Body.Bytes(), &created)

	// Spawn a member.
	memberBody, _ := json.Marshal(proto.SpawnMemberRequest{Name: "coder", Role: "programmer", AgentProfile: "{}"})
	req2 := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/v1/workspaces/"+ws.ID+"/teams/"+created.Team.ID+"/members", bytes.NewReader(memberBody))
	req2.Header.Set("Content-Type", "application/json")
	req2.SetPathValue("id", ws.ID)
	req2.SetPathValue("team_id", created.Team.ID)
	rec2 := httptest.NewRecorder()
	c.handlePostWorkspaceTeamMembers(rec2, req2)

	require.Equal(t, http.StatusCreated, rec2.Code)
	var m proto.TeamMember
	require.NoError(t, json.Unmarshal(rec2.Body.Bytes(), &m))
	assert.Equal(t, "coder", m.Name)
	assert.Equal(t, "programmer", m.Role)
	assert.NotEmpty(t, m.ID)
}

func TestPostWorkspaceTeamMessages_501(t *testing.T) {
	t.Parallel()
	c := newTestController()
	req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/v1/workspaces/any/teams/t1/messages", nil)
	req.SetPathValue("id", "any")
	req.SetPathValue("team_id", "t1")
	rec := httptest.NewRecorder()
	c.handlePostWorkspaceTeamMessages(rec, req)
	requireErrorBody(t, rec, http.StatusNotImplemented, "M3b")
}

func TestPostWorkspaceTeamTasks_NotFound(t *testing.T) {
	t.Parallel()
	c := newTestController()
	body, err := json.Marshal(proto.CreateTeamTaskRequest{Title: "do work"})
	require.NoError(t, err)
	req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/v1/workspaces/nonexistent/teams/t1/tasks", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.SetPathValue("id", "nonexistent")
	req.SetPathValue("team_id", "t1")
	rec := httptest.NewRecorder()
	c.handlePostWorkspaceTeamTasks(rec, req)
	requireErrorBody(t, rec, http.StatusNotFound, "workspace")
}

func TestPostWorkspaceTeamTasks_Success(t *testing.T) {
	t.Parallel()
	c, ws, _, cleanup := newTeamTestController(t)
	defer cleanup()

	// Create a team first.
	body, _ := json.Marshal(proto.CreateTeamRequest{Name: "alpha"})
	req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/v1/workspaces/"+ws.ID+"/teams", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.SetPathValue("id", ws.ID)
	rec := httptest.NewRecorder()
	c.handlePostWorkspaceTeams(rec, req)
	require.Equal(t, http.StatusCreated, rec.Code)
	var created proto.TeamSnapshot
	json.Unmarshal(rec.Body.Bytes(), &created)

	// Create a task.
	taskBody, _ := json.Marshal(proto.CreateTeamTaskRequest{Title: "build thing", Priority: 2})
	req2 := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/v1/workspaces/"+ws.ID+"/teams/"+created.Team.ID+"/tasks", bytes.NewReader(taskBody))
	req2.Header.Set("Content-Type", "application/json")
	req2.SetPathValue("id", ws.ID)
	req2.SetPathValue("team_id", created.Team.ID)
	rec2 := httptest.NewRecorder()
	c.handlePostWorkspaceTeamTasks(rec2, req2)

	require.Equal(t, http.StatusCreated, rec2.Code)
	var task proto.TeamTask
	require.NoError(t, json.Unmarshal(rec2.Body.Bytes(), &task))
	assert.Equal(t, "build thing", task.Title)
	assert.Equal(t, 2, task.Priority)
	assert.NotEmpty(t, task.ID)
}

func TestPatchWorkspaceTeamTask_NotFound(t *testing.T) {
	t.Parallel()
	c := newTestController()
	resultSummary := "done"
	body, err := json.Marshal(proto.UpdateTeamTaskRequest{ResultSummary: &resultSummary})
	require.NoError(t, err)
	req := httptest.NewRequestWithContext(t.Context(), http.MethodPatch, "/v1/workspaces/nonexistent/teams/t1/tasks/tk1", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.SetPathValue("id", "nonexistent")
	req.SetPathValue("team_id", "t1")
	req.SetPathValue("task_id", "tk1")
	rec := httptest.NewRecorder()
	c.handlePatchWorkspaceTeamTask(rec, req)
	requireErrorBody(t, rec, http.StatusNotFound, "workspace")
}

func TestPatchWorkspaceTeamTask_Success(t *testing.T) {
	t.Parallel()
	c, ws, _, cleanup := newTeamTestController(t)
	defer cleanup()

	// Create team.
	body, _ := json.Marshal(proto.CreateTeamRequest{Name: "alpha"})
	req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/v1/workspaces/"+ws.ID+"/teams", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.SetPathValue("id", ws.ID)
	rec := httptest.NewRecorder()
	c.handlePostWorkspaceTeams(rec, req)
	require.Equal(t, http.StatusCreated, rec.Code)
	var created proto.TeamSnapshot
	json.Unmarshal(rec.Body.Bytes(), &created)

	// Create task.
	taskBody, _ := json.Marshal(proto.CreateTeamTaskRequest{Title: "do work"})
	req2 := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/v1/workspaces/"+ws.ID+"/teams/"+created.Team.ID+"/tasks", bytes.NewReader(taskBody))
	req2.Header.Set("Content-Type", "application/json")
	req2.SetPathValue("id", ws.ID)
	req2.SetPathValue("team_id", created.Team.ID)
	rec2 := httptest.NewRecorder()
	c.handlePostWorkspaceTeamTasks(rec2, req2)
	require.Equal(t, http.StatusCreated, rec2.Code)
	var task proto.TeamTask
	require.NoError(t, json.Unmarshal(rec2.Body.Bytes(), &task))

	// Update the task.
	doneMsg := "all done"
	updateBody, _ := json.Marshal(proto.UpdateTeamTaskRequest{ResultSummary: &doneMsg})
	req3 := httptest.NewRequestWithContext(t.Context(), http.MethodPatch, "/v1/workspaces/"+ws.ID+"/teams/"+created.Team.ID+"/tasks/"+task.ID, bytes.NewReader(updateBody))
	req3.Header.Set("Content-Type", "application/json")
	req3.SetPathValue("id", ws.ID)
	req3.SetPathValue("team_id", created.Team.ID)
	req3.SetPathValue("task_id", task.ID)
	rec3 := httptest.NewRecorder()
	c.handlePatchWorkspaceTeamTask(rec3, req3)

	require.Equal(t, http.StatusOK, rec3.Code)
	var updated proto.TeamTask
	require.NoError(t, json.Unmarshal(rec3.Body.Bytes(), &updated))
	assert.NotNil(t, updated.ResultSummary)
	assert.Equal(t, "all done", *updated.ResultSummary)
}

func TestGetWorkspaceTeamEvents_NotFound(t *testing.T) {
	t.Parallel()
	c := newTestController()
	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/v1/workspaces/nonexistent/teams/t1/events", nil)
	req.SetPathValue("id", "nonexistent")
	req.SetPathValue("team_id", "t1")
	rec := httptest.NewRecorder()
	c.handleGetWorkspaceTeamEvents(rec, req)
	requireErrorBody(t, rec, http.StatusNotFound, "workspace")
}

func TestGetWorkspaceTeamEvents_Success(t *testing.T) {
	t.Parallel()
	c, ws, _, cleanup := newTeamTestController(t)
	defer cleanup()

	// Create a team.
	body, _ := json.Marshal(proto.CreateTeamRequest{Name: "alpha"})
	req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/v1/workspaces/"+ws.ID+"/teams", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.SetPathValue("id", ws.ID)
	rec := httptest.NewRecorder()
	c.handlePostWorkspaceTeams(rec, req)
	require.Equal(t, http.StatusCreated, rec.Code)
	var created proto.TeamSnapshot
	json.Unmarshal(rec.Body.Bytes(), &created)

	// List events.
	req2 := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/v1/workspaces/"+ws.ID+"/teams/"+created.Team.ID+"/events?after=0&limit=10", nil)
	req2.SetPathValue("id", ws.ID)
	req2.SetPathValue("team_id", created.Team.ID)
	rec2 := httptest.NewRecorder()
	c.handleGetWorkspaceTeamEvents(rec2, req2)

	require.Equal(t, http.StatusOK, rec2.Code)
	var resp proto.TeamEventsResponse
	require.NoError(t, json.Unmarshal(rec2.Body.Bytes(), &resp))
	assert.NotEmpty(t, resp.Events, "team.created event should exist")
}
