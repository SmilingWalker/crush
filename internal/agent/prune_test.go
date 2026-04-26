package agent

import (
	"fmt"
	"testing"

	"github.com/charmbracelet/crush/internal/message"
	"github.com/stretchr/testify/assert"
)

// ---------------------------------------------------------------------------
// Test helpers
// ---------------------------------------------------------------------------

type turnDesc struct {
	toolCalls map[string]string // toolName -> toolCallID
	results   map[string]string // toolCallID -> content
}

// buildMessages constructs a slice of messages representing a multi-turn
// conversation where each "turn" is: user -> assistant (with tool calls) ->
// tool (with results).
func buildMessages(turns []turnDesc) []message.Message {
	var msgs []message.Message
	for i, turn := range turns {
		// user message
		msgs = append(msgs, message.Message{
			ID:   fmt.Sprintf("user-%d", i),
			Role: message.User,
			Parts: []message.ContentPart{
				message.TextContent{Text: "do something"},
			},
		})
		// assistant message with tool calls
		var assistantParts []message.ContentPart
		assistantParts = append(assistantParts, message.TextContent{Text: "calling tools"})
		for toolName, callID := range turn.toolCalls {
			assistantParts = append(assistantParts, message.ToolCall{
				ID:       callID,
				Name:     toolName,
				Input:    `{"command":"cat big.log"}`,
				Finished: true,
			})
		}
		msgs = append(msgs, message.Message{
			ID:    fmt.Sprintf("asst-%d", i),
			Role:  message.Assistant,
			Parts: assistantParts,
		})
		// tool message with results
		var toolParts []message.ContentPart
		for callID, content := range turn.results {
			toolParts = append(toolParts, message.ToolResult{
				ToolCallID: callID,
				Name:       callIDToToolName(turn.toolCalls, callID),
				Content:    content,
			})
		}
		msgs = append(msgs, message.Message{
			ID:    fmt.Sprintf("tool-%d", i),
			Role:  message.Tool,
			Parts: toolParts,
		})
	}
	return msgs
}

func callIDToToolName(calls map[string]string, callID string) string {
	for name, id := range calls {
		if id == callID {
			return name
		}
	}
	return ""
}

// bigContent returns an n-byte string of 'x' characters for simulating large
// tool output.
func bigContent(n int) string {
	buf := make([]byte, n)
	for i := range buf {
		buf[i] = 'x'
	}
	return string(buf)
}

// findToolResult scans messages for a ToolResult with the given toolCallID.
func findToolResult(msgs []message.Message, toolCallID string) (message.ToolResult, bool) {
	for _, m := range msgs {
		if m.Role != message.Tool {
			continue
		}
		for _, p := range m.Parts {
			if tr, ok := p.(message.ToolResult); ok && tr.ToolCallID == toolCallID {
				return tr, true
			}
		}
	}
	return message.ToolResult{}, false
}

// findToolCall scans messages for a ToolCall with the given callID.
func findToolCall(msgs []message.Message, callID string) (message.ToolCall, bool) {
	for _, m := range msgs {
		if m.Role != message.Assistant {
			continue
		}
		for _, p := range m.Parts {
			if tc, ok := p.(message.ToolCall); ok && tc.ID == callID {
				return tc, true
			}
		}
	}
	return message.ToolCall{}, false
}

// ---------------------------------------------------------------------------
// Test functions
// ---------------------------------------------------------------------------

