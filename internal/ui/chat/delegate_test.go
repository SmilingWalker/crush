package chat

import (
	"testing"

	tea "charm.land/bubbletea/v2"
	"charm.land/fantasy"
	"github.com/charmbracelet/crush/internal/agent"
	"github.com/charmbracelet/crush/internal/message"
	"github.com/charmbracelet/crush/internal/team"
	"github.com/charmbracelet/crush/internal/ui/common"
	"github.com/charmbracelet/crush/internal/ui/styles"
	"github.com/charmbracelet/x/ansi"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
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

// --- Task 2: DelegateGroupMessageItem renderer + expand/animate/key handling ---

// TestDelegateGroupMessageItem_FlagOffRendersNothing locks acceptance #5: with
// the gate off, RenderTool returns the empty string so the item contributes no
// lines to the chat list.
func TestDelegateGroupMessageItem_FlagOffRendersNothing(t *testing.T) {
	prev := setDelegateUIEnabledForTest(false)
	t.Cleanup(func() { setDelegateUIEnabledForTest(prev) })

	sty := newTestStyles(t)
	item := NewDelegateGroupMessageItem(sty, "grp-1", message.ToolCall{ID: "grp-1"}, nil, false)
	item.SetChildren([]DelegateChildItem{
		{AgentType: "explore", Status: "running"},
		{AgentType: "explore", Status: "done"},
	})
	out := item.Render(80)
	assert.Empty(t, out, "flag off → no delegate UI")
}

// TestDelegateGroupMessageItem_CompactRendersStatusLine locks acceptance #1:
// compact mode renders a single status row with running/done counts.
func TestDelegateGroupMessageItem_CompactRendersStatusLine(t *testing.T) {
	prev := setDelegateUIEnabledForTest(true)
	t.Cleanup(func() { setDelegateUIEnabledForTest(prev) })

	sty := newTestStyles(t)
	item := NewDelegateGroupMessageItem(sty, "grp-1", message.ToolCall{ID: "grp-1"}, nil, false)
	item.SetChildren([]DelegateChildItem{
		{AgentType: "explore", Status: "running"},
		{AgentType: "explore", Status: "running"},
		{AgentType: "plan", Status: "done"},
	})
	out := item.Render(80)
	plain := ansi.Strip(out)
	assert.Contains(t, plain, "Delegates")
	assert.Contains(t, plain, "2 running")
	assert.Contains(t, plain, "1 done")
}

// TestDelegateGroupMessageItem_ExpandedShowsChildren locks acceptance #2: with
// the item expanded, each child renders a row with its agent type, status icon,
// and activity description.
func TestDelegateGroupMessageItem_ExpandedShowsChildren(t *testing.T) {
	prev := setDelegateUIEnabledForTest(true)
	t.Cleanup(func() { setDelegateUIEnabledForTest(prev) })

	sty := newTestStyles(t)
	item := NewDelegateGroupMessageItem(sty, "grp-1", message.ToolCall{ID: "grp-1"}, nil, false)
	item.SetChildren([]DelegateChildItem{
		{AgentType: "explore", Status: "done", ToolUseCount: 7, DurationMs: 1200, ActivityDesc: "found it"},
		{AgentType: "explore", Status: "error", ToolUseCount: 2, DurationMs: 300, ActivityDesc: "boom"},
	})
	// Default collapsed; expand via the Expandable interface (the keyboard path).
	exp, ok := any(item).(Expandable)
	require.True(t, ok)
	exp.ToggleExpanded()

	out := item.Render(80)
	plain := ansi.Strip(out)
	assert.Contains(t, plain, "explore")
	assert.Contains(t, plain, statusIcon("done"))
	assert.Contains(t, plain, statusIcon("error"))
	assert.Contains(t, plain, "found it")
	assert.Contains(t, plain, "boom")
}

// TestDelegateGroupMessageItem_KeyEnterEmitsOpenMsg locks acceptance #4's
// signal: with a completed child selected, Enter returns a cmd whose msg is
// OpenDelegateTranscriptMsg carrying the group id and child index.
func TestDelegateGroupMessageItem_KeyEnterEmitsOpenMsg(t *testing.T) {
	prev := setDelegateUIEnabledForTest(true)
	t.Cleanup(func() { setDelegateUIEnabledForTest(prev) })

	sty := newTestStyles(t)
	item := NewDelegateGroupMessageItem(sty, "grp-1", message.ToolCall{ID: "grp-1"}, nil, false)
	item.SetChildren([]DelegateChildItem{
		{AgentType: "explore", Status: "running"},
		{AgentType: "explore", Status: "done", ActivityDesc: "x", Result: agent.AgentToolResult{Content: "# answer"}},
	})
	exp, ok := any(item).(Expandable)
	require.True(t, ok)
	exp.ToggleExpanded()

	// Select the done child (index 1) via Down, then Enter.
	handler := any(item).(KeyEventHandler)
	handledDown, _ := handler.HandleKeyEvent(tea.KeyPressMsg{Code: tea.KeyDown})
	require.True(t, handledDown, "Down must be consumed to move the child cursor")

	handled, cmd := handler.HandleKeyEvent(tea.KeyPressMsg{Code: tea.KeyEnter})
	require.True(t, handled)
	require.NotNil(t, cmd)

	msg := executeCmd(t, cmd)
	open, ok := msg.(OpenDelegateTranscriptMsg)
	require.True(t, ok, "Enter must emit OpenDelegateTranscriptMsg, got %T", msg)
	assert.Equal(t, "grp-1", open.GroupID)
	assert.Equal(t, 1, open.ChildIndex, "Enter opens the selected (done) child")
}

// TestDelegateGroupMessageItem_KeyEnterOnRunningChildDoesNotOpen locks the
// guard: Enter on a non-done child is consumed (no default nav) but emits NO
// open msg (the modal is for completed transcripts only).
func TestDelegateGroupMessageItem_KeyEnterOnRunningChildDoesNotOpen(t *testing.T) {
	prev := setDelegateUIEnabledForTest(true)
	t.Cleanup(func() { setDelegateUIEnabledForTest(prev) })

	sty := newTestStyles(t)
	item := NewDelegateGroupMessageItem(sty, "grp-1", message.ToolCall{ID: "grp-1"}, nil, false)
	item.SetChildren([]DelegateChildItem{
		{AgentType: "explore", Status: "running"},
	})
	exp, ok := any(item).(Expandable)
	require.True(t, ok)
	exp.ToggleExpanded()

	handler := any(item).(KeyEventHandler)
	handled, cmd := handler.HandleKeyEvent(tea.KeyPressMsg{Code: tea.KeyEnter})
	assert.True(t, handled, "Enter is still consumed (no fallthrough) on a running child")
	if cmd != nil {
		msg := executeCmd(t, cmd)
		_, isOpen := msg.(OpenDelegateTranscriptMsg)
		assert.False(t, isOpen, "Enter on a running child must not open the transcript")
	}
}

// TestDelegateGroupMessageItem_KeyNotConsumedWhenCollapsed locks the fallthrough
// contract: when collapsed, Up/Down/Enter pass through to the list (so chat
// navigation still works). HandleKeyEvent returns (false, nil).
func TestDelegateGroupMessageItem_KeyNotConsumedWhenCollapsed(t *testing.T) {
	prev := setDelegateUIEnabledForTest(true)
	t.Cleanup(func() { setDelegateUIEnabledForTest(prev) })

	sty := newTestStyles(t)
	item := NewDelegateGroupMessageItem(sty, "grp-1", message.ToolCall{ID: "grp-1"}, nil, false)
	item.SetChildren([]DelegateChildItem{{AgentType: "explore", Status: "done"}})

	handler := any(item).(KeyEventHandler)
	handled, cmd := handler.HandleKeyEvent(tea.KeyPressMsg{Code: tea.KeyDown})
	assert.False(t, handled, "Down must fall through when collapsed")
	assert.Nil(t, cmd)
}

// --- test helpers (shared across delegate_test.go) ---

// newTestStyles returns a real *styles.Styles (built the same way the live UI
// builds them) so the renderers exercise the real style fields. Uses
// common.DefaultCommon(nil).Styles — the same construction newTestUI in
// internal/ui/model/layout_test.go:33 uses.
func newTestStyles(t *testing.T) *styles.Styles {
	t.Helper()
	return common.DefaultCommon(nil).Styles
}

// executeCmd runs a tea.Cmd synchronously and returns the produced msg.
func executeCmd(t *testing.T, cmd tea.Cmd) tea.Msg {
	t.Helper()
	if cmd == nil {
		return nil
	}
	return cmd()
}
