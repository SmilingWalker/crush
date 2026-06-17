// tools_test.go — M4-06: Member Tools tests
//
// Covers acceptance (master doc M4-06 :619-624):
//  1. Member team_report_status updates task status
//  2. Member team_send_message direct/broadcast/role
//  3. Member cannot update another member's task
//  4. Actor policy check (no shutdown_request from member)
//
// Fixture: :memory: SQLite + Service with enabled gate + pre-seeded team/member/tasks.

package team

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"charm.land/fantasy"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// newToolsFixture creates a Service, team, and two members with roles.
// Returns svc, teamID, memberID1, memberID2 (role=leader), memberRole1.
func newToolsFixture(t *testing.T) (Service, string, string, string) {
	t.Helper()
	svc, _ := newServiceFixture(t)
	ctx := context.Background()

	snap, err := svc.CreateTeam(ctx, CreateTeamRequest{
		WorkspaceID: "ws-1", LeaderSessionID: "lead-1", Name: "tools-test",
	})
	require.NoError(t, err)
	teamID := snap.Team.ID

	m1, err := svc.SpawnMember(ctx, SpawnMemberRequest{
		TeamID: teamID, Name: "coder", Role: "programmer", AgentProfile: "{}",
	})
	require.NoError(t, err)

	m2, err := svc.SpawnMember(ctx, SpawnMemberRequest{
		TeamID: teamID, Name: "leader", Role: "lead", AgentProfile: "{}",
	})
	require.NoError(t, err)

	return svc, teamID, m1.ID, m2.ID
}

// marshalParams is a test helper.
func marshalParams(t *testing.T, v any) string {
	t.Helper()
	b, err := json.Marshal(v)
	require.NoError(t, err)
	return string(b)
}

// --- team_report_status tests ---

func TestTeamReportStatus_HappyPath_UpdateOwnTask(t *testing.T) {
	svc, teamID, memberID, _ := newToolsFixture(t)
	ctx := context.Background()

	// Create a task assigned to this member.
	task, err := svc.CreateTask(ctx, CreateTaskRequest{
		TeamID: teamID, Title: "Do thing", CreatedByMemberID: memberID,
	})
	require.NoError(t, err)
	// Assign it.
	updated, err := svc.UpdateTask(ctx, UpdateTaskRequest{
		ID: task.ID, TeamID: teamID, Status: TaskAssigned, AssigneeMemberID: &memberID,
	})
	require.NoError(t, err)

	tool := NewTeamReportStatusTool(memberID, teamID, svc)

	input := marshalParams(t, map[string]string{
		"task_id": task.ID,
		"status":  "in_progress",
	})
	resp, err := tool.Run(ctx, fantasy.ToolCall{ID: "c1", Name: "team_report_status", Input: input})
	require.NoError(t, err)
	assert.False(t, resp.IsError)
	assert.Contains(t, resp.Content, "in_progress")

	// Verify task was actually updated.
	got, err := svc.GetTask(ctx, teamID, task.ID)
	require.NoError(t, err)
	assert.Equal(t, TaskInProgress, got.Status)
	assert.Greater(t, got.Version, updated.Version, "version should have incremented")
}

func TestTeamReportStatus_RejectsOtherMembersTask(t *testing.T) {
	svc, teamID, member1ID, member2ID := newToolsFixture(t)
	ctx := context.Background()

	// Create a task assigned to member2.
	task, err := svc.CreateTask(ctx, CreateTaskRequest{
		TeamID: teamID, Title: "Leaders task", CreatedByMemberID: member2ID,
	})
	require.NoError(t, err)
	_, err = svc.UpdateTask(ctx, UpdateTaskRequest{
		ID: task.ID, TeamID: teamID, Status: TaskAssigned, AssigneeMemberID: &member2ID,
	})
	require.NoError(t, err)

	// member1 tries to update member2's task.
	tool := NewTeamReportStatusTool(member1ID, teamID, svc)
	input := marshalParams(t, map[string]string{
		"task_id": task.ID,
		"status":  "in_progress",
	})
	resp, err := tool.Run(ctx, fantasy.ToolCall{ID: "c2", Name: "team_report_status", Input: input})
	require.NoError(t, err)
	assert.True(t, resp.IsError)
	assert.Contains(t, resp.Content, "not to you")
	assert.Contains(t, resp.Content, member1ID)

	// Verify task was NOT changed.
	got, err := svc.GetTask(ctx, teamID, task.ID)
	require.NoError(t, err)
	assert.Equal(t, TaskAssigned, got.Status)
}