// TestPrune_ProtectsRecentTwoTurns verifies that the most recent 2 turns are
// never pruned. 5 turns, each with 20KB bash output (~5K tokens each). Turns
// 4,3 are protected. Turns 0,1,2 are candidates. Total candidate tokens = 15K
// > 5K minimum. Turn 0 should be pruned.
func TestPrune_ProtectsRecentTwoTurns(t *testing.T) {
	output := bigContent(20_000) // ~5K tokens each
	turns := []turnDesc{
		{toolCalls: map[string]string{"bash": "bash-0"}, results: map[string]string{"bash-0": output}},
		{toolCalls: map[string]string{"bash": "bash-1"}, results: map[string]string{"bash-1": output}},
		{toolCalls: map[string]string{"bash": "bash-2"}, results: map[string]string{"bash-2": output}},
		{toolCalls: map[string]string{"bash": "bash-3"}, results: map[string]string{"bash-3": output}},
		{toolCalls: map[string]string{"bash": "bash-4"}, results: map[string]string{"bash-4": output}},
	}
	msgs := buildMessages(turns)

	freed := prune(msgs)
	assert.Greater(t, freed, 0, "prune should free tokens from at least turn 0")

	// Turns 3 and 4 (protected) must NOT be pruned
	tr3, ok := findToolResult(msgs, "bash-3")
	assert.True(t, ok, "turn 3 tool result should exist")
	assert.Equal(t, output, tr3.Content, "turn 3 output should be untouched (protected)")

	tr4, ok := findToolResult(msgs, "bash-4")
	assert.True(t, ok, "turn 4 tool result should exist")
	assert.Equal(t, output, tr4.Content, "turn 4 output should be untouched (protected)")

	// Turn 0 (oldest candidate) should be pruned
	tr0, ok := findToolResult(msgs, "bash-0")
	assert.True(t, ok, "turn 0 tool result should still exist")
	assert.Equal(t, pruneMarker, tr0.Content, "turn 0 output should be replaced with prune marker")
}

// TestPrune_PrunableTools_ContentReplaced verifies that old prunable tool
// output gets its Content replaced with "[tool output pruned]" while recent
// output stays intact. 5 turns, each with 200KB bash (~50K tokens each).
func TestPrune_PrunableTools_ContentReplaced(t *testing.T) {
	output := bigContent(200_000) // ~50K tokens each
	turns := []turnDesc{
		{toolCalls: map[string]string{"bash": "bash-0"}, results: map[string]string{"bash-0": output}},
		{toolCalls: map[string]string{"bash": "bash-1"}, results: map[string]string{"bash-1": output}},
		{toolCalls: map[string]string{"bash": "bash-2"}, results: map[string]string{"bash-2": output}},
		{toolCalls: map[string]string{"bash": "bash-3"}, results: map[string]string{"bash-3": output}},
		{toolCalls: map[string]string{"bash": "bash-4"}, results: map[string]string{"bash-4": output}},
	}
	msgs := buildMessages(turns)

	freed := prune(msgs)
	assert.Greater(t, freed, 0, "prune should free tokens")

	// Old bash output (turn 0) should be replaced with marker
	tr0, ok := findToolResult(msgs, "bash-0")
	assert.True(t, ok)
	assert.Equal(t, pruneMarker, tr0.Content, "old bash output should be replaced with prune marker")

	// Recent bash output (turn 4, protected) should NOT be pruned
	tr4, ok := findToolResult(msgs, "bash-4")
	assert.True(t, ok)
	assert.Equal(t, output, tr4.Content, "recent bash output should NOT be pruned")
}

// TestPrune_ClearableTools_InputCleared verifies that clearable tools (edit,
// multiedit, write) have their ToolCall.Input cleared to "{}" while their
// ToolResult.Content is kept intact.
func TestPrune_ClearableTools_InputCleared(t *testing.T) {
	bigOutput := bigContent(200_000) // ~50K tokens
	turns := []turnDesc{
		// Turn 0: bash 200KB (50K tokens) -- pushes total past 40K
		{toolCalls: map[string]string{"bash": "bash-0"}, results: map[string]string{"bash-0": bigOutput}},
		// Turn 1: edit with small result
		{toolCalls: map[string]string{"edit": "edit-1"}, results: map[string]string{"edit-1": "file edited successfully"}},
		// Turn 2: bash 200KB -- in token protection zone
		{toolCalls: map[string]string{"bash": "bash-2"}, results: map[string]string{"bash-2": bigOutput}},
		// Turn 3: bash 200KB -- protected by turns < 2
		{toolCalls: map[string]string{"bash": "bash-3"}, results: map[string]string{"bash-3": bigOutput}},
	}
	msgs := buildMessages(turns)

	freed := prune(msgs)
	assert.Greater(t, freed, 0, "prune should free tokens")

	// Edit ToolResult.Content should be KEPT
	tr1, ok := findToolResult(msgs, "edit-1")
	assert.True(t, ok, "edit tool result should exist")
	assert.Equal(t, "file edited successfully", tr1.Content, "edit ToolResult.Content should be KEPT")

	// Edit ToolCall.Input should be CLEARED to "{}"
	tc1, ok := findToolCall(msgs, "edit-1")
	assert.True(t, ok, "edit tool call should exist")
	assert.Equal(t, "{}", tc1.Input, "edit ToolCall.Input should be CLEARED to {}")
}

