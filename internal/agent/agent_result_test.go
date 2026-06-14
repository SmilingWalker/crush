package agent

import (
	"encoding/json"
	"testing"
	"time"

	"charm.land/fantasy"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestAgentToolResult_JSONRoundTrip locks acceptance #1/#2: the structured
// result serializes to legal JSON whose fields survive a round-trip.
func TestAgentToolResult_JSONRoundTrip(t *testing.T) {
	original := AgentToolResult{
		AgentID:           "sess_123",
		AgentType:         "general-purpose",
		Content:           "Found 5 files",
		TotalTokens:       1500,
		TotalToolUseCount: 8,
		TotalDurationMs:   45200,
		Usage: Usage{
			InputTokens:     800,
			OutputTokens:    700,
			CacheReadTokens: 123,
		},
	}

	data, err := json.Marshal(original)
	require.NoError(t, err)
	require.True(t, json.Valid(data), "serialized result must be legal JSON")

	var decoded AgentToolResult
	require.NoError(t, json.Unmarshal(data, &decoded))

	assert.Equal(t, original.AgentID, decoded.AgentID)
	assert.Equal(t, original.AgentType, decoded.AgentType)
	assert.Equal(t, original.Content, decoded.Content)
	assert.Equal(t, original.TotalTokens, decoded.TotalTokens)
	assert.Equal(t, original.TotalToolUseCount, decoded.TotalToolUseCount)
	assert.Equal(t, original.TotalDurationMs, decoded.TotalDurationMs)
	assert.Equal(t, original.Usage, decoded.Usage)
}

// TestCountToolCalls_NoToolCalls locks acceptance #3 for the empty case.
func TestCountToolCalls_NoToolCalls(t *testing.T) {
	result := &fantasy.AgentResult{
		Response: fantasy.Response{
			Content: fantasy.ResponseContent{
				fantasy.TextContent{Text: "just text, no tool calls"},
			},
		},
	}
	assert.Equal(t, 0, countToolCalls(result))
}

// TestCountToolCalls_CountsAcrossSteps locks acceptance #3: tool calls across
// every step are counted exactly once each.
func TestCountToolCalls_CountsAcrossSteps(t *testing.T) {
	result := &fantasy.AgentResult{
		Steps: []fantasy.StepResult{
			{Response: fantasy.Response{Content: fantasy.ResponseContent{
				fantasy.TextContent{Text: "thinking..."},
				fantasy.ToolCallContent{ToolCallID: "c1"},
				fantasy.ToolCallContent{ToolCallID: "c2"},
			}}},
			{Response: fantasy.Response{Content: fantasy.ResponseContent{
				fantasy.ToolCallContent{ToolCallID: "c3"},
			}}},
		},
	}
	assert.Equal(t, 3, countToolCalls(result))
}

// TestBuildAgentToolResult_FromAgentResult locks acceptance #1-#5 together:
// a real-shaped *fantasy.AgentResult is projected into the structured result
// with correct usage tokens, tool-call count, and content text.
func TestBuildAgentToolResult_FromAgentResult(t *testing.T) {
	result := &fantasy.AgentResult{
		Steps: []fantasy.StepResult{
			{Response: fantasy.Response{Content: fantasy.ResponseContent{
				fantasy.ToolCallContent{ToolCallID: "c1"},
			}}},
		},
		Response: fantasy.Response{
			Content: fantasy.ResponseContent{
				fantasy.TextContent{Text: "final answer"},
			},
		},
		TotalUsage: fantasy.Usage{
			InputTokens:     800,
			OutputTokens:    700,
			CacheReadTokens: 50,
		},
	}

	start := time.Now()
	ar := buildAgentToolResult(result, "general-purpose", start, "sess_123")

	assert.Equal(t, "sess_123", ar.AgentID)
	assert.Equal(t, "general-purpose", ar.AgentType)
	assert.Equal(t, "final answer", ar.Content)
	assert.Equal(t, int64(1500), ar.TotalTokens) // 800 + 700
	assert.Equal(t, 1, ar.TotalToolUseCount)
	assert.GreaterOrEqual(t, ar.TotalDurationMs, int64(0))
	assert.Equal(t, int64(800), ar.Usage.InputTokens)
	assert.Equal(t, int64(700), ar.Usage.OutputTokens)
	assert.Equal(t, int64(50), ar.Usage.CacheReadTokens)

	// The built result must itself serialize cleanly (acceptance #1).
	data, err := json.Marshal(ar)
	require.NoError(t, err)
	require.True(t, json.Valid(data))
}
