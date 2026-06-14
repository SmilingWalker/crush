package team

import (
	"strings"
	"testing"

	"charm.land/fantasy"
	"github.com/charmbracelet/crush/internal/agent"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// resultWithContent builds a *fantasy.AgentResult whose final Response.Content
// holds `text` and whose Steps carry `toolCalls` ToolCallContent elements (in a
// single step), so countToolCalls returns exactly toolCalls. Mirrors how M1-06
// builds results for sub-agent tests.
func resultWithContent(text string, toolCalls int) *fantasy.AgentResult {
	step := fantasy.StepResult{}
	step.Content = append(step.Content, fantasy.TextContent{Text: text})
	for i := 0; i < toolCalls; i++ {
		step.Content = append(step.Content, fantasy.ToolCallContent{
			ToolCallID: "tc-1",
			ToolName:   "view",
		})
	}
	return &fantasy.AgentResult{
		Steps:    []fantasy.StepResult{step},
		Response: fantasy.Response{Content: fantasy.ResponseContent{fantasy.TextContent{Text: text}}},
	}
}

// TestAggregateResults_ThreeCompleted locks acceptance #1 and #2: three
// completed delegates render a header line and three ### blocks, each carrying
// AgentID, duration, tool count, and content, separated by ---.
func TestAggregateResults_ThreeCompleted(t *testing.T) {
	group := &DelegateRunGroup{
		ID: "g1",
		Tasks: []DelegateTask{
			{ID: "t1", Prompt: "p", AgentID: "explore"},
			{ID: "t2", Prompt: "p", AgentID: "explore"},
			{ID: "t3", Prompt: "p", AgentID: "plan"},
		},
		Results: []DelegateResult{
			{TaskID: "t1", Status: agent.TurnCompleted, Result: resultWithContent("found foo", 2), DurationMs: 100},
			{TaskID: "t2", Status: agent.TurnCompleted, Result: resultWithContent("found bar", 0), DurationMs: 250},
			{TaskID: "t3", Status: agent.TurnCompleted, Result: resultWithContent("planned it", 5), DurationMs: 500},
		},
		Status: "done",
	}

	out := AggregateResults(group)

	// Header: 3 delegates, total 100+250+500 = 850ms.
	assert.Contains(t, out, "## Delegate Results (3 delegates, 850ms)")

	// Three ### blocks, each with index, agentID, duration, tool count.
	assert.Contains(t, out, "### 1. explore (100ms, 2 tools)")
	assert.Contains(t, out, "### 2. explore (250ms, 0 tools)")
	assert.Contains(t, out, "### 3. plan (500ms, 5 tools)")

	// Each delegate's content appears in its block.
	assert.Contains(t, out, "found foo")
	assert.Contains(t, out, "found bar")
	assert.Contains(t, out, "planned it")

	// Three --- separators between blocks.
	assert.Equal(t, 3, strings.Count(out, "\n---\n"))
}

// TestAggregateResults_MixedStatus locks acceptance #3: a failed delegate
// renders **FAILED**: <reason>, a canceled delegate renders **CANCELED**, and a
// completed delegate renders its content. Index alignment with Tasks is
// preserved (each block uses Tasks[i].AgentID).
func TestAggregateResults_MixedStatus(t *testing.T) {
	group := &DelegateRunGroup{
		ID: "g2",
		Tasks: []DelegateTask{
			{ID: "t1", Prompt: "p", AgentID: "explore"},
			{ID: "t2", Prompt: "p", AgentID: "explore"},
			{ID: "t3", Prompt: "p", AgentID: "plan"},
		},
		Results: []DelegateResult{
			{TaskID: "t1", Status: agent.TurnCompleted, Result: resultWithContent("ok", 1), DurationMs: 50},
			{TaskID: "t2", Status: agent.TurnFailed, Error: "model timed out", DurationMs: 30},
			{TaskID: "t3", Status: agent.TurnCanceled, DurationMs: 10},
		},
		Status: "done",
	}

	out := AggregateResults(group)

	// Completed -> content shown.
	assert.Contains(t, out, "### 1. explore (50ms, 1 tools)")
	assert.Contains(t, out, "ok")

	// Failed -> FAILED marker + error reason; no content body.
	assert.Contains(t, out, "### 2. explore (30ms, 0 tools)")
	assert.Contains(t, out, "**FAILED**: model timed out")

	// Canceled -> CANCELED marker, no error suffix.
	assert.Contains(t, out, "### 3. plan (10ms, 0 tools)")
	assert.Contains(t, out, "**CANCELED**")
}

// TestAggregateResults_PerDelegate500Cap locks Seam 3 note 1 (per-delegate
// 500-char content cap): a single delegate whose content is 600 chars renders
// only the first 500 chars followed by "...".
func TestAggregateResults_PerDelegate500Cap(t *testing.T) {
	long := strings.Repeat("x", 600)
	group := &DelegateRunGroup{
		ID:    "g3",
		Tasks: []DelegateTask{{ID: "t1", Prompt: "p", AgentID: "explore"}},
		Results: []DelegateResult{
			{TaskID: "t1", Status: agent.TurnCompleted, Result: resultWithContent(long, 0), DurationMs: 5},
		},
		Status: "done",
	}

	out := AggregateResults(group)

	// The full 500-char prefix is present...
	assert.Contains(t, out, strings.Repeat("x", 500))
	// ...but the 600-char full content is NOT (the last 100 xs are dropped).
	assert.NotContains(t, out, strings.Repeat("x", 600))
	// ...and the truncation marker appears.
	assert.Contains(t, out, "...")
}

// TestAggregateResults_Truncation locks acceptance #4 (hard 2000-char overall
// cap): six delegates each with 600-char content would far exceed 2000 chars;
// the output is bounded to <= 2000, carries the "... (truncated)" marker, and
// stopped before rendering the 6th delegate block.
func TestAggregateResults_Truncation(t *testing.T) {
	long := strings.Repeat("y", 600) // each block ~ >600 chars after header
	tasks := make([]DelegateTask, 6)
	results := make([]DelegateResult, 6)
	for i := 0; i < 6; i++ {
		tasks[i] = DelegateTask{ID: "t", Prompt: "p", AgentID: "explore"}
		results[i] = DelegateResult{
			TaskID:     "t",
			Status:     agent.TurnCompleted,
			Result:     resultWithContent(long, 0),
			DurationMs: int64(i) * 10,
		}
	}
	group := &DelegateRunGroup{ID: "g4", Tasks: tasks, Results: results, Status: "done"}

	out := AggregateResults(group)

	// Acceptance #4: total length bounded to 2000.
	assert.LessOrEqual(t, len(out), 2000, "aggregate must be <= 2000 chars")
	// The truncation marker is present.
	assert.Contains(t, out, "... (truncated)")
	// The loop stopped before the 6th delegate block (no "### 6." line).
	assert.NotContains(t, out, "### 6.", "loop must break before the 6th block")
}

// TestAggregateResults_EmptyGroup locks the degenerate case: zero delegates
// produces a header with count 0 and total 0ms, and no ### blocks.
func TestAggregateResults_EmptyGroup(t *testing.T) {
	group := &DelegateRunGroup{
		ID:      "g5",
		Tasks:   []DelegateTask{},
		Results: []DelegateResult{},
		Status:  "done",
	}

	out := AggregateResults(group)

	assert.Contains(t, out, "## Delegate Results (0 delegates, 0ms)")
	assert.NotContains(t, out, "###")
	assert.NotContains(t, out, "... (truncated)")
}

// TestAggregateResults_NilResultOnCompleted locks a defensive edge: a
// TurnCompleted slot with a nil Result (should not happen per M2-01's
// write paths, but AggregateResults must not panic) renders the header with
// 0 tools and no content body.
func TestAggregateResults_NilResultOnCompleted(t *testing.T) {
	group := &DelegateRunGroup{
		ID:    "g6",
		Tasks: []DelegateTask{{ID: "t1", Prompt: "p", AgentID: "explore"}},
		Results: []DelegateResult{
			{TaskID: "t1", Status: agent.TurnCompleted, Result: nil, DurationMs: 7},
		},
		Status: "done",
	}

	require.NotPanics(t, func() { _ = AggregateResults(group) })
	out := AggregateResults(group)
	assert.Contains(t, out, "### 1. explore (7ms, 0 tools)")
}