// TestPrune_ProtectedTools_NotModified verifies that protected tools (those
// with "lsp_" prefix) are never modified regardless of their size.
func TestPrune_ProtectedTools_NotModified(t *testing.T) {
	bigOutput := bigContent(200_000) // ~50K tokens
	turns := []turnDesc{
		// Turn 0: agent (lsp_*) 200KB -- should NOT be pruned
		{toolCalls: map[string]string{"lsp_diagnostics": "lsp-0"}, results: map[string]string{"lsp-0": bigOutput}},
		// Turn 1: bash 200KB
		{toolCalls: map[string]string{"bash": "bash-1"}, results: map[string]string{"bash-1": bigOutput}},
		// Turn 2: bash 200KB
		{toolCalls: map[string]string{"bash": "bash-2"}, results: map[string]string{"bash-2": bigOutput}},
		// Turn 3-4: bash 200KB (protected by turns)
		{toolCalls: map[string]string{"bash": "bash-3"}, results: map[string]string{"bash-3": bigOutput}},
		{toolCalls: map[string]string{"bash": "bash-4"}, results: map[string]string{"bash-4": bigOutput}},
	}
	msgs := buildMessages(turns)

	freed := prune(msgs)
	assert.Greater(t, freed, 0, "prune should free tokens from non-protected tools")

	// LSP tool output should be unchanged regardless of size
	tr0, ok := findToolResult(msgs, "lsp-0")
	assert.True(t, ok, "lsp tool result should exist")
	assert.Equal(t, bigOutput, tr0.Content, "protected lsp tool output should NOT be modified")
}

// TestPrune_MinimumThreshold verifies that prune does nothing when total
// candidate tokens are below the 5K minimum. 3 turns, each with 100-byte
// bash output.
func TestPrune_MinimumThreshold(t *testing.T) {
	tinyOutput := bigContent(100) // ~25 tokens each, 3 turns = ~75 tokens total
	turns := []turnDesc{
		{toolCalls: map[string]string{"bash": "bash-0"}, results: map[string]string{"bash-0": tinyOutput}},
		{toolCalls: map[string]string{"bash": "bash-1"}, results: map[string]string{"bash-1": tinyOutput}},
		{toolCalls: map[string]string{"bash": "bash-2"}, results: map[string]string{"bash-2": tinyOutput}},
	}
	msgs := buildMessages(turns)

	freed := prune(msgs)
	assert.Equal(t, 0, freed, "prune should free 0 tokens when below minimum threshold")

	// Nothing should be pruned; all content should remain unchanged
	for i := 0; i < 3; i++ {
		id := fmt.Sprintf("bash-%d", i)
		tr, ok := findToolResult(msgs, id)
		assert.True(t, ok, "turn %d tool result should exist", i)
		assert.Equal(t, tinyOutput, tr.Content, "turn %d output should be unchanged", i)
	}
}

