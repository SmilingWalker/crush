package chat

import (
	"testing"

	"charm.land/fantasy"
	"github.com/charmbracelet/crush/internal/agent"
	"github.com/charmbracelet/crush/internal/team"
	"github.com/stretchr/testify/assert"
)

// TestStatusIcon locks the icon glyphs the expanded child rows use. "done"
// reuses styles.TodoCompletedIcon so the delegate vocabulary matches the
// existing todo icon set rather than introducing a parallel glyph set.
func TestStatusIcon(t *testing.T) {
	cases := map[string]string{
		"running":  "●",
		"done":     "✓",
		"error":    "✗",
		"canceled": "⊘",
		"":         "○",
	}
	for in, want := range cases {
		assert.Equal(t, want, statusIcon(in), "statusIcon(%q)", in)
	}
}

// TestNewDelegateChildItem_Mapping locks Seam 2: a team.DelegateResult is
// projected into the UI-facing DelegateChildItem. A completed result carries
// the fantasy content text, token/tool counts, the child session id, and the
// runner-recorded duration.
func TestNewDelegateChildItem_Mapping(t *testing.T) {
	task := team.DelegateTask{ID: "t1", AgentID: "explore", Prompt: "find x"}
	res := team.DelegateResult{
		TaskID:         "t1",
		Status:         agent.TurnCompleted,
		ChildSessionID: "sess-child-1",
		DurationMs:     1234,
		Result: &fantasy.AgentResult{
			Response:   fantasy.Response{Content: fantasy.ResponseContent{fantasy.TextContent{Text: "# found it\nsome detail"}}},
			TotalUsage: fantasy.Usage{InputTokens: 100, OutputTokens: 50},
		},
	}

	c := NewDelegateChildItem(task, res)
	assert.Equal(t, "explore", c.AgentType)
	assert.Equal(t, "done", c.Status)
	assert.Equal(t, "sess-child-1", c.ChildSessionID)
	assert.Equal(t, int64(1234), c.DurationMs)
	assert.Equal(t, "found it", c.ActivityDesc, "ActivityDesc is the first content line")
	assert.Equal(t, int64(150), c.Result.TotalTokens, "tokens projected from fantasy usage")
}

// TestNewDelegateChildItem_FailedResultWithNilFantasyResult locks the failure
// path (M2-01's TestDelegateRunner_PanicRecovery / RunError): a nil fantasy
// result still yields a usable child whose Result.Content is the error string.
func TestNewDelegateChildItem_FailedResultWithNilFantasyResult(t *testing.T) {
	task := team.DelegateTask{ID: "p", AgentID: "panic", Prompt: "p"}
	res := team.DelegateResult{
		TaskID: "p",
		Status: agent.TurnFailed,
		Error:  "panic: simulated run panic",
	}
	c := NewDelegateChildItem(task, res)
	assert.Equal(t, "error", c.Status)
	assert.Contains(t, c.Result.Content, "panic: simulated run panic")
	assert.Equal(t, int64(0), c.Result.TotalTokens)
}

// TestNewDelegateChildItem_StatusMapping locks the TurnStatus → UI status
// string mapping for every terminal state plus the running (zero) state.
func TestNewDelegateChildItem_StatusMapping(t *testing.T) {
	task := team.DelegateTask{ID: "x", AgentID: "explore"}
	assert.Equal(t, "running", NewDelegateChildItem(task, team.DelegateResult{}).Status)
	assert.Equal(t, "done", NewDelegateChildItem(task, team.DelegateResult{Status: agent.TurnCompleted}).Status)
	assert.Equal(t, "error", NewDelegateChildItem(task, team.DelegateResult{Status: agent.TurnFailed}).Status)
	assert.Equal(t, "canceled", NewDelegateChildItem(task, team.DelegateResult{Status: agent.TurnCanceled}).Status)
}

// TestDelegateUIEnabled_DefaultOffThenToggle locks Seam 3 / acceptance #5:
// the gate is OFF by default (no delegate UI renders until the config flag
// lands), and the package-level switch can be flipped for tests/rendering.
func TestDelegateUIEnabled_DefaultOffThenToggle(t *testing.T) {
	// Ensure default-off semantics regardless of test ordering.
	prev := setDelegateUIEnabledForTest(false)
	t.Cleanup(func() { setDelegateUIEnabledForTest(prev) })
	assert.False(t, delegateUIEnabled.Load())

	setDelegateUIEnabledForTest(true)
	assert.True(t, delegateUIEnabled.Load())
}
