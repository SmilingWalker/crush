// team_test.go covers the M3-07 client SDK methods via round-trip tests against
// an httptest.Server whose handlers echo canned proto JSON, plus error-path
// tests (bad status codes, connection failures).
package client

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"

	"github.com/charmbracelet/crush/internal/proto"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// newTeamTestClient creates a Client pointed at the test server.
func newTeamTestClient(t *testing.T, srv *httptest.Server) *Client {
	t.Helper()
	u, err := url.Parse(srv.URL)
	require.NoError(t, err)
	c, err := NewClient(t.TempDir(), "tcp", u.Host)
	require.NoError(t, err)
	return c
}

func TestClient_CreateTeam_Success(t *testing.T) {
	t.Parallel()
	expected := proto.TeamSnapshot{Team: proto.Team{ID: "t1", Name: "alpha"}}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, http.MethodPost, r.Method)
		assert.Contains(t, r.URL.Path, "/workspaces/ws1/teams")
		w.WriteHeader(http.StatusCreated)
		json.NewEncoder(w).Encode(expected)
	}))
	defer srv.Close()

	c := newTeamTestClient(t, srv)
	snap, err := c.CreateTeam(context.Background(), "ws1", proto.CreateTeamRequest{Name: "alpha"})
	require.NoError(t, err)
	assert.Equal(t, "t1", snap.Team.ID)
	assert.Equal(t, "alpha", snap.Team.Name)
}

func TestClient_CreateTeam_Error(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
	}))
	defer srv.Close()

	c := newTeamTestClient(t, srv)
	_, err := c.CreateTeam(context.Background(), "ws1", proto.CreateTeamRequest{Name: "x"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "status code 403")
}

func TestClient_ListTeams_Success(t *testing.T) {
	t.Parallel()
	expected := proto.ListTeamsResponse{Teams: []proto.Team{{ID: "t1", Name: "alpha"}}}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, http.MethodGet, r.Method)
		assert.Contains(t, r.URL.Path, "/workspaces/ws1/teams")
		json.NewEncoder(w).Encode(expected)
	}))
	defer srv.Close()

	c := newTeamTestClient(t, srv)
	resp, err := c.ListTeams(context.Background(), "ws1", false)
	require.NoError(t, err)
	assert.Len(t, resp.Teams, 1)
	assert.Equal(t, "alpha", resp.Teams[0].Name)
}

func TestClient_ListTeams_WithArchived(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.True(t, r.URL.Query().Has("include_archived"))
		assert.Equal(t, "true", r.URL.Query().Get("include_archived"))
		json.NewEncoder(w).Encode(proto.ListTeamsResponse{})
	}))
	defer srv.Close()

	c := newTeamTestClient(t, srv)
	_, err := c.ListTeams(context.Background(), "ws1", true)
	require.NoError(t, err)
}

func TestClient_ListTeams_Error(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()

	c := newTeamTestClient(t, srv)
	_, err := c.ListTeams(context.Background(), "ws1", false)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "status code 404")
}

func TestClient_GetTeamSnapshot_Success(t *testing.T) {
	t.Parallel()
	expected := proto.TeamSnapshot{Team: proto.Team{ID: "t1", Name: "alpha"}}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Contains(t, r.URL.Path, "/workspaces/ws1/teams/t1")
		json.NewEncoder(w).Encode(expected)
	}))
	defer srv.Close()

	c := newTeamTestClient(t, srv)
	snap, err := c.GetTeamSnapshot(context.Background(), "ws1", "t1")
	require.NoError(t, err)
	assert.Equal(t, "alpha", snap.Team.Name)
}

func TestClient_GetTeamSnapshot_Error(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()

	c := newTeamTestClient(t, srv)
	_, err := c.GetTeamSnapshot(context.Background(), "ws1", "t1")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "status code 404")
}

