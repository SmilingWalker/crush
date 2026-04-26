# Tool Prune Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add a proactive, in-memory tool output pruning mechanism to Crush's agent loop that reduces token usage without losing original data.

**Architecture:** A standalone `prune()` function in `internal/agent/prune.go` modifies the `[]message.Message` slice in-place before `preparePrompt()` converts it to API messages. No database writes. Three-tier tool classification (prunable / clearable-use / protected) with triple protection (2 turns + 40K tokens + summary boundary).

**Tech Stack:** Go, using existing `message` package types (`message.Message`, `message.ToolCall`, `message.ToolResult`, `message.ContentPart`).

---

## File Structure

| File | Responsibility |
|------|---------------|
| `internal/agent/prune.go` | **New**: Constants, tool classification maps, `prune()` function, `estimateTokens()` helper |
| `internal/agent/prune_test.go` | **New**: Unit tests for all prune behaviors |
| `internal/agent/agent.go` | **Modified**: Add 1-line `prune(msgs)` call in `Run()` |

---

### Task 1: Create prune.go with constants and classification maps

**Files:**
- Create: `internal/agent/prune.go`

- [ ] **Step 1: Create the file with constants, maps, and stub function**

```go
package agent

import (
	"strings"

	"github.com/charmbracelet/crush/internal/message"
)

const (
	pruneProtectTurns  = 2
	pruneProtectTokens = 40_000
	pruneMinimum       = 5_000
	pruneMarker        = "[tool output pruned]"
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
	return 0
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
```

- [ ] **Step 2: Verify it compiles**

Run: `cd /g/ai-project/expert-paper/agent-init/crush && go build ./internal/agent/...`
Expected: compiles with no errors

- [ ] **Step 3: Commit**

```bash
git add internal/agent/prune.go
git commit -m "feat(prune): add tool classification maps and stub function"
```

---

### Task 2: Write failing tests for prune core behaviors

**Files:**
- Create: `internal/agent/prune_test.go`

- [ ] **Step 1: Create the test file with helper and first test**

The test helper `buildMessages` constructs a `[]message.Message` slice simulating a conversation. Each "turn" is: user message → assistant message (with ToolCalls) → tool message (with ToolResults).

```go
package agent

import (
	"fmt"
	"testing"

	"github.com/charmbracelet/crush/internal/message"
	"github.com/stretchr/testify/assert"
)

// buildMessages constructs a message list from turn descriptions.
// Each turn is: user → assistant(toolCalls) → tool(results).
// toolCalls maps toolName→toolCallID. results maps toolCallID→content.
type turnDesc struct {
	toolCalls map[string]string // toolName → toolCallID
	results   map[string]string // toolCallID → content
}

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

// bigContent generates a string of approximately n bytes.
func bigContent(n int) string {
	buf := make([]byte, n)
	for i := range buf {
		buf[i] = 'x'
	}
	return string(buf)
}
```

- [ ] **Step 2: Add test for turn protection**

```go
func TestPrune_ProtectsRecentTwoTurns(t *testing.T) {
	// 5 turns, each with a 20KB bash output (5K tokens each)
	// Turns 4,3 are protected (most recent 2)
	// Turns 2,1,0 are candidates — 15K tokens, above PruneMinimum=5K
	turns := make([]turnDesc, 5)
	for i := range turns {
		callID := fmt.Sprintf("bash-call-%d", i)
		turns[i] = turnDesc{
			toolCalls: map[string]string{"bash": callID},
			results:   map[string]string{callID: bigContent(20_000)},
		}
	}
	msgs := buildMessages(turns)

	freed := prune(msgs)

	assert.Greater(t, freed, 0, "should prune old turns")

	// Verify recent 2 turns (turns 4,3) are NOT pruned
	for i := 3; i <= 4; i++ {
		toolMsg := msgs[i*3+2] // each turn is 3 messages
		for _, part := range toolMsg.Parts {
			tr := part.(message.ToolResult)
			assert.NotEqual(t, pruneMarker, tr.Content,
				"turn %d result should not be pruned", i)
		}
	}

	// Verify old turns (0,1) ARE pruned
	toolMsg0 := msgs[2] // turn 0's tool message
	for _, part := range toolMsg0.Parts {
		tr := part.(message.ToolResult)
		assert.Equal(t, pruneMarker, tr.Content,
			"turn 0 result should be pruned")
	}
}
```