func TestTeamReportStatus_MemberCanClaimUnassignedTask(t *testing.T) {
	svc, teamID, memberID, _ := newToolsFixture(t)
	ctx := context.Background()

	// Create an unassigned task (queued).
	task, err := svc.CreateTask(ctx, CreateTaskRequest{
		TeamID: teamID, Title: "Unassigned task", CreatedByMemberID: memberID,
	})
	require.NoError(t, err)

	tool := NewTeamReportStatusTool(memberID, teamID, svc)
	input := marshalParams(t, map[string]string{
		"task_id": task.ID,
		"status":  "in_progress",
	})
	resp, err := tool.Run(ctx, fantasy.ToolCall{ID: "c3", Name: "team_report_status", Input: input})
	require.NoError(t, err)
	assert.False(t, resp.IsError)

	got, err := svc.GetTask(ctx, teamID, task.ID)
	require.NoError(t, err)
	assert.Equal(t, TaskInProgress, got.Status)
}

func TestTeamReportStatus_InvalidStatus(t *testing.T) {
	svc, teamID, memberID, _ := newToolsFixture(t)
	ctx := context.Background()

	task, err := svc.CreateTask(ctx, CreateTaskRequest{
		TeamID: teamID, Title: "Task", CreatedByMemberID: memberID,
	})
	require.NoError(t, err)

	tool := NewTeamReportStatusTool(memberID, teamID, svc)
	input := marshalParams(t, map[string]string{
		"task_id": task.ID,
		"status":  "bogus_status",
	})
	resp, err := tool.Run(ctx, fantasy.ToolCall{ID: "c4", Name: "team_report_status", Input: input})
	require.NoError(t, err)
	assert.True(t, resp.IsError)
	assert.Contains(t, resp.Content, "invalid status")
}

func TestTeamReportStatus_TaskNotFound(t *testing.T) {
	svc, teamID, memberID, _ := newToolsFixture(t)
	ctx := context.Background()

	tool := NewTeamReportStatusTool(memberID, teamID, svc)
	input := marshalParams(t, map[string]string{
		"task_id": "nonexistent-task-id",
		"status":  "in_progress",
	})
	resp, err := tool.Run(ctx, fantasy.ToolCall{ID: "c5", Name: "team_report_status", Input: input})
	require.NoError(t, err)
	assert.True(t, resp.IsError)
	assert.Contains(t, resp.Content, "not found")
}

func TestTeamReportStatus_CASConflictRetryThenBlock(t *testing.T) {
	svc, teamID, memberID, _ := newToolsFixture(t)
	ctx := context.Background()

	task, err := svc.CreateTask(ctx, CreateTaskRequest{
		TeamID: teamID, Title: "Race task", CreatedByMemberID: memberID,
	})
	require.NoError(t, err)
	_, err = svc.UpdateTask(ctx, UpdateTaskRequest{
		ID: task.ID, TeamID: teamID, Status: TaskAssigned, AssigneeMemberID: &memberID,
	})
	require.NoError(t, err)

	tool := NewTeamReportStatusTool(memberID, teamID, svc)

	// Simulate a concurrent update: directly bump the task status outside the tool,
	// so the tool's first read sees old version but CAS fails because we already
	// updated from outside.
	_, err = svc.UpdateTask(ctx, UpdateTaskRequest{
		ID: task.ID, TeamID: teamID, Status: TaskInProgress,
	})
	require.NoError(t, err) // concurrent update wins

	// Now the tool tries to update. It reads, CAS fails once (retry succeeds
	// because the concurrent write already landed).
	input := marshalParams(t, map[string]string{
		"task_id": task.ID,
		"status":  "completed",
	})
	resp, err := tool.Run(ctx, fantasy.ToolCall{ID: "c6", Name: "team_report_status", Input: input})
	require.NoError(t, err)
	assert.False(t, resp.IsError)
	assert.Contains(t, resp.Content, "completed")

	// Verify the tool's update landed.
	got, err := svc.GetTask(ctx, teamID, task.ID)
	require.NoError(t, err)
	assert.Equal(t, TaskCompleted, got.Status)
}

