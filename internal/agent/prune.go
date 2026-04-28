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
	"view":         true,
	"ls":           true,
	"sourcegraph":  true,
	"fetch":        true,
	"agentic_fetch": true,
	"crush_info":   true,
	"crush_logs":   true,
}

var clearableToolUses = map[string]bool{
	"edit":      true,
	"multiedit": true,
	"write":     true,
}

// ── 共享类型 ──

type toolCallLoc struct {
	msgIndex  int
	partIndex int
}

type candidate struct {
	msgIndex   int
	partIndex  int
	toolName   string
	tokens     int
	isPrunable bool
}

type pruneTarget struct {
	msgIndex   int
	partIndex  int
	isPrunable bool
}

// ── 主入口 ──

// prune 修改 msgs 以释放上下文空间：将旧的可修剪工具输出替换为短标记，
// 将旧的编辑类工具的调用参数清除。不写数据库，原始数据永久保留。
func prune(msgs []message.Message) int {
	callLoc := buildToolCallIndex(msgs)
	candidates := collectCandidates(msgs, callLoc)

	if len(candidates) == 0 {
		return 0
	}

	targets, freed := selectTargets(candidates)
	if len(targets) == 0 {
		return 0
	}

	executePruning(msgs, targets, callLoc, freed)
	return freed
}

// ── 子函数 ──

// buildToolCallIndex 构建 ToolCallID → (msgIndex, partIndex) 查找表。
func buildToolCallIndex(msgs []message.Message) map[string]toolCallLoc {
	loc := make(map[string]toolCallLoc)
	for i, msg := range msgs {
		if msg.Role != message.Assistant {
			continue
		}
		for j, part := range msg.Parts {
			if tc, ok := part.(message.ToolCall); ok {
				loc[tc.ID] = toolCallLoc{msgIndex: i, partIndex: j}
			}
		}
	}
	return loc
}

// collectCandidates 反向扫描消息，收集可裁剪的候选者。
// 跳过最近 pruneProtectTurns 个 turn，遇到 summary 停止。
// prunable 估算 ToolResult.Content，clearable 估算 ToolCall.Input。
// 返回按从新到旧排序的候选者列表。
func collectCandidates(msgs []message.Message, callLoc map[string]toolCallLoc) []candidate {
	var candidates []candidate
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

			if isProtectedTool(toolName) {
				continue
			}

			if isPrunableTool(toolName) {
				candidates = append(candidates, candidate{
					msgIndex:   msgIndex,
					partIndex:  partIndex,
					toolName:   toolName,
					tokens:     estimateTokens(tr.Content),
					isPrunable: true,
				})
			} else if isClearableToolUse(toolName) {
				inputTokens := lookupInputTokens(msgs, callLoc, tr.ToolCallID)
				if inputTokens > 0 {
					candidates = append(candidates, candidate{
						msgIndex:   msgIndex,
						partIndex:  partIndex,
						toolName:   toolName,
						tokens:     inputTokens,
						isPrunable: false,
					})
				}
			}
		}
	}

	slog.Debug("prune: 收集候选者", "candidates", len(candidates))
	return candidates
}

// selectTargets 判断是否触发裁剪，并从最旧的候选者中选定目标。
// 40K 是保护阈值，超出部分 >= 5K 才触发。
func selectTargets(candidates []candidate) ([]pruneTarget, int) {
	totalSavable := 0
	for _, c := range candidates {
		totalSavable += c.tokens
	}

	if totalSavable <= pruneProtectTokens {
		slog.Debug("prune: 未超过保护阈值", "total", totalSavable)
		return nil, 0
	}

	excess := totalSavable - pruneProtectTokens
	if excess < pruneMinimum {
		slog.Debug("prune: 超出不足 5K，跳过", "excess", excess)
		return nil, 0
	}

	// 从最旧的候选者开始裁剪
	var targets []pruneTarget
	freed := 0
	remaining := excess

	for i := len(candidates) - 1; i >= 0 && remaining > 0; i-- {
		c := candidates[i]
		targets = append(targets, pruneTarget{
			msgIndex:   c.msgIndex,
			partIndex:  c.partIndex,
			isPrunable: c.isPrunable,
		})
		freed += c.tokens
		remaining -= c.tokens
	}

	slog.Debug("prune: 选定目标", "targets", len(targets), "freed", freed)
	return targets, freed
}

// executePruning 就地执行裁剪：prunable 替换 Content，clearable 清除 Input。
func executePruning(msgs []message.Message, targets []pruneTarget, callLoc map[string]toolCallLoc, totalFreed int) {
	prunedOutputs := 0
	clearedInputs := 0

	for _, tgt := range targets {
		if tgt.isPrunable {
			tr := msgs[tgt.msgIndex].Parts[tgt.partIndex].(message.ToolResult)
			tr.Content = pruneMarker
			tr.Data = ""
			msgs[tgt.msgIndex].Parts[tgt.partIndex] = tr
			prunedOutputs++
		} else {
			tr := msgs[tgt.msgIndex].Parts[tgt.partIndex].(message.ToolResult)
			if loc, ok := callLoc[tr.ToolCallID]; ok {
				tc := msgs[loc.msgIndex].Parts[loc.partIndex].(message.ToolCall)
				tc.Input = "{}"
				msgs[loc.msgIndex].Parts[loc.partIndex] = tc
				clearedInputs++
			}
		}
	}

	slog.Debug("prune: 执行完成",
		"pruned_outputs", prunedOutputs,
		"cleared_inputs", clearedInputs,
		"total_freed", totalFreed,
	)
}

// ── 工具函数 ──

// lookupInputTokens 查找 ToolCall 并估算其 Input 的 token 数。
func lookupInputTokens(msgs []message.Message, callLoc map[string]toolCallLoc, toolCallID string) int {
	loc, ok := callLoc[toolCallID]
	if !ok {
		return 0
	}
	tc, ok := msgs[loc.msgIndex].Parts[loc.partIndex].(message.ToolCall)
	if !ok {
		return 0
	}
	return estimateTokens(tc.Input)
}

func estimateTokens(text string) int {
	return len(text) / 4
}

func isPrunableTool(name string) bool {
	return prunableTools[name]
}

func isClearableToolUse(name string) bool {
	return clearableToolUses[name]
}

func isProtectedTool(name string) bool {
	return strings.HasPrefix(name, "lsp_")
}
