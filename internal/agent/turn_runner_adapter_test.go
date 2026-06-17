package agent

import (
	"context"
	"errors"
	"testing"

	"charm.land/fantasy"
	"github.com/charmbracelet/crush/internal/actor"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// trackableSA embeds mockSessionAgent (from coordinator_test.go, same package)
// and overrides Cancel + IsSessionBusy to expose per-test tracking state.
type trackableSA struct {
	mockSessionAgent
	canceled []string
	busyFunc func(sessionID string) bool
}

func (m *trackableSA) Cancel(sessionID string) {
	m.canceled = append(m.canceled, sessionID)
}

func (m *trackableSA) IsSessionBusy(sessionID string) bool {
	if m.busyFunc != nil {
		return m.busyFunc(sessionID)
	}
	return false
}

func TestTurnRunnerAdapter_Run_Success(t *testing.T) {
	var runCalls []SessionAgentCall
	mock := &trackableSA{}
	mock.runFunc = func(ctx context.Context, call SessionAgentCall) (*fantasy.AgentResult, error) {
		runCalls = append(runCalls, call)
		return &fantasy.AgentResult{TotalUsage: fantasy.Usage{InputTokens: 100, OutputTokens: 50}}, nil
	}

	adapter := NewTurnRunnerFromSessionAgent(mock)
	result, err := adapter.Run(context.Background(), TeamAgentCall{
		SessionID:      "sess-1",
		PromptEnvelope: "hello",
		Actor:          actor.ActorContext{SessionID: "sess-1", TeamID: "t1", MemberID: "m1"},
	})
	require.NoError(t, err)
	assert.Equal(t, TurnCompleted, result.Status)
	assert.Equal(t, int64(100), result.Result.TotalUsage.InputTokens)
	assert.Equal(t, int64(50), result.Result.TotalUsage.OutputTokens)
	require.Len(t, runCalls, 1)
	assert.Equal(t, "sess-1", runCalls[0].SessionID)
	assert.Equal(t, "hello", runCalls[0].Prompt)
	assert.False(t, runCalls[0].NonInteractive) // member turns need full multi-turn tool loop
}

func TestTurnRunnerAdapter_Run_Error(t *testing.T) {
	mock := &trackableSA{}
	mock.runFunc = func(ctx context.Context, call SessionAgentCall) (*fantasy.AgentResult, error) {
		return nil, errors.New("boom")
	}

	adapter := NewTurnRunnerFromSessionAgent(mock)
	result, err := adapter.Run(context.Background(), TeamAgentCall{
		SessionID: "sess-1", PromptEnvelope: "test",
	})
	assert.Error(t, err)
	assert.Equal(t, TurnFailed, result.Status)
}

func TestTurnRunnerAdapter_Run_InjectsActorContext(t *testing.T) {
	var capturedCtx context.Context
	mock := &trackableSA{}
	mock.runFunc = func(ctx context.Context, call SessionAgentCall) (*fantasy.AgentResult, error) {
		capturedCtx = ctx
		return &fantasy.AgentResult{}, nil
	}

	adapter := NewTurnRunnerFromSessionAgent(mock)
	ac := actor.ActorContext{SessionID: "sess-1", TeamID: "team-1", MemberID: "member-1", MemberRole: "coder"}
	_, err := adapter.Run(context.Background(), TeamAgentCall{
		SessionID:      "sess-1",
		PromptEnvelope: "test",
		Actor:          ac,
	})
	require.NoError(t, err)

	got, ok := actor.FromContext(capturedCtx)
	assert.True(t, ok)
	assert.Equal(t, "team-1", got.TeamID)
	assert.Equal(t, "member-1", got.MemberID)
	assert.Equal(t, "coder", got.MemberRole)
}

func TestTurnRunnerAdapter_Cancel(t *testing.T) {
	mock := &trackableSA{}
	adapter := NewTurnRunnerFromSessionAgent(mock)
	adapter.Cancel("sess-1")
	assert.Equal(t, []string{"sess-1"}, mock.canceled)
}

func TestTurnRunnerAdapter_IsSessionBusy(t *testing.T) {
	mock := &trackableSA{
		busyFunc: func(id string) bool { return id == "sess-1" },
	}
	adapter := NewTurnRunnerFromSessionAgent(mock)
	assert.True(t, adapter.IsSessionBusy("sess-1"))
	assert.False(t, adapter.IsSessionBusy("sess-2"))
}

func TestSessionAgentFactory_BuildRunner(t *testing.T) {
	var calls int
	factory := NewAgentFactory(func() SessionAgent {
		calls++
		return &trackableSA{
			busyFunc: func(id string) bool { return false },
		}
	})
	r1, err := factory.BuildRunner(context.Background(), AgentSpec{AgentType: "test"})
	require.NoError(t, err)
	assert.NotNil(t, r1)
	assert.Equal(t, 1, calls)

	r2, err := factory.BuildRunner(context.Background(), AgentSpec{AgentType: "test"})
	require.NoError(t, err)
	assert.NotNil(t, r2)
	assert.Equal(t, 2, calls)
	assert.NotSame(t, r1, r2)
}