- [ ] **Step 3: Add test for tool classification**

This test uses 6 turns to ensure total tokens exceed the 40K protection zone and 5K minimum.
Turn layout: 5 turns old + 1 turn recent. Old turns have different tool types to test classification.
Token math: each 200KB bash output = ~50K estimated tokens. Two bash outputs push total past 40K.

```go
func TestPrune_PrunableTools_ContentReplaced(t *testing.T) {
	// 5 turns: turns 0-2 are old enough (turns >= 2 from scan perspective),
	// turns 3-4 are protected (most recent 2).
	// Turn 0: bash with 200KB output (~50K tokens) — should be pruned
	// Turn 1: bash with 200KB output (~50K tokens) — should be pruned
	// Turn 2: bash with 200KB output (~50K tokens) — may be pruned
	// Turn 3: bash with 200KB — protected (recent)
	// Turn 4: bash with 200KB — protected (recent)
	turns := make([]turnDesc, 5)
	for i := range turns {
		callID := fmt.Sprintf("bash-call-%d", i)
		turns[i] = turnDesc{
			toolCalls: map[string]string{"bash": callID},
			results:   map[string]string{callID: bigContent(200_000)},
		}
	}
	msgs := buildMessages(turns)

	freed := prune(msgs)

	assert.Greater(t, freed, 0, "should prune old bash outputs")

	// Turn 0: should be pruned (furthest from protection zone)
	toolMsg0 := msgs[2].Parts[0].(message.ToolResult)
	assert.Equal(t, pruneMarker, toolMsg0.Content, "old bash output should be pruned")

	// Turn 4 (most recent): should NOT be pruned
	toolMsg4 := msgs[14].Parts[0].(message.ToolResult)
	assert.NotEqual(t, pruneMarker, toolMsg4.Content, "recent bash output should not be pruned")
}

func TestPrune_ClearableTools_InputCleared(t *testing.T) {
	// 4 turns. Need total tokens > 40K to start pruning.
	// Turn 0: bash 200KB (50K tokens) — pushes total past 40K
	// Turn 1: edit with large Input — Input should be cleared
	// Turn 2: bash 200KB — in protection zone
	// Turn 3: bash 200KB — protected by turns < 2
	turns := []turnDesc{
		{
			toolCalls: map[string]string{"bash": "bash-0"},
			results:   map[string]string{"bash-0": bigContent(200_000)},
		},
		{
			toolCalls: map[string]string{"edit": "edit-1"},
			results:   map[string]string{"edit-1": "file edited successfully"},
		},
		{
			toolCalls: map[string]string{"bash": "bash-2"},
			results:   map[string]string{"bash-2": bigContent(200_000)},
		},
		{
			toolCalls: map[string]string{"bash": "bash-3"},
			results:   map[string]string{"bash-3": bigContent(200_000)},
		},
	}
	msgs := buildMessages(turns)

	freed := prune(msgs)

	assert.Greater(t, freed, 0)

	// Turn 1 edit: ToolResult.Content should be KEPT (edit results are small and useful)
	toolMsg1 := msgs[5].Parts[0].(message.ToolResult)
	assert.Equal(t, "file edited successfully", toolMsg1.Content,
		"edit ToolResult.Content should be kept")

	// Turn 1 edit: ToolCall.Input should be CLEARED
	asstMsg1 := msgs[4] // assistant message of turn 1
	for _, part := range asstMsg1.Parts {
		if tc, ok := part.(message.ToolCall); ok && tc.ID == "edit-1" {
			assert.Equal(t, "{}", tc.Input, "edit ToolCall.Input should be cleared")
		}
	}
}

func TestPrune_ProtectedTools_NotModified(t *testing.T) {
	// 5 turns. Agent tool output should never be pruned regardless of size.
	// Turn 0: agent with 200KB — should NOT be pruned (protected)
	// Turn 1-2: bash with 200KB — may be pruned (ensures total > 40K)
	// Turn 3-4: bash with 200KB — protected by turns
	turns := []turnDesc{
		{
			toolCalls: map[string]string{"agent": "agent-0"},
			results:   map[string]string{"agent-0": bigContent(200_000)},
		},
		{
			toolCalls: map[string]string{"bash": "bash-1"},
			results:   map[string]string{"bash-1": bigContent(200_000)},
		},
		{
			toolCalls: map[string]string{"bash": "bash-2"},
			results:   map[string]string{"bash-2": bigContent(200_000)},
		},
		{
			toolCalls: map[string]string{"bash": "bash-3"},
			results:   map[string]string{"bash-3": bigContent(200_000)},
		},
		{
			toolCalls: map[string]string{"bash": "bash-4"},
			results:   map[string]string{"bash-4": bigContent(200_000)},
		},
	}
	msgs := buildMessages(turns)

	prune(msgs)

	// Turn 0 agent: should NOT be pruned (agent is not in any classification map)
	toolMsg0 := msgs[2].Parts[0].(message.ToolResult)
	assert.Equal(t, bigContent(200_000), toolMsg0.Content,
		"agent output should never be pruned")
}
```

