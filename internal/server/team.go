// team.go implements the M3-07 team-data-domain HTTP routes on *controllerV1.
// Each handler resolves the workspace, builds a TeamWorkspace from it, and
// delegates. Errors flow through handleError (team sentinels mapped in
// proto.go). The messages route is a 501 stub (mailbox deferred to M3b).
package server

import (
	"encoding/json"
	"net/http"
	"strconv"

	"github.com/charmbracelet/crush/internal/proto"
	"github.com/charmbracelet/crush/internal/workspace"
)

// teamWS resolves the workspace from the request path and wraps it in a
// TeamWorkspace backed by the workspace's team.Service (M3-07 Seam 1). Returns
// nil + writes an error response if the workspace is not found or the team
// service has not been configured.
func (c *controllerV1) teamWS(w http.ResponseWriter, r *http.Request) (workspace.TeamWorkspace, bool) {
	id := r.PathValue("id")
	ws, err := c.backend.GetWorkspace(id)
	if err != nil {
		c.handleError(w, r, err)
		return nil, false
	}
	wsTeam := workspace.NewAppWorkspace(ws.App, ws.Cfg)
	wsTeam.SetTeamService(ws.App.TeamService())
	return wsTeam, true
}

// handlePostWorkspaceTeams creates a team. Acceptance #1: returns 201.
func (c *controllerV1) handlePostWorkspaceTeams(w http.ResponseWriter, r *http.Request) {
	tw, ok := c.teamWS(w, r)
	if !ok {
		return
	}
	var req proto.CreateTeamRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		jsonError(w, http.StatusBadRequest, "failed to decode request")
		return
	}
	req.WorkspaceID = r.PathValue("id")
	snap, err := tw.CreateTeam(r.Context(), req)
	if err != nil {
		c.handleError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusCreated)
	jsonEncode(w, snap)
}

// handleGetWorkspaceTeams lists teams.
func (c *controllerV1) handleGetWorkspaceTeams(w http.ResponseWriter, r *http.Request) {
	tw, ok := c.teamWS(w, r)
	if !ok {
		return
	}
	resp, err := tw.ListTeams(r.Context(), proto.ListTeamsRequest{
		WorkspaceID:     r.PathValue("id"),
		IncludeArchived: r.URL.Query().Has("include_archived"),
	})
	if err != nil {
		c.handleError(w, r, err)
		return
	}
	jsonEncode(w, resp)
}

// handleGetWorkspaceTeam returns a team snapshot.
func (c *controllerV1) handleGetWorkspaceTeam(w http.ResponseWriter, r *http.Request) {
	tw, ok := c.teamWS(w, r)
	if !ok {
		return
	}
	snap, err := tw.GetTeamSnapshot(r.Context(), r.PathValue("id"), r.PathValue("team_id"))
	if err != nil {
		c.handleError(w, r, err)
		return
	}
	jsonEncode(w, snap)
}

// handleGetWorkspaceTeamSnapshot serves the explicit /snapshot route; it is
// the same operation as GetTeam (master doc: the per-team GET also returns
// the full snapshot).
func (c *controllerV1) handleGetWorkspaceTeamSnapshot(w http.ResponseWriter, r *http.Request) {
	c.handleGetWorkspaceTeam(w, r)
}

// handlePostWorkspaceTeamMembers spawns a member in the team.
func (c *controllerV1) handlePostWorkspaceTeamMembers(w http.ResponseWriter, r *http.Request) {
	tw, ok := c.teamWS(w, r)
	if !ok {
		return
	}
	var req proto.SpawnMemberRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		jsonError(w, http.StatusBadRequest, "failed to decode request")
		return
	}
	req.TeamID = r.PathValue("team_id")
	m, err := tw.SpawnMember(r.Context(), req)
	if err != nil {
		c.handleError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusCreated)
	jsonEncode(w, m)
}

// handlePostWorkspaceTeamMessages is a 501 stub — mailbox is deferred to M3b
// (Seam 4). The route is registered so the surface is stable for clients, but
// no real implementation exists yet.
func (c *controllerV1) handlePostWorkspaceTeamMessages(w http.ResponseWriter, r *http.Request) {
	jsonError(w, http.StatusNotImplemented, "team mailbox not yet implemented (M3b)")
}

// handlePostWorkspaceTeamTasks creates a task in the team.
func (c *controllerV1) handlePostWorkspaceTeamTasks(w http.ResponseWriter, r *http.Request) {
	tw, ok := c.teamWS(w, r)
	if !ok {
		return
	}
	var req proto.CreateTeamTaskRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		jsonError(w, http.StatusBadRequest, "failed to decode request")
		return
	}
	req.TeamID = r.PathValue("team_id")
	task, err := tw.CreateTask(r.Context(), req)
	if err != nil {
		c.handleError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusCreated)
	jsonEncode(w, task)
}

// handlePatchWorkspaceTeamTask updates a task (read-then-CAS).
func (c *controllerV1) handlePatchWorkspaceTeamTask(w http.ResponseWriter, r *http.Request) {
	tw, ok := c.teamWS(w, r)
	if !ok {
		return
	}
	var req proto.UpdateTeamTaskRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		jsonError(w, http.StatusBadRequest, "failed to decode request")
		return
	}
	req.TeamID = r.PathValue("team_id")
	req.ID = r.PathValue("task_id")
	task, err := tw.UpdateTask(r.Context(), req)
	if err != nil {
		c.handleError(w, r, err)
		return
	}
	jsonEncode(w, task)
}

// handleGetWorkspaceTeamEvents lists events after a seq cursor.
func (c *controllerV1) handleGetWorkspaceTeamEvents(w http.ResponseWriter, r *http.Request) {
	tw, ok := c.teamWS(w, r)
	if !ok {
		return
	}
	after, _ := strconv.ParseInt(r.URL.Query().Get("after"), 10, 64)
	limit := 50
	if l := r.URL.Query().Get("limit"); l != "" {
		if n, err := strconv.Atoi(l); err == nil && n > 0 {
			limit = n
		}
	}
	resp, err := tw.ListEventsAfter(r.Context(), r.PathValue("id"), r.PathValue("team_id"), after, limit)
	if err != nil {
		c.handleError(w, r, err)
		return
	}
	jsonEncode(w, resp)
}
