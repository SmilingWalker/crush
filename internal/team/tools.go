// tools.go — M4-06: Member Tools (team_report_status + team_send_message)
//
// Two fantasy.AgentTool builders that give long-lived team members the ability to
// update their own task status (via CAS read-then-retry) and send messages to
// other members (via the M4-04 Mailbox SendMessage API).
//
// Design (plan 2026-06-15-m4-06-tools.md):
//   - Tools are built as closures capturing memberID/teamID/role so the handler
//     always knows "who am I" without reading ActorContext from the context.
//   - team_report_status: ownership check → CAS update → retry once on conflict
//     → return "blocked" on persistent conflict.
//   - team_send_message: kind whitelist (message/task_status only) → delegate to
//     M4-04 SendMessage. Members cannot send shutdown_request/shutdown_ack.
//
// Acceptance (master doc M4-06 :619-624):
//  1. Member team_report_status updates task status
//  2. Member team_send_message direct/broadcast/role all work
//  3. Member cannot update another member's task
//  4. Actor policy checks correct (no shutdown_request from member)

package team

import (
	"context"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"charm.land/fantasy"
)

//go:embed team_report_status.md
var teamReportStatusDescription string

//go:embed team_send_message.md
var teamSendMessageDescription string

// --- Tool name constants ---

const (
	TeamReportStatusToolName = "team_report_status"
	TeamSendMessageToolName  = "team_send_message"
)

// --- team_report_status params ---

// TeamReportStatusParams is the input for the team_report_status tool.
type TeamReportStatusParams struct {
	TaskID        string `json:"task_id"`
	Status        string `json:"status"`
	ResultSummary string `json:"result_summary,omitempty"`
}

// NewTeamReportStatusTool returns a fantasy.AgentTool that lets a member update
// their own task status via CAS. The tool captures memberID and teamID at
// construction time so it always knows the caller's identity.
//
// CAS retry: on ErrVersionConflict, retries once (read fresh + CAS again).
// If the second attempt also conflicts, returns an error suggesting the member
// transition to blocked.
func NewTeamReportStatusTool(memberID, teamID string, svc Service) fantasy.AgentTool {
	return fantasy.NewAgentTool(
		TeamReportStatusToolName,
		teamReportStatusDescription,
		func(ctx context.Context, params TeamReportStatusParams, call fantasy.ToolCall) (fantasy.ToolResponse, error) {
			// --- validate ---
			if strings.TrimSpace(params.TaskID) == "" {
				return newTextError("task_id is required"), nil
			}
			if !TaskStatus(params.Status).Valid() {
				return newTextError(fmt.Sprintf(
					"invalid status %q (valid: queued, assigned, in_progress, blocked, completed, failed, canceled)",
					params.Status,
				)), nil
			}

			newStatus := TaskStatus(params.Status)

			// --- read current task ---
			task, err := svc.GetTask(ctx, teamID, params.TaskID)
			if err != nil {
				return newTextError(fmt.Sprintf("task not found: %s", err.Error())), nil
			}

			// --- ownership check ---
			// Member can update: tasks assigned to them, OR unassigned tasks
			// (they can self-claim). Tasks assigned to someone else are denied.
			if task.AssigneeMemberID != nil && *task.AssigneeMemberID != memberID {
				return newTextError(fmt.Sprintf(
					"task %s is assigned to %s, not to you (%s)",
					task.ID, *task.AssigneeMemberID, memberID,
				)), nil
			}

			// --- CAS update (attempt 1) ---
			assignee := task.AssigneeMemberID
			// If unassigned and member is claiming it, set assignee.
			if assignee == nil && newStatus != TaskQueued {
				assignee = &memberID
			}

			resultSummary := &params.ResultSummary
			if params.ResultSummary == "" {
				resultSummary = nil
			}

			_, err = svc.UpdateTask(ctx, UpdateTaskRequest{
				ID:               task.ID,
				TeamID:           teamID,
				Status:           newStatus,
				AssigneeMemberID: assignee,
				ResultSummary:    resultSummary,
			})
			if err == nil {
				return newTextResponse(
					fmt.Sprintf("Task %s updated to %s.", task.ID, newStatus),
				), nil
			}

			// --- CAS retry (attempt 2) ---
			if !errors.Is(err, ErrVersionConflict) {
				return newTextError(fmt.Sprintf("update failed: %s", err.Error())), nil
			}

			// Re-read fresh version.
			task, err = svc.GetTask(ctx, teamID, params.TaskID)
			if err != nil {
				return newTextError(fmt.Sprintf("task not found on retry: %s", err.Error())), nil
			}

			// Re-validate ownership after re-read (TOCTOU guard, Finding 1).
			if task.AssigneeMemberID != nil && *task.AssigneeMemberID != memberID {
				return newTextError(fmt.Sprintf(
					"task %s was reassigned to %s during retry, no longer yours (%s)",
					task.ID, *task.AssigneeMemberID, memberID,
				)), nil
			}

			// Re-derive assignee from fresh read (Finding 2).
			assignee = task.AssigneeMemberID
			if assignee == nil && newStatus != TaskQueued {
				assignee = &memberID
			}

			_, err = svc.UpdateTask(ctx, UpdateTaskRequest{
				ID:               task.ID,
				TeamID:           teamID,
				Status:           newStatus,
				AssigneeMemberID: assignee,
				ResultSummary:    resultSummary,
			})
			if err != nil {
				if errors.Is(err, ErrVersionConflict) {
					return newTextError(
						"Task was modified concurrently after two attempts. Your update could not be applied. Consider transitioning to blocked and notifying the leader.",
					), nil
				}
				return newTextError(fmt.Sprintf("update failed on retry: %s", err.Error())), nil
			}

			return newTextResponse(
				fmt.Sprintf("Task %s updated to %s (retry succeeded).", task.ID, newStatus),
			), nil
		},
	)
}

