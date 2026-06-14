package agent

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"charm.land/fantasy"
	"github.com/charmbracelet/crush/internal/config"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestAgentTool_ReturnsStructuredJSON was intentionally removed: the handler
// closure is built inside fantasy.NewParallelAgentTool and exercising it
// end-to-end needs a fully-built SessionAgent + real provider, which is not
// feasible as a focused unit test. The handler contract is gated instead by
// TestAgentTool_HandlerSerializesResult and _HandlerReturnsErrorOnAgentRunFailure
// below, which replay the exact call sequence the handler runs.

// TestAgentTool_HandlerSerializesResult is the focused serialization gate:
// it replays the handler's call sequence (runSubAgentStructured ->
// buildAgentToolResult -> json.Marshal -> NewTextResponse) and asserts the
// resulting ToolResponse.Content is a legal JSON AgentToolResult carrying the
// sub-agent's content and token usage.
func TestAgentTool_HandlerSerializesResult(t *testing.T) {
	const providerID = "test-provider"
	providerCfg := setupProviderConfig(providerID)
	env := testEnv(t)
	coord := newTestCoordinator(t, env, providerID, providerCfg)

	parent, err := env.sessions.Create(t.Context(), "Parent")
	require.NoError(t, err)

	agent := newMockAgent(providerID, 4096, func(_ context.Context, _ SessionAgentCall) (*fantasy.AgentResult, error) {
		return &fantasy.AgentResult{
			Response: fantasy.Response{
				Content: fantasy.ResponseContent{fantasy.TextContent{Text: "sub-agent says hi"}},
			},
			TotalUsage: fantasy.Usage{
				InputTokens:  200,
				OutputTokens: 100,
			},
		}, nil
	})

	start := time.Now()
	_, ar, err := coord.runSubAgentStructured(t.Context(), subAgentParams{
		Agent:          agent,
		SessionID:      parent.ID,
		AgentMessageID: "msg-1",
		ToolCallID:     "call-1",
		Prompt:         "do it",
		SessionTitle:   "Sub",
	})
	require.NoError(t, err)
	require.NotNil(t, ar)

	// Exact handler sequence: build with the sub-session id, marshal, wrap.
	subSessionID := coord.sessions.CreateAgentToolSessionID("msg-1", "call-1")
	result := buildAgentToolResult(ar, config.AgentTask, start, subSessionID)
	data, mErr := json.Marshal(result)
	require.NoError(t, mErr)
	assert.True(t, json.Valid(data))

	// The handler wraps the JSON in a text response.
	toolResp := fantasy.NewTextResponse(string(data))
	assert.False(t, toolResp.IsError)

	var decoded AgentToolResult
	require.NoError(t, json.Unmarshal([]byte(toolResp.Content), &decoded))
	assert.Equal(t, config.AgentTask, decoded.AgentType)
	assert.Equal(t, subSessionID, decoded.AgentID)
	assert.Equal(t, "sub-agent says hi", decoded.Content)
	assert.Equal(t, int64(300), decoded.TotalTokens) // 200 + 100
	assert.Equal(t, int64(200), decoded.Usage.InputTokens)
	assert.Equal(t, int64(100), decoded.Usage.OutputTokens)
}

// TestAgentTool_HandlerReturnsErrorOnAgentRunFailure locks seam #4: when the
// sub-agent fails, the handler returns the error ToolResponse directly, NOT a
// JSON AgentToolResult (ar == nil).
func TestAgentTool_HandlerReturnsErrorOnAgentRunFailure(t *testing.T) {
	const providerID = "test-provider"
	providerCfg := setupProviderConfig(providerID)
	env := testEnv(t)
	coord := newTestCoordinator(t, env, providerID, providerCfg)

	parent, err := env.sessions.Create(t.Context(), "Parent")
	require.NoError(t, err)

	agent := newMockAgent(providerID, 4096, func(_ context.Context, _ SessionAgentCall) (*fantasy.AgentResult, error) {
		return nil, context.DeadlineExceeded
	})

	resp, ar, err := coord.runSubAgentStructured(t.Context(), subAgentParams{
		Agent:          agent,
		SessionID:      parent.ID,
		AgentMessageID: "msg-1",
		ToolCallID:     "call-1",
		Prompt:         "do it",
		SessionTitle:   "Sub",
	})
	require.NoError(t, err) // swallowed into the error response
	assert.Nil(t, ar)
	assert.True(t, resp.IsError)

	// The handler path: because ar == nil, the handler returns resp directly
	// (it does NOT attempt to build/marshal an AgentToolResult).
	assert.Contains(t, resp.Content, "Failed to generate response")
	// And resp.Content is NOT legal AgentToolResult JSON.
	var decoded AgentToolResult
	json.Unmarshal([]byte(resp.Content), &decoded)
	assert.Equal(t, AgentToolResult{}, decoded, "error response must not be AgentToolResult JSON")
}
