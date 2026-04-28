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

// buildMessages 构造多轮对话消息：每轮 = user -> assistant(tool calls) -> tool(results)。
func buildMessages(turns []turnDesc) []message.Message {
	var msgs []message.Message
	for i, turn := range turns {
		msgs = append(msgs, message.Message{
			ID:   fmt.Sprintf("user-%d", i),
			Role: message.User,
			Parts: []message.ContentPart{
				message.TextContent{Text: "do something"},
			},
		})
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

func bigContent(n int) string {
	buf := make([]byte, n)
	for i := range buf {
		buf[i] = 'x'
	}
	return string(buf)
}

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
// Tests
// ---------------------------------------------------------------------------

// TestPrune_ProtectsRecentTwoTurns 验证最近 2 个 turn 不被裁剪。
// 5 turns × 80KB (20K tokens)。Turns 4,3 受保护。
// Candidates: turns 2,1,0 = 60K > 40K, excess = 20K >= 5K。
// 从最旧开始裁剪：turn 0 (20K) 释放后 remaining=0，停止。
func TestPrune_ProtectsRecentTwoTurns(t *testing.T) {
	output := bigContent(80_000) // ~20K tokens each
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

	// Turns 3 和 4 受 turn 保护，不应被裁剪
	tr3, ok := findToolResult(msgs, "bash-3")
	assert.True(t, ok)
	assert.Equal(t, output, tr3.Content, "turn 3 应受保护")

	tr4, ok := findToolResult(msgs, "bash-4")
	assert.True(t, ok)
	assert.Equal(t, output, tr4.Content, "turn 4 应受保护")

	// Turn 0（最旧候选者）应被裁剪
	tr0, ok := findToolResult(msgs, "bash-0")
	assert.True(t, ok)
	assert.Equal(t, pruneMarker, tr0.Content, "turn 0 应被替换为标记")
}

// TestPrune_PrunableTools_ContentReplaced 验证旧的可修剪工具输出被替换为标记。
// 5 turns × 200KB (50K tokens)。Candidates: turns 2,1,0 = 150K > 40K。
func TestPrune_PrunableTools_ContentReplaced(t *testing.T) {
	output := bigContent(200_000)
	turns := []turnDesc{
		{toolCalls: map[string]string{"bash": "bash-0"}, results: map[string]string{"bash-0": output}},
		{toolCalls: map[string]string{"bash": "bash-1"}, results: map[string]string{"bash-1": output}},
		{toolCalls: map[string]string{"bash": "bash-2"}, results: map[string]string{"bash-2": output}},
		{toolCalls: map[string]string{"bash": "bash-3"}, results: map[string]string{"bash-3": output}},
		{toolCalls: map[string]string{"bash": "bash-4"}, results: map[string]string{"bash-4": output}},
	}
	msgs := buildMessages(turns)

	freed := prune(msgs)
	assert.Greater(t, freed, 0)

	// Turn 0（最旧）应被替换
	tr0, ok := findToolResult(msgs, "bash-0")
	assert.True(t, ok)
	assert.Equal(t, pruneMarker, tr0.Content)

	// Turn 4（受保护）不应被裁剪
	tr4, ok := findToolResult(msgs, "bash-4")
	assert.True(t, ok)
	assert.Equal(t, output, tr4.Content)
}

// TestPrune_ClearableTools_InputCleared 验证 clearable 工具的 ToolCall.Input 被清除，
// 同时 ToolResult.Content 保持不变。
// 构造 3 turns：turn 0 有一个 edit（Input 200KB ≈ 50K tokens），turn 1 和 2 用 bash 填充。
// turn 2 受 turn 保护，candidates = edit-0 的 Input (~50K)。
// total = 50K > 40K, excess = 10K >= 5K, edit-0 应被清除。
func TestPrune_ClearableTools_InputCleared(t *testing.T) {
	largeInput := `{"path":"file.go","old_string":"` + bigContent(200_000) + `","new_string":"fixed"}`

	msgs := []message.Message{
		// Turn 0: user
		{ID: "user-0", Role: message.User, Parts: []message.ContentPart{message.TextContent{Text: "edit"}}},
		// Turn 0: assistant with edit call (large Input)
		{ID: "asst-0", Role: message.Assistant, Parts: []message.ContentPart{
			message.TextContent{Text: "editing"},
			message.ToolCall{ID: "edit-0", Name: "edit", Input: largeInput, Finished: true},
		}},
		// Turn 0: tool result (small)
		{ID: "tool-0", Role: message.Tool, Parts: []message.ContentPart{
			message.ToolResult{ToolCallID: "edit-0", Name: "edit", Content: "file edited successfully"},
		}},
		// Turn 1: user + bash（用于占位，使 turn 0 成为非保护的候选者）
		{ID: "user-1", Role: message.User, Parts: []message.ContentPart{message.TextContent{Text: "run"}}},
		{ID: "asst-1", Role: message.Assistant, Parts: []message.ContentPart{
			message.TextContent{Text: "running"},
			message.ToolCall{ID: "bash-1", Name: "bash", Input: `{"cmd":"ls"}`, Finished: true},
		}},
		{ID: "tool-1", Role: message.Tool, Parts: []message.ContentPart{
			message.ToolResult{ToolCallID: "bash-1", Name: "bash", Content: "output"},
		}},
		// Turn 2: user + bash（受 turn 保护）
		{ID: "user-2", Role: message.User, Parts: []message.ContentPart{message.TextContent{Text: "run again"}}},
		{ID: "asst-2", Role: message.Assistant, Parts: []message.ContentPart{
			message.TextContent{Text: "running"},
			message.ToolCall{ID: "bash-2", Name: "bash", Input: `{"cmd":"ls"}`, Finished: true},
		}},
		{ID: "tool-2", Role: message.Tool, Parts: []message.ContentPart{
			message.ToolResult{ToolCallID: "bash-2", Name: "bash", Content: "output"},
		}},
	}

	freed := prune(msgs)
	assert.Greater(t, freed, 0, "prune should free tokens from edit-0 Input")

	// ToolResult.Content 应保持不变
	tr0, ok := findToolResult(msgs, "edit-0")
	assert.True(t, ok)
	assert.Equal(t, "file edited successfully", tr0.Content, "edit ToolResult.Content 应保留")

	// ToolCall.Input 应被清除为 "{}"
	tc0, ok := findToolCall(msgs, "edit-0")
	assert.True(t, ok)
	assert.Equal(t, "{}", tc0.Input, "edit ToolCall.Input 应被清除为 {}")
}

// TestPrune_ProtectedTools_NotModified 验证 lsp_* 前缀的工具不被修改。
func TestPrune_ProtectedTools_NotModified(t *testing.T) {
	bigOutput := bigContent(200_000) // ~50K tokens
	turns := []turnDesc{
		{toolCalls: map[string]string{"lsp_diagnostics": "lsp-0"}, results: map[string]string{"lsp-0": bigOutput}},
		{toolCalls: map[string]string{"bash": "bash-1"}, results: map[string]string{"bash-1": bigOutput}},
		{toolCalls: map[string]string{"bash": "bash-2"}, results: map[string]string{"bash-2": bigOutput}},
		{toolCalls: map[string]string{"bash": "bash-3"}, results: map[string]string{"bash-3": bigOutput}},
		{toolCalls: map[string]string{"bash": "bash-4"}, results: map[string]string{"bash-4": bigOutput}},
	}
	msgs := buildMessages(turns)

	freed := prune(msgs)
	assert.Greater(t, freed, 0)

	// LSP 工具输出不应被修改
	tr0, ok := findToolResult(msgs, "lsp-0")
	assert.True(t, ok)
	assert.Equal(t, bigOutput, tr0.Content, "lsp 工具输出不应被修改")
}

// TestPrune_FiveKTrigger 验证 excess >= 5K 时触发裁剪。
// 5 turns: 15K + 25K + 25K + protected + protected = 65K candidates。
// excess = 65K - 40K = 25K >= 5K。
func TestPrune_FiveKTrigger(t *testing.T) {
	turns := []turnDesc{
		{toolCalls: map[string]string{"bash": "bash-0"}, results: map[string]string{"bash-0": bigContent(60_000)}},
		{toolCalls: map[string]string{"bash": "bash-1"}, results: map[string]string{"bash-1": bigContent(100_000)}},
		{toolCalls: map[string]string{"bash": "bash-2"}, results: map[string]string{"bash-2": bigContent(100_000)}},
		{toolCalls: map[string]string{"bash": "bash-3"}, results: map[string]string{"bash-3": bigContent(100_000)}},
		{toolCalls: map[string]string{"bash": "bash-4"}, results: map[string]string{"bash-4": bigContent(100_000)}},
	}
	msgs := buildMessages(turns)

	freed := prune(msgs)
	assert.Greater(t, freed, 0, "excess 25K >= 5K，应触发裁剪")

	tr0, ok := findToolResult(msgs, "bash-0")
	assert.True(t, ok)
	assert.Equal(t, pruneMarker, tr0.Content, "turn 0 应被裁剪")
}

// TestPrune_FiveKTrigger_NoPrune 验证 excess < 5K 时不触发裁剪。
// 3 turns: 1K + 20K + 20K = 41K, excess = 1K < 5K。
func TestPrune_FiveKTrigger_NoPrune(t *testing.T) {
	turns := []turnDesc{
		{toolCalls: map[string]string{"bash": "bash-0"}, results: map[string]string{"bash-0": bigContent(4_000)}},
		{toolCalls: map[string]string{"bash": "bash-1"}, results: map[string]string{"bash-1": bigContent(80_000)}},
		{toolCalls: map[string]string{"bash": "bash-2"}, results: map[string]string{"bash-2": bigContent(80_000)}},
	}
	msgs := buildMessages(turns)

	freed := prune(msgs)
	assert.Equal(t, 0, freed, "excess 1K < 5K，不应触发裁剪")

	for i := 0; i < 3; i++ {
		id := fmt.Sprintf("bash-%d", i)
		tr, ok := findToolResult(msgs, id)
		assert.True(t, ok)
		assert.NotEqual(t, pruneMarker, tr.Content, "turn %d 不应被裁剪", i)
	}
}

// TestPrune_MinimumThreshold 验证 total < 40K 时不裁剪。
func TestPrune_MinimumThreshold(t *testing.T) {
	tinyOutput := bigContent(100) // ~25 tokens each
	turns := []turnDesc{
		{toolCalls: map[string]string{"bash": "bash-0"}, results: map[string]string{"bash-0": tinyOutput}},
		{toolCalls: map[string]string{"bash": "bash-1"}, results: map[string]string{"bash-1": tinyOutput}},
		{toolCalls: map[string]string{"bash": "bash-2"}, results: map[string]string{"bash-2": tinyOutput}},
	}
	msgs := buildMessages(turns)

	freed := prune(msgs)
	assert.Equal(t, 0, freed, "total ~75 tokens << 40K，不应裁剪")

	for i := 0; i < 3; i++ {
		id := fmt.Sprintf("bash-%d", i)
		tr, ok := findToolResult(msgs, id)
		assert.True(t, ok)
		assert.Equal(t, tinyOutput, tr.Content)
	}
}

// TestPrune_SummaryBoundary 验证 summary 消息之前的内容不被裁剪。
func TestPrune_SummaryBoundary(t *testing.T) {
	output := bigContent(60_000)

	msgs := []message.Message{
		{ID: "user-0", Role: message.User, Parts: []message.ContentPart{message.TextContent{Text: "do something"}}},
		{ID: "asst-0", Role: message.Assistant, Parts: []message.ContentPart{
			message.TextContent{Text: "running bash"},
			message.ToolCall{ID: "bash-0", Name: "bash", Input: `{"command":"cat big.log"}`, Finished: true},
		}},
		{ID: "tool-0", Role: message.Tool, Parts: []message.ContentPart{
			message.ToolResult{ToolCallID: "bash-0", Name: "bash", Content: output},
		}},
		// Summary 消息
		{ID: "asst-summary", Role: message.Assistant, Parts: []message.ContentPart{
			message.TextContent{Text: "Summary..."},
		}, IsSummaryMessage: true},
		{ID: "user-1", Role: message.User, Parts: []message.ContentPart{message.TextContent{Text: "continue"}}},
		{ID: "asst-1", Role: message.Assistant, Parts: []message.ContentPart{
			message.TextContent{Text: "running bash again"},
			message.ToolCall{ID: "bash-1", Name: "bash", Input: `{"command":"cat big2.log"}`, Finished: true},
		}},
		{ID: "tool-1", Role: message.Tool, Parts: []message.ContentPart{
			message.ToolResult{ToolCallID: "bash-1", Name: "bash", Content: output},
		}},
	}

	prune(msgs)

	// Summary 之前的输出不应被裁剪
	tr0, ok := findToolResult(msgs, "bash-0")
	assert.True(t, ok)
	assert.Equal(t, output, tr0.Content, "summary 之前的输出不应被裁剪")
}

// TestPrune_TokenProtection 验证 40K 保护阈值。
// 3 turns: 50K + 20K + 20K = 90K, excess = 50K。
// 从最旧裁剪 50K: turn 0 (50K) 正好覆盖 excess。
func TestPrune_TokenProtection(t *testing.T) {
	turns := []turnDesc{
		{toolCalls: map[string]string{"bash": "bash-0"}, results: map[string]string{"bash-0": bigContent(200_000)}},
		{toolCalls: map[string]string{"bash": "bash-1"}, results: map[string]string{"bash-1": bigContent(80_000)}},
		{toolCalls: map[string]string{"bash": "bash-2"}, results: map[string]string{"bash-2": bigContent(80_000)}},
	}
	msgs := buildMessages(turns)

	freed := prune(msgs)
	assert.Greater(t, freed, 0)

	// Turn 0 (50K) 应被裁剪
	tr0, ok := findToolResult(msgs, "bash-0")
	assert.True(t, ok)
	assert.Equal(t, pruneMarker, tr0.Content, "turn 0 应被裁剪")

	// Turns 1 和 2 不应被裁剪
	tr1, ok := findToolResult(msgs, "bash-1")
	assert.True(t, ok)
	assert.Equal(t, bigContent(80_000), tr1.Content, "turn 1 不应被裁剪")

	tr2, ok := findToolResult(msgs, "bash-2")
	assert.True(t, ok)
	assert.Equal(t, bigContent(80_000), tr2.Content, "turn 2 不应被裁剪")
}

// TestPrune_Unified_ClearableCountedAndCleared 验证 clearable 的 Input
// 被计入 totalSavableTokens 并正确裁剪。
// 3 turns：turn 0 是 edit（Input 200KB ≈ 50K tokens），turn 1/2 是 bash 占位。
// Turn 2 受保护，candidates 只有 edit-0 的 Input。
// total = 50K > 40K, excess = 10K >= 5K。
func TestPrune_Unified_ClearableCountedAndCleared(t *testing.T) {
	largeInput := `{"path":"file.go","old_string":"` + bigContent(200_000) + `","new_string":"fixed"}`

	msgs := []message.Message{
		// Turn 0: edit (large Input)
		{ID: "user-0", Role: message.User, Parts: []message.ContentPart{message.TextContent{Text: "edit"}}},
		{ID: "asst-0", Role: message.Assistant, Parts: []message.ContentPart{
			message.TextContent{Text: "editing"},
			message.ToolCall{ID: "edit-0", Name: "edit", Input: largeInput, Finished: true},
		}},
		{ID: "tool-0", Role: message.Tool, Parts: []message.ContentPart{
			message.ToolResult{ToolCallID: "edit-0", Name: "edit", Content: "ok"},
		}},
		// Turn 1: bash 占位
		{ID: "user-1", Role: message.User, Parts: []message.ContentPart{message.TextContent{Text: "run"}}},
		{ID: "asst-1", Role: message.Assistant, Parts: []message.ContentPart{
			message.TextContent{Text: "running"},
			message.ToolCall{ID: "bash-1", Name: "bash", Input: `{"cmd":"ls"}`, Finished: true},
		}},
		{ID: "tool-1", Role: message.Tool, Parts: []message.ContentPart{
			message.ToolResult{ToolCallID: "bash-1", Name: "bash", Content: "output"},
		}},
		// Turn 2: bash 占位（受保护）
		{ID: "user-2", Role: message.User, Parts: []message.ContentPart{message.TextContent{Text: "run"}}},
		{ID: "asst-2", Role: message.Assistant, Parts: []message.ContentPart{
			message.TextContent{Text: "running"},
			message.ToolCall{ID: "bash-2", Name: "bash", Input: `{"cmd":"ls"}`, Finished: true},
		}},
		{ID: "tool-2", Role: message.Tool, Parts: []message.ContentPart{
			message.ToolResult{ToolCallID: "bash-2", Name: "bash", Content: "output"},
		}},
	}

	freed := prune(msgs)
	assert.Greater(t, freed, 0, "edit Input ~50K > 40K, excess 10K >= 5K, 应触发裁剪")

	// edit-0 的 ToolResult.Content 保持不变
	tr0, ok := findToolResult(msgs, "edit-0")
	assert.True(t, ok)
	assert.Equal(t, "ok", tr0.Content, "edit ToolResult.Content 应保留")

	// edit-0 的 ToolCall.Input 应被清除
	tc0, ok := findToolCall(msgs, "edit-0")
	assert.True(t, ok)
	assert.Equal(t, "{}", tc0.Input, "edit ToolCall.Input 应被清除为 {}")
}

// TestPrune_Unified_ClearableBelowThreshold 验证 clearable 在 total < 40K 时不被清除。
// 3 turns: turn 0 是 edit（小 Input），total 远小于 40K。
func TestPrune_Unified_ClearableBelowThreshold(t *testing.T) {
	msgs := []message.Message{
		// Turn 0: edit (small Input)
		{ID: "user-0", Role: message.User, Parts: []message.ContentPart{message.TextContent{Text: "edit"}}},
		{ID: "asst-0", Role: message.Assistant, Parts: []message.ContentPart{
			message.TextContent{Text: "editing"},
			message.ToolCall{ID: "edit-0", Name: "edit", Input: `{"path":"f.go","old":"a","new":"b"}`, Finished: true},
		}},
		{ID: "tool-0", Role: message.Tool, Parts: []message.ContentPart{
			message.ToolResult{ToolCallID: "edit-0", Name: "edit", Content: "ok"},
		}},
		// Turn 1: bash 占位
		{ID: "user-1", Role: message.User, Parts: []message.ContentPart{message.TextContent{Text: "run"}}},
		{ID: "asst-1", Role: message.Assistant, Parts: []message.ContentPart{
			message.TextContent{Text: "running"},
			message.ToolCall{ID: "bash-1", Name: "bash", Input: `{"cmd":"ls"}`, Finished: true},
		}},
		{ID: "tool-1", Role: message.Tool, Parts: []message.ContentPart{
			message.ToolResult{ToolCallID: "bash-1", Name: "bash", Content: "output"},
		}},
		// Turn 2: bash 占位（受保护）
		{ID: "user-2", Role: message.User, Parts: []message.ContentPart{message.TextContent{Text: "run"}}},
		{ID: "asst-2", Role: message.Assistant, Parts: []message.ContentPart{
			message.TextContent{Text: "running"},
			message.ToolCall{ID: "bash-2", Name: "bash", Input: `{"cmd":"ls"}`, Finished: true},
		}},
		{ID: "tool-2", Role: message.Tool, Parts: []message.ContentPart{
			message.ToolResult{ToolCallID: "bash-2", Name: "bash", Content: "output"},
		}},
	}

	// total ≈ edit Input (~40 bytes) + bash-1 Content (~24 bytes) = ~16 tokens << 40K
	freed := prune(msgs)
	assert.Equal(t, 0, freed, "total << 40K, 不应触发裁剪")

	// edit Input 不应被清除
	tc0, ok := findToolCall(msgs, "edit-0")
	assert.True(t, ok)
	assert.NotEqual(t, "{}", tc0.Input, "edit Input 不应被清除")
}