- [ ] **Step 4: Add test for PruneMinimum threshold**

```go
func TestPrune_MinimumThreshold(t *testing.T) {
	// 3 turns with small outputs (< PruneMinimum=5K tokens total)
	turns := make([]turnDesc, 3)
	for i := range turns {
		callID := fmt.Sprintf("bash-call-%d", i)
		turns[i] = turnDesc{
			toolCalls: map[string]string{"bash": callID},
			results:   map[string]string{callID: bigContent(100)}, // tiny output
		}
	}
	msgs := buildMessages(turns)

	freed := prune(msgs)

	assert.Equal(t, 0, freed, "should not prune when below minimum threshold")
	// Verify nothing was modified
	toolMsg := msgs[2].Parts[0].(message.ToolResult)
	assert.NotEqual(t, pruneMarker, toolMsg.Content)
}
```

- [ ] **Step 5: Add test for summary boundary**

```go
func TestPrune_SummaryBoundary(t *testing.T) {
	// Build messages: turn 0 (bash, big) → summary message → turn 1 (bash, big)
	msgs := []message.Message{
		// turn 0
		{ID: "u0", Role: message.User, Parts: []message.ContentPart{message.TextContent{Text: "hi"}}},
		{ID: "a0", Role: message.Assistant, Parts: []message.ContentPart{
			message.TextContent{Text: "running bash"},
			message.ToolCall{ID: "bash-0", Name: "bash", Input: "{}", Finished: true},
		}},
		{ID: "t0", Role: message.Tool, Parts: []message.ContentPart{
			message.ToolResult{ToolCallID: "bash-0", Name: "bash", Content: bigContent(60_000)},
		}},
		// summary message — should stop backward scan
		{ID: "summary", Role: message.Assistant, IsSummaryMessage: true, Parts: []message.ContentPart{
			message.TextContent{Text: "Previous conversation was summarized..."},
		}},
		// turn 1 (after summary)
		{ID: "u1", Role: message.User, Parts: []message.ContentPart{message.TextContent{Text: "continue"}}},
		{ID: "a1", Role: message.Assistant, Parts: []message.ContentPart{
			message.TextContent{Text: "running bash again"},
			message.ToolCall{ID: "bash-1", Name: "bash", Input: "{}", Finished: true},
		}},
		{ID: "t1", Role: message.Tool, Parts: []message.ContentPart{
			message.ToolResult{ToolCallID: "bash-1", Name: "bash", Content: bigContent(60_000)},
		}},
	}

	prune(msgs)

	// Turn 0's bash output should NOT be pruned (summary boundary stops scan)
	toolMsg0 := msgs[2].Parts[0].(message.ToolResult)
	assert.NotEqual(t, pruneMarker, toolMsg0.Content,
		"pre-summary output should not be pruned")
}
```