// TestPrune_SummaryBoundary verifies that messages before a summary message
// (IsSummaryMessage=true) are NOT pruned. Manually constructs: turn 0 (bash
// 60KB) -> summary assistant message -> turn 1 (bash 60KB).
func TestPrune_SummaryBoundary(t *testing.T) {
	output := bigContent(60_000) // ~15K tokens

	msgs := []message.Message{
		// Turn 0: user
		{
			ID:   "user-0",
			Role: message.User,
			Parts: []message.ContentPart{
				message.TextContent{Text: "do something"},
			},
		},
		// Turn 0: assistant with bash call
		{
			ID:   "asst-0",
			Role: message.Assistant,
			Parts: []message.ContentPart{
				message.TextContent{Text: "running bash"},
				message.ToolCall{
					ID:       "bash-0",
					Name:     "bash",
					Input:    `{"command":"cat big.log"}`,
					Finished: true,
				},
			},
		},
		// Turn 0: tool result
		{
			ID:   "tool-0",
			Role: message.Tool,
			Parts: []message.ContentPart{
				message.ToolResult{
					ToolCallID: "bash-0",
					Name:       "bash",
					Content:    output,
				},
			},
		},
		// Summary message (assistant, IsSummaryMessage=true)
		{
			ID:   "asst-summary",
			Role: message.Assistant,
			Parts: []message.ContentPart{
				message.TextContent{Text: "Summary of previous conversation..."},
			},
			IsSummaryMessage: true,
		},
		// Turn 1: user
		{
			ID:   "user-1",
			Role: message.User,
			Parts: []message.ContentPart{
				message.TextContent{Text: "continue"},
			},
		},
		// Turn 1: assistant with bash call
		{
			ID:   "asst-1",
			Role: message.Assistant,
			Parts: []message.ContentPart{
				message.TextContent{Text: "running bash again"},
				message.ToolCall{
					ID:       "bash-1",
					Name:     "bash",
					Input:    `{"command":"cat big2.log"}`,
					Finished: true,
				},
			},
		},
		// Turn 1: tool result
		{
			ID:   "tool-1",
			Role: message.Tool,
			Parts: []message.ContentPart{
				message.ToolResult{
					ToolCallID: "bash-1",
					Name:       "bash",
					Content:    output,
				},
			},
		},
	}

	prune(msgs)

	// Pre-summary output should NOT be pruned
	tr0, ok := findToolResult(msgs, "bash-0")
	assert.True(t, ok, "pre-summary tool result should exist")
	assert.Equal(t, output, tr0.Content, "pre-summary output should NOT be pruned")
}

// TestPrune_TokenProtection verifies the 40K token protection zone. 3 turns:
// turn 0 = 200KB bash (50K tokens), turn 1 = 80KB bash (20K tokens), turn 2 =
// 80KB bash (20K tokens). Reverse scan: turn 2 (20K) + turn 1 (20K) = 40K <=
// protection zone -> protected. Turn 0 (50K) pushes to 90K -> pruned.
func TestPrune_TokenProtection(t *testing.T) {
	turns := []turnDesc{
		{toolCalls: map[string]string{"bash": "bash-0"}, results: map[string]string{"bash-0": bigContent(200_000)}}, // ~50K tokens
		{toolCalls: map[string]string{"bash": "bash-1"}, results: map[string]string{"bash-1": bigContent(80_000)}},  // ~20K tokens
		{toolCalls: map[string]string{"bash": "bash-2"}, results: map[string]string{"bash-2": bigContent(80_000)}},  // ~20K tokens
	}
	msgs := buildMessages(turns)

	freed := prune(msgs)
	assert.Greater(t, freed, 0, "prune should free tokens from turn 0")

	// Turn 0 (50K tokens, pushes past protection zone) should be pruned
	tr0, ok := findToolResult(msgs, "bash-0")
	assert.True(t, ok)
	assert.Equal(t, pruneMarker, tr0.Content, "turn 0 should be pruned (pushes past 40K protection zone)")

	// Turns 1 and 2 (within protection zone) should NOT be pruned
	tr1, ok := findToolResult(msgs, "bash-1")
	assert.True(t, ok)
	assert.Equal(t, bigContent(80_000), tr1.Content, "turn 1 should NOT be pruned (within protection zone)")

	tr2, ok := findToolResult(msgs, "bash-2")
	assert.True(t, ok)
	assert.Equal(t, bigContent(80_000), tr2.Content, "turn 2 should NOT be pruned (within protection zone)")
}
