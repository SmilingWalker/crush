package team

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestErrContextOverflow(t *testing.T) {
	assert.EqualError(t, ErrContextOverflow, "context overflow: priority-1 sections exceed context window")
}

func TestPromptPriority_Values(t *testing.T) {
	// Priority 1 (system + critical) — never truncated.
	assert.Equal(t, PromptPriority(1), PrioritySystem)
	assert.Equal(t, PromptPriority(1), PriorityCritical)
	// Priority 2 — direct messages.
	assert.Equal(t, PromptPriority(2), PriorityHigh)
	// Priority 3 — dependency results + leader instruction.
	assert.Equal(t, PromptPriority(3), PriorityMedium)
	// Priority 4 — broadcast messages.
	assert.Equal(t, PromptPriority(4), PriorityLow)
	// Priority 5 — session summary.
	assert.Equal(t, PromptPriority(5), PriorityLowest)
}

func TestNewPromptBuilder(t *testing.T) {
	mailbox := &stubMailboxReader{}
	pb := NewPromptBuilder("m1", "t1", "coder", nil, mailbox)
	require.NotNil(t, pb)
	assert.Equal(t, "m1", pb.memberID)
	assert.Equal(t, "t1", pb.teamID)
	assert.Equal(t, "coder", pb.role)
	assert.Nil(t, pb.task)
	assert.Equal(t, mailbox, pb.mailbox)
	assert.Equal(t, 0, pb.maxBytes)
}

func TestPromptBuilder_SetMaxBytes(t *testing.T) {
	pb := NewPromptBuilder("m1", "t1", "coder", nil, &stubMailboxReader{})
	assert.Equal(t, 0, pb.maxBytes)

	pb.SetMaxBytes(100000)
	assert.Equal(t, 100000, pb.maxBytes)

	// SetMaxBytes returns self for chaining.
	pb2 := NewPromptBuilder("m2", "t2", "lead", nil, &stubMailboxReader{})
	assert.Same(t, pb2, pb2.SetMaxBytes(5000))
}

// --- T2: section method tests ---

func TestPromptBuilder_systemPolicy(t *testing.T) {
	pb := NewPromptBuilder("m1", "t1", "coder", nil, nil)
	s := pb.systemPolicy()
	assert.Contains(t, s, "AI agent")
	assert.Contains(t, s, "multi-agent team")
	assert.Contains(t, s, "destructive commands")
}

func TestPromptBuilder_memberIdentity(t *testing.T) {
	pb := NewPromptBuilder("member-1", "team-x", "tester", nil, nil)
	s := pb.memberIdentity()
	assert.Contains(t, s, "member-1")
	assert.Contains(t, s, "team-x")
	assert.Contains(t, s, "tester")
}

func TestPromptBuilder_taskText_Nil(t *testing.T) {
	pb := NewPromptBuilder("m1", "t1", "coder", nil, nil)
	s := pb.taskText()
	assert.Contains(t, s, "No current task assigned")
}

func TestPromptBuilder_taskText_WithTask(t *testing.T) {
	task := &TeamTask{
		ID:          "task-1",
		Title:       "Fix login bug",
		Description: "The login form returns 500 when submitting with empty email.",
	}
	pb := NewPromptBuilder("m1", "t1", "coder", task, nil)
	s := pb.taskText()
	assert.Contains(t, s, "Fix login bug")
	assert.Contains(t, s, "login form returns 500")
	// Full text is present — not truncated.
	assert.Contains(t, s, "empty email")
}

func TestPromptBuilder_directMessages_NoMailbox(t *testing.T) {
	pb := NewPromptBuilder("m1", "t1", "coder", nil, nil)
	s := pb.directMessages(context.Background())
	assert.Contains(t, s, "no mailbox available")
}

func TestPromptBuilder_directMessages_Empty(t *testing.T) {
	pb := NewPromptBuilder("m1", "t1", "coder", nil, &stubMailboxReader{})
	s := pb.directMessages(context.Background())
	assert.Contains(t, s, "no unread messages")
}

func TestPromptBuilder_directMessages_WithMessages(t *testing.T) {
	mb := &stubMailboxReader{
		msgs: []MailboxMessage{
			{ID: "msg-1", FromMemberID: "m2", Kind: KindMessage, Summary: "Need help", Payload: "Can you review PR #42?"},
			{ID: "msg-2", FromMemberID: "m3", Kind: KindTaskAssignment, Summary: "", Payload: ""},
		},
	}
	pb := NewPromptBuilder("m1", "t1", "coder", nil, mb)
	s := pb.directMessages(context.Background())

	// Acceptance #6: Mailbox content marked UNTRUSTED PEER INPUT.
	assert.Contains(t, s, "[UNTRUSTED PEER INPUT]")
	// Both messages present.
	assert.Contains(t, s, "From: m2")
	assert.Contains(t, s, "Need help")
	assert.Contains(t, s, "Can you review PR #42?")
	assert.Contains(t, s, "From: m3")
}

func TestPromptBuilder_directMessages_Error(t *testing.T) {
	mb := &stubMailboxReader{err: assert.AnError}
	pb := NewPromptBuilder("m1", "t1", "coder", nil, mb)
	s := pb.directMessages(context.Background())
	assert.Contains(t, s, "mailbox error")
}

