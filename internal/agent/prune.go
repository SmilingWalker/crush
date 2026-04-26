package agent

import (
	"log/slog"
	"strings"

	"github.com/charmbracelet/crush/internal/message"
)

const (
	pruneProtectTurns  = 2
	pruneProtectTokens = 40_000
	pruneMinimum       = 5_000
	pruneMarker        = "[Old tool result content cleared]"
)

var prunableTools = map[string]bool{
	"bash":          true,
	"grep":          true,
	"glob":          true,
	"view":          true,
	"ls":            true,
	"sourcegraph":   true,
	"fetch":         true,
	"agentic_fetch": true,
	"crush_info":    true,
	"crush_logs":    true,
}

var clearableToolUses = map[string]bool{
	"edit":      true,
	"multiedit": true,
	"write":     true,
}

// prune modifies msgs in-place to free context space by replacing old tool
// outputs with a short marker and clearing large edit parameters. It does not
// write to the database — original data is preserved. Returns the estimated
// number of tokens freed.
func prune(msgs []message.Message) int {
	// Build ToolCallID → (msgIndex, partIndex) lookup for clearableToolUses backtracking.
	type callLocation struct {
		msgIndex  int
		partIndex int
	}
	toolCallLoc := make(map[string]callLocation)
	for i, msg := range msgs {
		if msg.Role != message.Assistant {
			continue
		}
		for j, part := range msg.Parts {
			if tc, ok := part.(message.ToolCall); ok {
				toolCallLoc[tc.ID] = callLocation{msgIndex: i, partIndex: j}
			}
		}
	}

	type candidate struct {
		msgIndex  int
		partIndex int
		toolName  string
		tokens    int
	}

	// Phase 1: Collect all candidates via reverse-scan.
	var candidates []candidate // ordered newest-to-oldest
	turns := 0

	for msgIndex := len(msgs) - 1; msgIndex >= 0; msgIndex-- {
		msg := msgs[msgIndex]

		if msg.Role == message.User {
			turns++
		}
		if turns < pruneProtectTurns {
			continue
		}
		if msg.Role == message.Assistant && msg.IsSummaryMessage {
			break
		}

		if msg.Role != message.Tool {
			continue
		}
		for partIndex := len(msg.Parts) - 1; partIndex >= 0; partIndex-- {
			tr, ok := msgs[msgIndex].Parts[partIndex].(message.ToolResult)
			if !ok {
				continue
			}

			toolName := tr.Name

			// Skip protected tools (LSP, agent, MCP, etc.)
			if isProtectedTool(toolName) {
				continue
			}
			if !isPrunableTool(toolName) && !isClearableToolUse(toolName) {
				continue
			}

			candidates = append(candidates, candidate{
				msgIndex:  msgIndex,
				partIndex: partIndex,
				toolName:  toolName,
				tokens:    estimateTokens(tr.Content),
			})
		}
	}

	if len(candidates) == 0 {
		return 0
	}

	// Phase 2: Determine prune targets.
	type pruneTarget struct {
		msgIndex  int
		partIndex int
		toolName  string
	}

	// 2a: Prunable tools — apply token protection zone.
	// Accumulate from newest; anything beyond pruneProtectTokens is a target.
	runningTotal := 0
	var prunableTargets []pruneTarget
	prunableFreed := 0
	for _, c := range candidates {
		if !isPrunableTool(c.toolName) {
			continue
		}
		runningTotal += c.tokens
		if runningTotal > pruneProtectTokens {
			prunableTargets = append(prunableTargets, pruneTarget{
				msgIndex:  c.msgIndex,
				partIndex: c.partIndex,
				toolName:  c.toolName,
			})
			prunableFreed += c.tokens
		}
	}

	// 2b: If nothing exceeded the zone, check if total prunable tokens meet
	// the minimum and prune from oldest.
	if prunableFreed == 0 {
		totalPrunable := 0
		for _, c := range candidates {
			if isPrunableTool(c.toolName) {
				totalPrunable += c.tokens
			}
		}
		if totalPrunable >= pruneMinimum {
			for i := len(candidates) - 1; i >= 0; i-- {
				if isPrunableTool(candidates[i].toolName) {
					prunableTargets = append(prunableTargets, pruneTarget{
						msgIndex:  candidates[i].msgIndex,
						partIndex: candidates[i].partIndex,
						toolName:  candidates[i].toolName,
					})
					prunableFreed += candidates[i].tokens
					if prunableFreed >= pruneMinimum {
						break
					}
				}
			}
		}
	}

	// 2c: Clearable tools — always clear input for candidates (cheap operation).
	var clearableTargets []pruneTarget
	clearableFreed := 0
	for _, c := range candidates {
		if !isClearableToolUse(c.toolName) {
			continue
		}
		clearableTargets = append(clearableTargets, pruneTarget{
			msgIndex:  c.msgIndex,
			partIndex: c.partIndex,
			toolName:  c.toolName,
		})
		// Estimate freed tokens from ToolCall.Input.
		tr := msgs[c.msgIndex].Parts[c.partIndex].(message.ToolResult)
		if loc, ok := toolCallLoc[tr.ToolCallID]; ok {
			tc := msgs[loc.msgIndex].Parts[loc.partIndex].(message.ToolCall)
			clearableFreed += estimateTokens(tc.Input)
		}
	}

	totalFreed := prunableFreed + clearableFreed
	if totalFreed < pruneMinimum {
		return 0
	}

	// Phase 3: Execute pruning in-place.
	// Prunable: replace content with marker.
	for _, tgt := range prunableTargets {
		tr := msgs[tgt.msgIndex].Parts[tgt.partIndex].(message.ToolResult)
		tr.Content = pruneMarker
		tr.Data = ""
		msgs[tgt.msgIndex].Parts[tgt.partIndex] = tr
	}

	// Clearable: clear ToolCall.Input to "{}".
	for _, tgt := range clearableTargets {
		tr := msgs[tgt.msgIndex].Parts[tgt.partIndex].(message.ToolResult)
		// Keep ToolResult as-is (content preserved).
		msgs[tgt.msgIndex].Parts[tgt.partIndex] = tr
		if loc, ok := toolCallLoc[tr.ToolCallID]; ok {
			tc := msgs[loc.msgIndex].Parts[loc.partIndex].(message.ToolCall)
			tc.Input = "{}"
			msgs[loc.msgIndex].Parts[loc.partIndex] = tc
		}
	}

	slog.Debug("prune: executed",
		"candidates", len(candidates),
		"pruned_outputs", len(prunableTargets),
		"cleared_inputs", len(clearableTargets),
		"freed_tokens", totalFreed,
	)

	return totalFreed
}

// estimateTokens returns a rough token count for text (4 chars ≈ 1 token).
func estimateTokens(text string) int {
	return len(text) / 4
}

// isPrunableTool returns true if the tool is eligible for output pruning.
func isPrunableTool(name string) bool {
	return prunableTools[name]
}

// isClearableToolUse returns true if the tool's call input can be cleared.
func isClearableToolUse(name string) bool {
	return clearableToolUses[name]
}

// isProtectedTool returns true if the tool is an LSP tool (protected by prefix).
func isProtectedTool(name string) bool {
	return strings.HasPrefix(name, "lsp_")
}