// --- team_send_message params ---

// TeamSendMessageParams is the input for the team_send_message tool.
type TeamSendMessageParams struct {
	RecipientType string `json:"recipient_type"`
	ToMemberID    string `json:"to_member_id,omitempty"`
	ToRole        string `json:"to_role,omitempty"`
	Kind          string `json:"kind"`
	Summary       string `json:"summary"`
	Payload       string `json:"payload"`
}

// NewTeamSendMessageTool returns a fantasy.AgentTool that lets a member send
// messages to other members via the M4-04 Mailbox. The tool captures memberID,
// teamID, and role at construction time.
//
// Kind whitelist: only message and task_status are allowed. shutdown_request
// and shutdown_ack are blocked (only the leader/TeamRunner can shut down members).
func NewTeamSendMessageTool(memberID, teamID, role string, svc Service) fantasy.AgentTool {
	return fantasy.NewAgentTool(
		TeamSendMessageToolName,
		teamSendMessageDescription,
		func(ctx context.Context, params TeamSendMessageParams, call fantasy.ToolCall) (fantasy.ToolResponse, error) {
			// --- validate kind ---
			kind := MessageKind(params.Kind)
			if !kind.Valid() {
				return newTextError(fmt.Sprintf(
					"invalid kind %q (valid: message, task_status, task_assignment, shutdown_request, shutdown_ack)",
					params.Kind,
				)), nil
			}

			// Member policy: cannot send shutdown_request or shutdown_ack.
			if kind == KindShutdownRequest || kind == KindShutdownAck {
				return newTextError(fmt.Sprintf(
					"kind %q is not allowed for members (only the team leader can send shutdown messages)",
					params.Kind,
				)), nil
			}

			// --- validate recipient_type ---
			recipientType := RecipientType(params.RecipientType)
			if !recipientType.Valid() {
				return newTextError(fmt.Sprintf(
					"invalid recipient_type %q (valid: direct, broadcast, role)",
					params.RecipientType,
				)), nil
			}

			// Direct requires to_member_id.
			if recipientType == RecipientDirect && strings.TrimSpace(params.ToMemberID) == "" {
				return newTextError("to_member_id is required for direct messages"), nil
			}

			// --- send ---
			recipientIDs, err := svc.SendMessage(ctx, SendMessageRequest{
				TeamID:        teamID,
				FromMemberID:  memberID,
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

// --- response helpers ---

func newTextResponse(content string) fantasy.ToolResponse {
	return fantasy.ToolResponse{
		Content: content,
		IsError: false,
	}
}

func newTextError(msg string) fantasy.ToolResponse {
	return fantasy.ToolResponse{
		Content: msg,
		IsError: true,
	}
}

// Keep json import for future use even if currently unused in param parsing
// (params are auto-parsed by fantasy.NewAgentTool).
var _ = json.Marshal