func TestPromptBuilder_dependencyResults(t *testing.T) {
	pb := NewPromptBuilder("m1", "t1", "coder", nil, nil)
	s := pb.dependencyResults()
	assert.Contains(t, s, "no dependency results")
}

func TestPromptBuilder_leaderInstruction(t *testing.T) {
	pb := NewPromptBuilder("m1", "t1", "coder", nil, nil)
	s := pb.leaderInstruction()
	assert.Contains(t, s, "no leader instruction")
}

func TestPromptBuilder_broadcastMessages(t *testing.T) {
	pb := NewPromptBuilder("m1", "t1", "coder", nil, nil)
	s := pb.broadcastMessages(context.Background())
	assert.Contains(t, s, "no broadcast messages")
}

func TestPromptBuilder_sessionSummary(t *testing.T) {
	pb := NewPromptBuilder("m1", "t1", "coder", nil, nil)
	s := pb.sessionSummary()
	assert.Contains(t, s, "no session summary")
}

func TestPromptBuilder_reportingRules(t *testing.T) {
	pb := NewPromptBuilder("m1", "t1", "coder", nil, nil)
	s := pb.reportingRules()
	assert.Contains(t, s, "REPORTING RULES")
	assert.Contains(t, s, "mark it as completed")
	assert.Contains(t, s, "silently fail")
}

func TestPromptBuilder_SectionOrder(t *testing.T) {
	// Acceptance #1: section order is fixed and testable.
	pb := NewPromptBuilder("m1", "t1", "coder", nil, &stubMailboxReader{})
	result, err := pb.Build(context.Background())
	require.NoError(t, err)

	// Find the position of each section header in the output.
	systemPos := indexOf(t, result, "[system_policy]")
	identityPos := indexOf(t, result, "[member_identity]")
	taskPos := indexOf(t, result, "[current_task]")
	directPos := indexOf(t, result, "[direct_messages]")
	depPos := indexOf(t, result, "[dependency_results]")
	leaderPos := indexOf(t, result, "[leader_instruction]")
	broadcastPos := indexOf(t, result, "[broadcast_messages]")
	sessionPos := indexOf(t, result, "[session_summary]")
	reportingPos := indexOf(t, result, "[reporting_rules]")

	assert.True(t, systemPos < identityPos, "system_policy before member_identity")
	assert.True(t, identityPos < taskPos, "member_identity before current_task")
	assert.True(t, taskPos < directPos, "current_task before direct_messages")
	assert.True(t, directPos < depPos, "direct_messages before dependency_results")
	assert.True(t, depPos < leaderPos, "dependency_results before leader_instruction")
	assert.True(t, leaderPos < broadcastPos, "leader_instruction before broadcast_messages")
	assert.True(t, broadcastPos < sessionPos, "broadcast_messages before session_summary")
	assert.True(t, sessionPos < reportingPos, "session_summary before reporting_rules")
}

func TestPromptBuilder_DirectBeforeBroadcast(t *testing.T) {
	// Acceptance #3: direct_messages section appears before broadcast_messages.
	mb := &stubMailboxReader{
		msgs: []MailboxMessage{
			{ID: "msg-1", FromMemberID: "m2", Kind: KindMessage, Summary: "hello"},
		},
	}
	pb := NewPromptBuilder("m1", "t1", "coder", nil, mb)
	result, err := pb.Build(context.Background())
	require.NoError(t, err)

	directPos := indexOf(t, result, "[direct_messages]")
	broadcastPos := indexOf(t, result, "[broadcast_messages]")
	assert.True(t, directPos < broadcastPos, "direct_messages before broadcast_messages")
}

func TestPromptBuilder_MailboxUntrustedMarker(t *testing.T) {
	// Acceptance #6: mailbox content marked UNTRUSTED PEER INPUT.
	mb := &stubMailboxReader{
		msgs: []MailboxMessage{
			{ID: "msg-1", FromMemberID: "m2", Kind: KindMessage, Summary: "Look at this"},
		},
	}
	pb := NewPromptBuilder("m1", "t1", "coder", nil, mb)
	result, err := pb.Build(context.Background())
	require.NoError(t, err)

	// UNTRUSTED PEER INPUT appears in the direct_messages section.
	assert.Contains(t, result, "[UNTRUSTED PEER INPUT]")
	// Broadcast section is empty; UNTRUSTED only appears once.
	assert.Equal(t, 1, countSubstr(result, "[UNTRUSTED PEER INPUT]"))
}

// indexOf returns the byte position of substr in s, or t.Fatal if not found.
func indexOf(t *testing.T, s, substr string) int {
	t.Helper()
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return i
		}
	}
	t.Fatalf("substring %q not found", substr)
	return -1
}

// countSubstr counts non-overlapping occurrences of substr in s.
func countSubstr(s, substr string) int {
	n := 0
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			n++
		}
	}
	return n
}

// stubMailboxReader is a minimal stub for testing PromptBuilder without a real
// Service. Returns empty results by default.
type stubMailboxReader struct {
	msgs []MailboxMessage
	err  error
}

func (s *stubMailboxReader) GetUnreadMessages(_ context.Context, _ string, _ int) ([]MailboxMessage, error) {
	if s.err != nil {
		return nil, s.err
	}
	return s.msgs, nil
}