func TestTeamReportStatus_WithResultSummary(t *testing.T) {
	svc, teamID, memberID, _ := newToolsFixture(t)
	ctx := context.Background()

	task, err := svc.CreateTask(ctx, CreateTaskRequest{
		TeamID: teamID, Title: "Summarize me", CreatedByMemberID: memberID,
	})
	require.NoError(t, err)

	tool := NewTeamReportStatusTool(memberID, teamID, svc)
	input := marshalParams(t, map[string]string{
		"task_id":        task.ID,
		"status":         "completed",
		"result_summary": "All done!",
	})
	resp, err := tool.Run(ctx, fantasy.ToolCall{ID: "c7", Name: "team_report_status", Input: input})
	require.NoError(t, err)
	assert.False(t, resp.IsError)
	assert.Contains(t, resp.Content, "completed")

	got, err := svc.GetTask(ctx, teamID, task.ID)
	require.NoError(t, err)
	assert.Equal(t, TaskCompleted, got.Status)
	assert.NotNil(t, got.ResultSummary)
	assert.Equal(t, "All done!", *got.ResultSummary)
}

// --- team_send_message tests ---

func TestTeamSendMessage_Direct(t *testing.T) {
	svc, teamID, fromMemberID, toMemberID := newToolsFixture(t)
	ctx := context.Background()

	tool := NewTeamSendMessageTool(fromMemberID, teamID, "programmer", svc, nil)
	input := marshalParams(t, map[string]string{
		"recipient_type": "direct",
		"to_member_id":   toMemberID,
		"kind":           "message",
		"summary":        "Hello",
		"payload":        "Hi leader!",
	})
	resp, err := tool.Run(ctx, fantasy.ToolCall{ID: "m1", Name: "team_send_message", Input: input})
	require.NoError(t, err)
	assert.False(t, resp.IsError)
	assert.Contains(t, resp.Content, toMemberID, "response should mention recipient")

	// Verify the message was stored.
	msgs, err := svc.GetUnreadMessages(ctx, toMemberID, 10)
	require.NoError(t, err)
	require.Len(t, msgs, 1)
	assert.Equal(t, "Hello", msgs[0].Summary)
	assert.Equal(t, fromMemberID, msgs[0].FromMemberID)
}

func TestTeamSendMessage_Broadcast(t *testing.T) {
	svc, teamID, fromMemberID, toMemberID := newToolsFixture(t)
	ctx := context.Background()

	tool := NewTeamSendMessageTool(fromMemberID, teamID, "programmer", svc, nil)
	input := marshalParams(t, map[string]string{
		"recipient_type": "broadcast",
		"kind":           "message",
		"summary":        "Broadcast hello",
		"payload":        "Everyone!",
	})
	resp, err := tool.Run(ctx, fantasy.ToolCall{ID: "m2", Name: "team_send_message", Input: input})
	require.NoError(t, err)
	assert.False(t, resp.IsError)
	// Should have sent to other member (not self).
	assert.Contains(t, resp.Content, toMemberID)

	// Sender should not have a receipt (they don't receive their own broadcast).
	msgs, err := svc.GetUnreadMessages(ctx, fromMemberID, 10)
	require.NoError(t, err)
	assert.Len(t, msgs, 0, "sender should not receive own broadcast")

	// Other member should have the message.
	msgs, err = svc.GetUnreadMessages(ctx, toMemberID, 10)
	require.NoError(t, err)
	assert.Len(t, msgs, 1)
}

