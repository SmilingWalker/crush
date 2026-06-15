// team.go adds the M3-07 client SDK methods for the team-data-domain routes.
// Each method builds the path from the workspace/team IDs, sends the request,
// and decodes the proto response. Errors follow the existing client convention
// (fmt.Errorf with status code); the server's handleError maps domain sentinels
// to HTTP statuses.
package client

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strconv"

	"github.com/charmbracelet/crush/internal/proto"
)

// CreateTeam sends a CreateTeam request and returns the resulting team snapshot.
// The server returns 201 on success.
func (c *Client) CreateTeam(ctx context.Context, workspaceID string, req proto.CreateTeamRequest) (*proto.TeamSnapshot, error) {
	rsp, err := c.post(ctx, fmt.Sprintf("/workspaces/%s/teams", workspaceID), nil, jsonBody(req), nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create team: %w", err)
	}
	defer rsp.Body.Close()
	if rsp.StatusCode != http.StatusCreated {
		return nil, fmt.Errorf("failed to create team: status code %d", rsp.StatusCode)
	}
	var snap proto.TeamSnapshot
	if err := json.NewDecoder(rsp.Body).Decode(&snap); err != nil {
		return nil, fmt.Errorf("failed to decode team snapshot: %w", err)
	}
	return &snap, nil
}

// ListTeams lists teams in a workspace. If includeArchived is true, archived
// teams are included in the response.
func (c *Client) ListTeams(ctx context.Context, workspaceID string, includeArchived bool) (*proto.ListTeamsResponse, error) {
	q := url.Values{}
	if includeArchived {
		q.Set("include_archived", "true")
	}
	rsp, err := c.get(ctx, fmt.Sprintf("/workspaces/%s/teams", workspaceID), q, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to list teams: %w", err)
	}
	defer rsp.Body.Close()
	if rsp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("failed to list teams: status code %d", rsp.StatusCode)
	}
	var resp proto.ListTeamsResponse
	if err := json.NewDecoder(rsp.Body).Decode(&resp); err != nil {
		return nil, fmt.Errorf("failed to decode team list: %w", err)
	}
	return &resp, nil
}

// GetTeamSnapshot returns the full team snapshot (team + members + tasks + runs).
func (c *Client) GetTeamSnapshot(ctx context.Context, workspaceID, teamID string) (*proto.TeamSnapshot, error) {
	rsp, err := c.get(ctx, fmt.Sprintf("/workspaces/%s/teams/%s", workspaceID, teamID), nil, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to get team snapshot: %w", err)
	}
	defer rsp.Body.Close()
	if rsp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("failed to get team snapshot: status code %d", rsp.StatusCode)
	}
	var snap proto.TeamSnapshot
	if err := json.NewDecoder(rsp.Body).Decode(&snap); err != nil {
		return nil, fmt.Errorf("failed to decode team snapshot: %w", err)
	}
	return &snap, nil
}

// SpawnMember spawns a new member into a team. The server returns 201 on success.
func (c *Client) SpawnMember(ctx context.Context, workspaceID, teamID string, req proto.SpawnMemberRequest) (*proto.TeamMember, error) {
	rsp, err := c.post(ctx, fmt.Sprintf("/workspaces/%s/teams/%s/members", workspaceID, teamID), nil, jsonBody(req), nil)
	if err != nil {
		return nil, fmt.Errorf("failed to spawn member: %w", err)
	}
	defer rsp.Body.Close()
	if rsp.StatusCode != http.StatusCreated {
		return nil, fmt.Errorf("failed to spawn member: status code %d", rsp.StatusCode)
	}
	var m proto.TeamMember
	if err := json.NewDecoder(rsp.Body).Decode(&m); err != nil {
		return nil, fmt.Errorf("failed to decode team member: %w", err)
	}
	return &m, nil
}

// CreateTask creates a task in a team. The server returns 201 on success.
func (c *Client) CreateTask(ctx context.Context, workspaceID, teamID string, req proto.CreateTeamTaskRequest) (*proto.TeamTask, error) {
	rsp, err := c.post(ctx, fmt.Sprintf("/workspaces/%s/teams/%s/tasks", workspaceID, teamID), nil, jsonBody(req), nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create task: %w", err)
	}
	defer rsp.Body.Close()
	if rsp.StatusCode != http.StatusCreated {
		return nil, fmt.Errorf("failed to create task: status code %d", rsp.StatusCode)
	}
	var task proto.TeamTask
	if err := json.NewDecoder(rsp.Body).Decode(&task); err != nil {
		return nil, fmt.Errorf("failed to decode team task: %w", err)
	}
	return &task, nil
}

// UpdateTask updates an existing task (PATCH). Returns the updated task.
func (c *Client) UpdateTask(ctx context.Context, workspaceID, teamID, taskID string, req proto.UpdateTeamTaskRequest) (*proto.TeamTask, error) {
	rsp, err := c.patch(ctx, fmt.Sprintf("/workspaces/%s/teams/%s/tasks/%s", workspaceID, teamID, taskID), nil, jsonBody(req), nil)
	if err != nil {
		return nil, fmt.Errorf("failed to update task: %w", err)
	}
	defer rsp.Body.Close()
	if rsp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("failed to update task: status code %d", rsp.StatusCode)
	}
	var task proto.TeamTask
	if err := json.NewDecoder(rsp.Body).Decode(&task); err != nil {
		return nil, fmt.Errorf("failed to decode team task: %w", err)
	}
	return &task, nil
}

// ListTeamEvents lists team events after a sequence number cursor.
func (c *Client) ListTeamEvents(ctx context.Context, workspaceID, teamID string, afterSeq int64, limit int) (*proto.TeamEventsResponse, error) {
	q := url.Values{}
	q.Set("after", strconv.FormatInt(afterSeq, 10))
	q.Set("limit", strconv.Itoa(limit))
	rsp, err := c.get(ctx, fmt.Sprintf("/workspaces/%s/teams/%s/events", workspaceID, teamID), q, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to list team events: %w", err)
	}
	defer rsp.Body.Close()
	if rsp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("failed to list team events: status code %d", rsp.StatusCode)
	}
	var resp proto.TeamEventsResponse
	if err := json.NewDecoder(rsp.Body).Decode(&resp); err != nil {
		return nil, fmt.Errorf("failed to decode team events: %w", err)
	}
	return &resp, nil
}
