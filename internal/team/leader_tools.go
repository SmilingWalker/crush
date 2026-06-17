// leader_tools.go — M4.5: Leader Tools
// (team_create, team_spawn_member, team_list, team_status, team_send_message)
//
// Five fantasy.AgentTool builders injected into the coder agent so the model
// can create teams, spawn members, send messages, and inspect state.
//
// Design (spec 2026-06-16-m4.5-leader-tools-design.md):
//   - Tools are closures capturing Service + TeamRunner (same pattern as M4-06).
//   - team_create: Service.CreateTeam → TeamRunner.StartTeam
//   - team_spawn_member: TeamRunner.SpawnMember (which calls Service.SpawnMember internally)
//   - team_list: Service.ListTeams (read-only)
//   - team_status: TeamRunner.Status (read-only)

package team

import (
	"context"
	_ "embed"
	"fmt"
	"log/slog"
	"strings"

	"charm.land/fantasy"
	"github.com/charmbracelet/crush/internal/agent"
)

//go:embed team_create.md
var teamCreateDesc string

//go:embed team_spawn_member.md
var teamSpawnMemberDesc string

//go:embed team_list.md
var teamListDesc string

//go:embed team_status.md
var teamStatusDesc string

//go:embed leader_send_message.md
var leaderSendMessageDesc string

const (
	TeamCreateToolName      = "team_create"
	TeamSpawnMemberToolName = "team_spawn_member"
	TeamListToolName        = "team_list"
	TeamStatusToolName      = "team_status"
)

// =============================================================================
// team_create
// =============================================================================

type TeamCreateParams struct {
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
}

func NewTeamCreateTool(svc Service, runner TeamRunner, sessionID func() string) fantasy.AgentTool {
	return fantasy.NewAgentTool(
		TeamCreateToolName,
		teamCreateDesc,
		func(ctx context.Context, params TeamCreateParams, call fantasy.ToolCall) (fantasy.ToolResponse, error) {
			if strings.TrimSpace(params.Name) == "" {
				return newTextError("name is required"), nil
			}
			leaderSID := sessionID()
			snap, err := svc.CreateTeam(ctx, CreateTeamRequest{
				WorkspaceID:     "default",
				LeaderSessionID: leaderSID,
				Name:            params.Name,
				Description:     params.Description,
			})
			if err != nil {
				return newTextError(fmt.Sprintf("create team failed: %s", err.Error())), nil
			}

			// M5-P2: leader member with user's main session.
			// Leader = the user; its session is always valid for bar switching.
			leader, lerr := svc.SpawnMember(ctx, SpawnMemberRequest{
				TeamID:       snap.Team.ID,
				Name:         "leader",
				Role:         "leader",
				AgentProfile: "{}",
				SessionID:    &leaderSID,
			})
			if lerr != nil {
				return newTextError(fmt.Sprintf("spawn leader member failed: %s", lerr.Error())), nil
			}

			if err := runner.StartTeam(ctx, snap.Team.ID); err != nil {
				return newTextError(fmt.Sprintf("start team failed: %s", err.Error())), nil
			}
			return newTextResponse(
				fmt.Sprintf("Team %q created (id=%s, leader_member_id=%s, status=%s). Use team_spawn_member to add more members.",
					params.Name, snap.Team.ID, leader.ID, snap.Team.Status),
			), nil
		},
	)
}

// =============================================================================
// team_spawn_member
// =============================================================================

type TeamSpawnMemberParams struct {
	TeamID         string `json:"team_id"`
	Name           string `json:"name"`
	Role           string `json:"role"`
	ModelType      string `json:"model_type,omitempty"`
	PermissionMode string `json:"permission_mode,omitempty"`
}

func NewTeamSpawnMemberTool(svc Service, runner TeamRunner) fantasy.AgentTool {
	return fantasy.NewAgentTool(
		TeamSpawnMemberToolName,
		teamSpawnMemberDesc,
		func(ctx context.Context, params TeamSpawnMemberParams, call fantasy.ToolCall) (fantasy.ToolResponse, error) {
			if strings.TrimSpace(params.TeamID) == "" {
				return newTextError("team_id is required"), nil
			}
			if strings.TrimSpace(params.Name) == "" {
				return newTextError("name is required"), nil
			}
			if strings.TrimSpace(params.Role) == "" {
				return newTextError("role is required"), nil
			}

			modelType := params.ModelType
			if modelType == "" {
				modelType = "inherit"
			}
			permMode := params.PermissionMode
			if permMode == "" {
				permMode = "default"
			}

			// TeamRunner.SpawnMember creates the DB record AND starts the runtime
			// loop. Do NOT also call svc.SpawnMember — that would create a duplicate.
			spec := agent.AgentSpec{
				AgentType:      "general-purpose",
				ModelType:      modelType,
				PermissionMode: permMode,
				MaxPromptBytes: DefaultMaxPromptBytes,
				ToolPolicy: agent.ToolPolicyProfile{
					AllowedTools: nil, // nil = all built-in tools (bash, read, write, edit, grep, glob, etc.)
				},
				MaxTurns: 50,
			}
			dbMember, err := runner.SpawnMember(ctx, params.TeamID, params.Name, params.Role, "{}", spec)
			if err != nil {
				return newTextError(fmt.Sprintf("spawn member failed: %s", err.Error())), nil
			}

			return newTextResponse(
				fmt.Sprintf("Member %q (id=%s, role=%s) spawned in team %s. State: idle.",
					params.Name, dbMember.ID, params.Role, params.TeamID),
			), nil
		},
	)
}

// =============================================================================
// team_list
// =============================================================================