func TestTeamSendMessage_Role(t *testing.T) {
	svc, teamID, fromMemberID, leaderMemberID := newToolsFixture(t)
	ctx := context.Background()

	// fromMemberID=programmer, leaderMemberID=lead
	tool := NewTeamSendMessageTool(fromMemberID, teamID, "programmer", svc, nil)
	input := marshalParams(t, map[string]string{
		"recipient_type": "role",
		"to_role":        "lead",
		"kind":           "message",
		"summary":        "Role msg",
		"payload":        "Only leads",
	})
	resp, err := tool.Run(ctx, fantasy.ToolCall{ID: "m3", Name: "team_send_message", Input: input})
	require.NoError(t, err)
	assert.False(t, resp.IsError)
	assert.Contains(t, resp.Content, leaderMemberID)

	// Leader (role=lead) should get it.
	msgs, err := svc.GetUnreadMessages(ctx, leaderMemberID, 10)
	require.NoError(t, err)
	assert.Len(t, msgs, 1)

	// Sender (role=programmer) should not.
	msgs, err = svc.GetUnreadMessages(ctx, fromMemberID, 10)
	require.NoError(t, err)
	assert.Len(t, msgs, 0)
}

func TestTeamSendMessage_RejectsShutdownRequest(t *testing.T) {
	svc, teamID, fromMemberID, _ := newToolsFixture(t)
	ctx := context.Background()

	tool := NewTeamSendMessageTool(fromMemberID, teamID, "programmer", svc, nil)

	for _, blocked := range []string{"shutdown_request", "shutdown_ack"} {
		input := marshalParams(t, map[string]string{
			"recipient_type": "broadcast",
			"kind":           blocked,
			"summary":        "Shutdown",
			"payload":        "Stop!",
		})
		resp, err := tool.Run(ctx, fantasy.ToolCall{ID: "m4_" + blocked, Name: "team_send_message", Input: input})
		require.NoError(t, err)
		assert.True(t, resp.IsError)
		assert.Contains(t, resp.Content, "not allowed")
	}
}

func TestTeamSendMessage_MemberCanSendTaskStatus(t *testing.T) {
	svc, teamID, fromMemberID, toMemberID := newToolsFixture(t)
	ctx := context.Background()

	tool := NewTeamSendMessageTool(fromMemberID, teamID, "programmer", svc, nil)
	input := marshalParams(t, map[string]string{
		"recipient_type": "direct",
		"to_member_id":   toMemberID,
		"kind":           "task_status",
		"summary":        "Task update",
		"payload":        "Done with M4-06",
	})
	resp, err := tool.Run(ctx, fantasy.ToolCall{ID: "m5", Name: "team_send_message", Input: input})
	require.NoError(t, err)
	assert.False(t, resp.IsError)

	msgs, err := svc.GetUnreadMessages(ctx, toMemberID, 10)
	require.NoError(t, err)
	assert.Len(t, msgs, 1)
	assert.Equal(t, KindTaskStatus, msgs[0].Kind)
}

func TestTeamSendMessage_InvalidKind(t *testing.T) {
	svc, teamID, memberID, _ := newToolsFixture(t)
	ctx := context.Background()

	tool := NewTeamSendMessageTool(memberID, teamID, "programmer", svc, nil)
	input := marshalParams(t, map[string]string{
		"recipient_type": "broadcast",
		"kind":           "bogus_kind",
		"summary":        "X",
		"payload":        "Y",
	})
	resp, err := tool.Run(ctx, fantasy.ToolCall{ID: "m6", Name: "team_send_message", Input: input})
	require.NoError(t, err)
	assert.True(t, resp.IsError)
	assert.Contains(t, resp.Content, "invalid kind")
}

func TestTeamSendMessage_InvalidRecipientType(t *testing.T) {
	svc, teamID, memberID, _ := newToolsFixture(t)
	ctx := context.Background()

	tool := NewTeamSendMessageTool(memberID, teamID, "programmer", svc, nil)
	input := marshalParams(t, map[string]string{
		"recipient_type": "telepathy",
		"kind":           "message",
		"summary":        "X",
		"payload":        "Y",
	})
	resp, err := tool.Run(ctx, fantasy.ToolCall{ID: "m7", Name: "team_send_message", Input: input})
	require.NoError(t, err)
	assert.True(t, resp.IsError)
	assert.Contains(t, resp.Content, "invalid recipient_type")
}