- [ ] **Step 6: Add test for token protection**

```go
func TestPrune_TokenProtection(t *testing.T) {
	// 3 turns. Turn 0 has a big output (50K tokens), turn 1 has small output.
	// 40K token protection zone should protect turn 1's output.
	// Turn 0's output exceeds 40K → should be pruned.
	turns := []turnDesc{
		{
			toolCalls: map[string]string{"bash": "bash-0"},
			results:   map[string]string{"bash-0": bigContent(200_000)}, // ~50K tokens
		},
		{
			toolCalls: map[string]string{"bash": "bash-1"},
			results:   map[string]string{"bash-1": bigContent(80_000)}, // ~20K tokens
		},
		{
			toolCalls: map[string]string{"bash": "bash-2"},
			results:   map[string]string{"bash-2": bigContent(80_000)}, // ~20K tokens
		},
	}
	msgs := buildMessages(turns)

	freed := prune(msgs)

	// The scan goes reverse: turn 2 (20K) + turn 1 (20K) = 40K <= pruneProtectTokens
	// These are protected. Turn 0 (50K) pushes total to 90K, so it gets pruned.
	assert.Greater(t, freed, 0)
	toolMsg0 := msgs[2].Parts[0].(message.ToolResult)
	assert.Equal(t, pruneMarker, toolMsg0.Content, "turn 0 should be pruned")
}
```

- [ ] **Step 7: Run tests to verify they fail**

Run: `cd /g/ai-project/expert-paper/agent-init/crush && go test ./internal/agent/ -run TestPrune -v`
Expected: All tests FAIL (prune returns 0, does nothing)

- [ ] **Step 8: Commit**

```bash
git add internal/agent/prune_test.go
git commit -m "test(prune): add failing tests for prune behaviors"
```

---

### Task 3: Implement the prune function

**Files:**
- Modify: `internal/agent/prune.go`

- [ ] **Step 1: Replace the stub `prune` function with the full implementation**

Replace the `prune` function body in `internal/agent/prune.go`:

```go
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

	type pruneTarget struct {
		msgIndex  int
		partIndex int
		toolName  string
	}

	total := 0   // cumulative tokens from newest
	pruned := 0  // tokens to be freed
	var targets []pruneTarget
	turns := 0

	// Reverse-scan from newest to oldest.
loop:
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

			estimate := estimateTokens(tr.Content)
			total += estimate
			if total > pruneProtectTokens {
				pruned += estimate
				targets = append(targets, pruneTarget{
					msgIndex:  msgIndex,
					partIndex: partIndex,
					toolName:  toolName,
				})
			}
		}
	}

	if pruned < pruneMinimum {
		return 0
	}

	// Execute pruning in-place.
	for _, tgt := range targets {
		tr := msgs[tgt.msgIndex].Parts[tgt.partIndex].(message.ToolResult)

		if isPrunableTool(tgt.toolName) {
			tr.Content = pruneMarker
			tr.Data = ""
			msgs[tgt.msgIndex].Parts[tgt.partIndex] = tr
		} else if isClearableToolUse(tgt.toolName) {
			msgs[tgt.msgIndex].Parts[tgt.partIndex] = tr
			// Backtrack to find the ToolCall and clear its Input.
			if loc, ok := toolCallLoc[tr.ToolCallID]; ok {
				tc := msgs[loc.msgIndex].Parts[loc.partIndex].(message.ToolCall)
				tc.Input = "{}"
				msgs[loc.msgIndex].Parts[loc.partIndex] = tc
			}
		}
	}

	return pruned
}
```