type TeamListParams struct {
	WorkspaceID string `json:"workspace_id,omitempty"`
}

func NewTeamListTool(svc Service) fantasy.AgentTool {
	return fantasy.NewAgentTool(
		TeamListToolName,
		teamListDesc,
		func(ctx context.Context, params TeamListParams, call fantasy.ToolCall) (fantasy.ToolResponse, error) {
			wsID := params.WorkspaceID
			if wsID == "" {
				wsID = "default"
			}
			teams, err := svc.ListTeams(ctx, wsID, TeamFilter{})
			if err != nil {
				return newTextError(fmt.Sprintf("list teams failed: %s", err.Error())), nil
			}
			if len(teams) == 0 {
				return newTextResponse(
					fmt.Sprintf("No teams found in workspace %q. Create one with team_create.", wsID),
				), nil
			}
			var b strings.Builder
			b.WriteString(fmt.Sprintf("Teams in workspace %q:\n", wsID))
			for i, t := range teams {
				if i > 0 {
					b.WriteRune('\n')
				}
				b.WriteString(fmt.Sprintf("- %s (id=%s, status=%s)", t.Name, t.ID, t.Status))
			}
			return newTextResponse(b.String()), nil
		},
	)
}

// =============================================================================
// team_status
// =============================================================================

type TeamStatusParams struct {
	TeamID string `json:"team_id"`
}

func NewTeamStatusTool(runner TeamRunner) fantasy.AgentTool {
	return fantasy.NewAgentTool(
		TeamStatusToolName,
		teamStatusDesc,
		func(ctx context.Context, params TeamStatusParams, call fantasy.ToolCall) (fantasy.ToolResponse, error) {
			if strings.TrimSpace(params.TeamID) == "" {
				return newTextError("team_id is required"), nil
			}
			status, err := runner.Status(ctx, params.TeamID)
			if err != nil {
				return newTextError(fmt.Sprintf("get team status failed: %s", err.Error())), nil
			}
			var b strings.Builder
			b.WriteString(fmt.Sprintf("Team %s: %d members, %d active runs\n",
				status.TeamID, len(status.Members), status.ActiveRuns))
			for id, ms := range status.Members {
				b.WriteString(fmt.Sprintf("\n- %s (%s) state=%s", id, ms.Role, ms.State))
			}
			return newTextResponse(b.String()), nil
		},
	)
}

// =============================================================================
// team_send_message (leader variant)
// =============================================================================

// LeaderSendMessageParams is the leader's variant of TeamSendMessageParams.
// Unlike the member variant, team_id is a parameter since the leader manages
// multiple teams and isn't a team member itself.
type LeaderSendMessageParams struct {
	TeamID        string `json:"team_id"`
	FromMemberID  string `json:"from_member_id,omitempty"`
	RecipientType string `json:"recipient_type"`
	ToMemberID    string `json:"to_member_id,omitempty"`
	ToRole        string `json:"to_role,omitempty"`
	Kind          string `json:"kind"`
	Summary       string `json:"summary"`
	Payload       string `json:"payload"`
}

// NewLeaderSendMessageTool returns a fantasy.AgentTool for the leader to send
// messages to team members. Uses the same M4-04 SendMessage API as the member
// variant, but team_id is a parameter (not captured) because the leader can
// manage multiple teams.
func NewLeaderSendMessageTool(svc Service, runner TeamRunner) fantasy.AgentTool {
	return fantasy.NewAgentTool(
		TeamSendMessageToolName + "_leader",
		leaderSendMessageDesc,
		func(ctx context.Context, params LeaderSendMessageParams, call fantasy.ToolCall) (fantasy.ToolResponse, error) {
			if strings.TrimSpace(params.TeamID) == "" {
				return newTextError("team_id is required"), nil
			}
			if strings.TrimSpace(params.FromMemberID) == "" {
				return newTextError("from_member_id is required (use the leader_member_id from team_create)"), nil
			}
			kind := MessageKind(params.Kind)
			if !kind.Valid() {
				return newTextError(fmt.Sprintf(
					"invalid kind %q (valid: message, task_status, task_assignment, shutdown_request, shutdown_ack)",
					params.Kind,
				)), nil
			}
			recipientType := RecipientType(params.RecipientType)
			if !recipientType.Valid() {
				return newTextError(fmt.Sprintf(
					"invalid recipient_type %q (valid: direct, broadcast, role)",
					params.RecipientType,
				)), nil
			}
			if recipientType == RecipientDirect && strings.TrimSpace(params.ToMemberID) == "" {
				return newTextError("to_member_id is required for direct messages"), nil
			}
			recipientIDs, err := svc.SendMessage(ctx, SendMessageRequest{
				TeamID:        params.TeamID,
				FromMemberID:  params.FromMemberID,
				RecipientType: recipientType,
				ToMemberID:    params.ToMemberID,
				ToRole:        params.ToRole,
				Kind:          kind,
				Summary:       params.Summary,
				Payload:       params.Payload,
			})
			if err != nil {
				return newTextError(fmt.Sprintf("send failed: %s", err.Error())), nil
			}
			// Wake each recipient so they process the new message.
			for _, rid := range recipientIDs {
				if err := runner.WakeMember(ctx, params.TeamID, rid, WakeSourceMailbox); err != nil {
					slog.Warn("failed to wake member after send", "member_id", rid, "error", err)
				}
			}
			recipientList := "none"
			if len(recipientIDs) > 0 {
				recipientList = strings.Join(recipientIDs, ", ")
			}
			return newTextResponse(
				fmt.Sprintf("Message sent to %d recipient(s): %s", len(recipientIDs), recipientList),
			), nil
		},
	)
}