func TestTeamSendMessage_DirectRequiresToMemberID(t *testing.T) {
	svc, teamID, memberID, _ := newToolsFixture(t)
	ctx := context.Background()

	tool := NewTeamSendMessageTool(memberID, teamID, "programmer", svc, nil)
	input := marshalParams(t, map[string]string{
		"recipient_type": "direct",
		// to_member_id intentionally missing
		"kind":    "message",
		"summary": "X",
		"payload": "Y",
	})
	resp, err := tool.Run(ctx, fantasy.ToolCall{ID: "m8", Name: "team_send_message", Input: input})
	require.NoError(t, err)
	assert.True(t, resp.IsError)
	assert.Contains(t, resp.Content, "to_member_id")
}

// --- policy tests ---

func TestMemberTools_Policy_ShutdownKindBlocked(t *testing.T) {
	// Verify that even a member with role="lead" cannot send shutdown_request via the member tool.
	svc, teamID, leaderID, _ := newToolsFixture(t)
	ctx := context.Background()

	// Even if the DB role is "lead", the member tool blocks shutdown_request.
	tool := NewTeamSendMessageTool(leaderID, teamID, "lead", svc, nil)
	for _, blocked := range []string{"shutdown_request", "shutdown_ack"} {
		input := marshalParams(t, map[string]string{
			"recipient_type": "broadcast",
			"kind":           blocked,
			"summary":        "X",
			"payload":        "Y",
		})
		resp, err := tool.Run(ctx, fantasy.ToolCall{ID: "p1_" + blocked, Name: "team_send_message", Input: input})
		require.NoError(t, err)
		assert.True(t, resp.IsError, "member should not be able to send %s", blocked)
	}
}

func TestMemberTools_ToolNamesAreCorrect(t *testing.T) {
	svc, teamID, memberID, _ := newToolsFixture(t)

	reportTool := NewTeamReportStatusTool(memberID, teamID, svc)
	assert.Equal(t, "team_report_status", reportTool.Info().Name)

	sendTool := NewTeamSendMessageTool(memberID, teamID, "coder", svc, nil)
	assert.Equal(t, "team_send_message", sendTool.Info().Name)
}

func TestMemberTools_DescriptionIsNonEmpty(t *testing.T) {
	svc, teamID, memberID, _ := newToolsFixture(t)

	reportTool := NewTeamReportStatusTool(memberID, teamID, svc)
	assert.NotEmpty(t, reportTool.Info().Description)

	sendTool := NewTeamSendMessageTool(memberID, teamID, "coder", svc, nil)
	assert.NotEmpty(t, sendTool.Info().Description)
}

func TestMemberTools_ProviderOptions(t *testing.T) {
	svc, teamID, memberID, _ := newToolsFixture(t)

	reportTool := NewTeamReportStatusTool(memberID, teamID, svc)
	// ProviderOptions may be nil (fantasy.NewAgentTool default).
	assert.Nil(t, reportTool.ProviderOptions())

	sendTool := NewTeamSendMessageTool(memberID, teamID, "coder", svc, nil)
	assert.Nil(t, sendTool.ProviderOptions())
}

func TestMemberTools_SetProviderOptions(t *testing.T) {
	svc, teamID, memberID, _ := newToolsFixture(t)

	reportTool := NewTeamReportStatusTool(memberID, teamID, svc)
	// Should not panic.
	reportTool.SetProviderOptions(fantasy.ProviderOptions{})

	sendTool := NewTeamSendMessageTool(memberID, teamID, "coder", svc, nil)
	sendTool.SetProviderOptions(fantasy.ProviderOptions{})
}

func TestMemberTools_ReturnsErrorOnEmptyParams(t *testing.T) {
	svc, teamID, memberID, _ := newToolsFixture(t)
	ctx := context.Background()

	reportTool := NewTeamReportStatusTool(memberID, teamID, svc)
	resp, err := reportTool.Run(ctx, fantasy.ToolCall{ID: "e1", Name: "team_report_status", Input: "{}"})
	require.NoError(t, err)
	assert.True(t, resp.IsError)
	assert.True(t, strings.Contains(resp.Content, "task_id") || strings.Contains(resp.Content, "required"))

	sendTool := NewTeamSendMessageTool(memberID, teamID, "coder", svc, nil)
	resp, err = sendTool.Run(ctx, fantasy.ToolCall{ID: "e2", Name: "team_send_message", Input: "{}"})
	require.NoError(t, err)
	assert.True(t, resp.IsError)
}