func TestClient_SpawnMember_Success(t *testing.T) {
	t.Parallel()
	expected := proto.TeamMember{ID: "m1", Name: "coder", Role: "programmer"}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, http.MethodPost, r.Method)
		assert.Contains(t, r.URL.Path, "/workspaces/ws1/teams/t1/members")
		w.WriteHeader(http.StatusCreated)
		json.NewEncoder(w).Encode(expected)
	}))
	defer srv.Close()

	c := newTeamTestClient(t, srv)
	m, err := c.SpawnMember(context.Background(), "ws1", "t1", proto.SpawnMemberRequest{Name: "coder", Role: "programmer"})
	require.NoError(t, err)
	assert.Equal(t, "m1", m.ID)
	assert.Equal(t, "coder", m.Name)
}

func TestClient_SpawnMember_Error(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusConflict)
	}))
	defer srv.Close()

	c := newTeamTestClient(t, srv)
	_, err := c.SpawnMember(context.Background(), "ws1", "t1", proto.SpawnMemberRequest{})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "status code 409")
}

func TestClient_CreateTask_Success(t *testing.T) {
	t.Parallel()
	expected := proto.TeamTask{ID: "tk1", Title: "do work", Priority: 1}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, http.MethodPost, r.Method)
		assert.Contains(t, r.URL.Path, "/workspaces/ws1/teams/t1/tasks")
		w.WriteHeader(http.StatusCreated)
		json.NewEncoder(w).Encode(expected)
	}))
	defer srv.Close()

	c := newTeamTestClient(t, srv)
	task, err := c.CreateTask(context.Background(), "ws1", "t1", proto.CreateTeamTaskRequest{Title: "do work", Priority: 1})
	require.NoError(t, err)
	assert.Equal(t, "tk1", task.ID)
	assert.Equal(t, "do work", task.Title)
}

func TestClient_CreateTask_Error(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
	}))
	defer srv.Close()

	c := newTeamTestClient(t, srv)
	_, err := c.CreateTask(context.Background(), "ws1", "t1", proto.CreateTeamTaskRequest{})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "status code 403")
}

func TestClient_UpdateTask_Success(t *testing.T) {
	t.Parallel()
	doneMsg := "all done"
	expected := proto.TeamTask{ID: "tk1", Title: "do work", ResultSummary: &doneMsg}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, http.MethodPatch, r.Method)
		assert.Contains(t, r.URL.Path, "/workspaces/ws1/teams/t1/tasks/tk1")
		json.NewEncoder(w).Encode(expected)
	}))
	defer srv.Close()

	c := newTeamTestClient(t, srv)
	task, err := c.UpdateTask(context.Background(), "ws1", "t1", "tk1", proto.UpdateTeamTaskRequest{ResultSummary: &doneMsg})
	require.NoError(t, err)
	assert.Equal(t, "tk1", task.ID)
	require.NotNil(t, task.ResultSummary)
	assert.Equal(t, "all done", *task.ResultSummary)
}

func TestClient_UpdateTask_Error(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusConflict)
	}))
	defer srv.Close()

	c := newTeamTestClient(t, srv)
	_, err := c.UpdateTask(context.Background(), "ws1", "t1", "tk1", proto.UpdateTeamTaskRequest{})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "status code 409")
}

func TestClient_ListTeamEvents_Success(t *testing.T) {
	t.Parallel()
	expected := proto.TeamEventsResponse{Events: []proto.TeamEvent{{Seq: 1, EventType: "team.created"}}}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, http.MethodGet, r.Method)
		assert.Contains(t, r.URL.Path, "/workspaces/ws1/teams/t1/events")
		assert.Equal(t, "5", r.URL.Query().Get("after"))
		assert.Equal(t, "20", r.URL.Query().Get("limit"))
		json.NewEncoder(w).Encode(expected)
	}))
	defer srv.Close()

	c := newTeamTestClient(t, srv)
	resp, err := c.ListTeamEvents(context.Background(), "ws1", "t1", 5, 20)
	require.NoError(t, err)
	assert.Len(t, resp.Events, 1)
	assert.Equal(t, int64(1), resp.Events[0].Seq)
}

func TestClient_ListTeamEvents_Error(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()

	c := newTeamTestClient(t, srv)
	_, err := c.ListTeamEvents(context.Background(), "ws1", "t1", 0, 50)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "status code 404")
}
