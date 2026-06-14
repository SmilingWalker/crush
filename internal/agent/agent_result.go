package agent

import (
	"time"

	"charm.land/fantasy"
)

// AgentToolResult is the structured result of a sub-agent run. It is serialized
// to JSON and embedded in the ToolResponse.Content returned to the parent agent,
// so the parent can read content, token usage, tool-call count, and duration
// without parsing free text.
type AgentToolResult struct {
	AgentID           string `json:"agent_id"`
	AgentType         string `json:"agent_type"`
	Content           string `json:"content"`
	TotalTokens       int64  `json:"total_tokens"`
	TotalToolUseCount int    `json:"total_tool_use_count"`
	TotalDurationMs   int64  `json:"total_duration_ms"`
	Usage             Usage  `json:"usage"`
}

// Usage is the token-usage breakdown surfaced in AgentToolResult. CacheReadTokens
// is omitted from JSON when zero (providers that don't report cache reads).
type Usage struct {
	InputTokens     int64 `json:"input_tokens"`
	OutputTokens    int64 `json:"output_tokens"`
	CacheReadTokens int64 `json:"cache_read_tokens,omitempty"`
}

// buildAgentToolResult projects a *fantasy.AgentResult into the structured
// AgentToolResult. agentType is the configured agent type (e.g. "task"),
// startTime marks when the run began (for duration), and sessionID is the
// sub-session id that ran the agent.
func buildAgentToolResult(
	result *fantasy.AgentResult,
	agentType string,
	startTime time.Time,
	sessionID string,
) AgentToolResult {
	return AgentToolResult{
		AgentID:           sessionID,
		AgentType:         agentType,
		Content:           result.Response.Content.Text(),
		TotalTokens:       result.TotalUsage.InputTokens + result.TotalUsage.OutputTokens,
		TotalToolUseCount: countToolCalls(result),
		TotalDurationMs:   time.Since(startTime).Milliseconds(),
		Usage: Usage{
			InputTokens:     result.TotalUsage.InputTokens,
			OutputTokens:    result.TotalUsage.OutputTokens,
			CacheReadTokens: result.TotalUsage.CacheReadTokens,
		},
	}
}

// countToolCalls counts the number of tool calls the model made across every
// step of the agent run. Each ToolCallContent element counts once.
func countToolCalls(result *fantasy.AgentResult) int {
	count := 0
	for _, step := range result.Steps {
		for _, content := range step.Content {
			if _, ok := content.(fantasy.ToolCallContent); ok {
				count++
			}
		}
	}
	return count
}
