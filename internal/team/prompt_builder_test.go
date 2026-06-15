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