- [ ] **Step 2: Run tests to verify they pass**

Run: `cd /g/ai-project/expert-paper/agent-init/crush && go test ./internal/agent/ -run TestPrune -v`
Expected: ALL tests PASS

- [ ] **Step 3: Commit**

```bash
git add internal/agent/prune.go
git commit -m "feat(prune): implement reverse-scan pruning algorithm"
```

---

### Task 4: Fix test compilation and iterate

**Files:**
- Modify: `internal/agent/prune_test.go`

The test file from Task 2 may need adjustments after seeing actual test output. This task ensures all tests compile and pass.

- [ ] **Step 1: Run tests and fix any compilation or logic errors**

Run: `cd /g/ai-project/expert-paper/agent-init/crush && go test ./internal/agent/ -run TestPrune -v`
Expected: ALL tests PASS

If any test fails, read the failure output, fix the test expectations to match the actual algorithm behavior, and re-run.

Common adjustments:
- The turn counting logic: user messages count as turns. Verify the test message structure matches what the algorithm expects.
- Token estimation: `bigContent(n)` creates n bytes → n/4 estimated tokens. Verify the protection math.

- [ ] **Step 2: Commit**

```bash
git add internal/agent/prune_test.go internal/agent/prune.go
git commit -m "test(prune): fix test compilation and alignment with algorithm"
```

---

### Task 5: Integrate prune into agent.go Run()

**Files:**
- Modify: `internal/agent/agent.go`

- [ ] **Step 1: Add the prune call in Run()**

In `internal/agent/agent.go`, find the line after `getSessionMessages` (around line 222):

```go
	msgs, err := a.getSessionMessages(ctx, currentSession)
	if err != nil {
		return nil, fmt.Errorf("failed to get session messages: %w", err)
	}
```

Add one line after the error check block, before the `var wg sync.WaitGroup` line:

```go
	prune(msgs)
```

The final context should look like:

```go
	msgs, err := a.getSessionMessages(ctx, currentSession)
	if err != nil {
		return nil, fmt.Errorf("failed to get session messages: %w", err)
	}

	prune(msgs)

	var wg sync.WaitGroup
```

- [ ] **Step 2: Verify compilation**

Run: `cd /g/ai-project/expert-paper/agent-init/crush && go build ./internal/agent/...`
Expected: compiles with no errors

- [ ] **Step 3: Run all existing agent tests to check for regressions**

Run: `cd /g/ai-project/expert-paper/agent-init/crush && go test ./internal/agent/ -v -count=1`
Expected: All existing tests pass (prune is a no-op for short test conversations with < 2 turns or < 5K tokens of tool output)

- [ ] **Step 4: Commit**

```bash
git add internal/agent/agent.go
git commit -m "feat(prune): integrate prune into agent Run() loop"
```

---

### Task 6: Final verification

**Files:**
- All modified files

- [ ] **Step 1: Run the full test suite**

Run: `cd /g/ai-project/expert-paper/agent-init/crush && go test ./... 2>&1 | head -80`
Expected: All tests pass. prune tests and existing agent tests all green.

- [ ] **Step 2: Run the linter**

Run: `cd /g/ai-project/expert-paper/agent-init/crush && go vet ./internal/agent/...`
Expected: No issues

- [ ] **Step 3: Verify the final diff**

Run: `cd /g/ai-project/expert-paper/agent-init/crush && git diff main --stat`
Expected: Changes in exactly 3 files:
- `internal/agent/prune.go` (new)
- `internal/agent/prune_test.go` (new)
- `internal/agent/agent.go` (1 line added)
