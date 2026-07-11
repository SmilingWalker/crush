package chat

import (
	"fmt"
	"strings"
	"sync/atomic"

	tea "charm.land/bubbletea/v2"
	"charm.land/fantasy"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/crush/internal/agent"
	"github.com/charmbracelet/crush/internal/message"
	"github.com/charmbracelet/crush/internal/team"
	"github.com/charmbracelet/crush/internal/ui/anim"
	"github.com/charmbracelet/crush/internal/ui/styles"
)

// delegateUIEnabled gates whether the delegate group item renders. Until the
// Experimental.AgentTeamPreview config flag lands (open architecture issue #8,
// docs/agent-team-mode/12-open-architecture-issues.md:147), the gate is a
// package-level switch so the renderer is inert by default and acceptance #5
// ("flag off → no delegate UI") is testable in isolation. When the flag ships,
// swap the single delegateUIEnabled.Load() read in RenderTool (added in Task 2)
// for cfg.Experimental.AgentTeamPreview (a one-line change; the rest of the
// renderer is flag-agnostic).
var delegateUIEnabled atomic.Bool

// setDelegateUIEnabledForTest flips the gate and returns the previous value.
// Tests use it (with a t.Cleanup restore) so they never leak the flag into
// sibling tests in the same package binary.
func setDelegateUIEnabledForTest(on bool) bool {
	return delegateUIEnabled.Swap(on)
}

// DelegateChildItem is the UI-facing view of one delegate (a slot in a
// team.DelegateRunGroup). It is projected from a team.DelegateResult + its
// team.DelegateTask so the renderer never touches fantasy directly except
// through the small local projection below.
type DelegateChildItem struct {
	AgentType      string             // "explore" | "general-purpose" | "plan" — from the task
	Status         string             // "running" | "done" | "error" | "canceled"
	ToolUseCount   int                // Result.TotalToolUseCount
	ActivityDesc   string             // first content line (done) or "working…" (running)
	Result         agent.AgentToolResult
	ChildSessionID string
	DurationMs     int64
}

// statusIcon maps a UI status string to the glyph shown in the expanded child
// row. "done" reuses styles.TodoCompletedIcon so the delegate vocabulary
// matches the existing todo icon set.
func statusIcon(status string) string {
	switch status {
	case "running":
		return "●"
	case "done":
		return styles.TodoCompletedIcon // "✓"
	case "error":
		return "✗"
	case "canceled":
		return "⊘"
	default:
		return "○"
	}
}

// childStatus maps a team/agent terminal TurnStatus (or the zero "running"
// state) to the UI status string used by DelegateChildItem and statusIcon.
func childStatus(s agent.TurnStatus) string {
	switch s {
	case agent.TurnCompleted:
		return "done"
	case agent.TurnFailed:
		return "error"
	case agent.TurnCanceled:
		return "canceled"
	default:
		return "running"
	}
}

// firstLine returns the first non-empty line of s, stripped of leading '#'/
// whitespace; "" if s is empty/whitespace. Used for the ActivityDesc column.
func firstLine(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return ""
	}
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		s = s[:i]
	}
	return strings.TrimSpace(strings.TrimLeft(s, "# "))
}

// NewDelegateChildItem projects a team.DelegateTask + team.DelegateResult into
// the UI-facing DelegateChildItem (Seam 2). A nil fantasy Result (failure
// before any model output) yields a child whose Result.Content is the error
// string, so failure paths render the reason.
func NewDelegateChildItem(task team.DelegateTask, res team.DelegateResult) DelegateChildItem {
	c := DelegateChildItem{
		AgentType:      task.AgentID,
		Status:         childStatus(res.Status),
		ChildSessionID: res.ChildSessionID,
		DurationMs:     res.DurationMs,
	}
	if res.Result != nil {
		c.Result = resultToAgentToolResult(res.Result)
		c.ToolUseCount = c.Result.TotalToolUseCount
		c.ActivityDesc = firstLine(c.Result.Content)
	} else if res.Error != "" {
		c.Result.Content = res.Error
		c.ActivityDesc = firstLine(res.Error)
	}
	if c.Status == "running" && c.ActivityDesc == "" {
		c.ActivityDesc = "working…"
	}
	return c
}

// resultToAgentToolResult projects a *fantasy.AgentResult into the small
// agent.AgentToolResult slice the UI reads (Content, TotalTokens,
// TotalToolUseCount). It mirrors agent.buildAgentToolResult
// (internal/agent/agent_result.go:35) but lives in package chat so the UI does
// not depend on the (currently unexported) agent helper. DurationMs is left
// zero here — the caller (NewDelegateChildItem) prefers the runner-recorded
// res.DurationMs, which is always set (delegate_runner.go:152).
func resultToAgentToolResult(r *fantasy.AgentResult) agent.AgentToolResult {
	if r == nil {
		return agent.AgentToolResult{}
	}
	ar := agent.AgentToolResult{
		Content:     r.Response.Content.Text(),
		TotalTokens: r.TotalUsage.InputTokens + r.TotalUsage.OutputTokens,
	}
	// Count tool calls the same way agent.countToolCalls does
	// (agent_result.go:58): one per ToolCallContent element across every step.
	for _, step := range r.Steps {
		for _, content := range step.Content {
			if _, ok := content.(fantasy.ToolCallContent); ok {
				ar.TotalToolUseCount++
			}
		}
	}
	return ar
}

// DelegateGroupMessageItem renders a parallel delegate group (a
// team.DelegateRunGroup) as a single chat list item. It follows the
// AgentToolMessageItem pattern (internal/ui/chat/agent.go:28): it embeds
// *baseToolMessageItem for caching/versioning/animation, holds its child rows
// inline (children are NOT list items — they render as lines inside Render,
// like agent.go's nested tools), and implements Expandable + KeyEventHandler
// for the compact/expanded toggle and the Enter-opens-modal interaction.
type DelegateGroupMessageItem struct {
	*baseToolMessageItem

	groupID       string
	children      []DelegateChildItem
	selectedChild int // cursor into children; only meaningful when expanded
}

var (
	_ ToolMessageItem = (*DelegateGroupMessageItem)(nil)
	_ Expandable      = (*DelegateGroupMessageItem)(nil)
	_ Animatable      = (*DelegateGroupMessageItem)(nil)
	_ KeyEventHandler = (*DelegateGroupMessageItem)(nil)
)

// NewDelegateGroupMessageItem creates the group item. toolCall.ID is the list
// item's ID (the same ID the message-extraction path would assign); groupID is
// the team.DelegateRunGroup.ID, carried so OpenDelegateTranscriptMsg can name
// the group.
func NewDelegateGroupMessageItem(
	sty *styles.Styles,
	groupID string,
	toolCall message.ToolCall,
	result *message.ToolResult,
	canceled bool,
) *DelegateGroupMessageItem {
	d := &DelegateGroupMessageItem{groupID: groupID}
	d.baseToolMessageItem = newBaseToolMessageItem(sty, toolCall, result, &DelegateGroupRenderContext{group: d}, canceled)
	// Keep the parent spinning while any child is still running.
	d.spinningFunc = func(state SpinningState) bool {
		for _, c := range d.children {
			if c.Status == "running" {
				return true
			}
		}
		return false
	}
	return d
}

// Children returns the current child rows (read-only view for callers like the
// main model that need to read a child's content when opening the transcript).
func (d *DelegateGroupMessageItem) Children() []DelegateChildItem {
	return d.children
}

// SetChildren replaces the child rows. Always bumps (mirrors
// AgentToolMessageItem.SetNestedTools at agent.go:104): the live update path
// mutates children in place then calls SetChildren with the same slice, so
// pointer-equality dedupe would skip a needed re-render.
func (d *DelegateGroupMessageItem) SetChildren(children []DelegateChildItem) {
	if d.selectedChild >= len(children) {
		d.selectedChild = max(0, len(children)-1)
	}
	d.children = children
	d.clearCache()
	d.Bump()
}

// ToggleExpanded implements Expandable. Overrides the embedded base method so
// the bump/clearCache happen on this concrete type (the base implementation is
// identical, but defining it here keeps the delegate surface self-contained
// and lets future per-state hooks live on the right receiver).
func (d *DelegateGroupMessageItem) ToggleExpanded() bool {
	d.expandedContent = !d.expandedContent
	d.clearCache()
	d.Bump()
	return d.expandedContent
}

// Animate forwards spinner ticks to the parent anim (mirrors
// baseToolMessageItem.Animate at tools.go:307, with the ID gate) so running
// children show a live spinner. Bumps the version so the list cache re-renders
// the new frame.
func (d *DelegateGroupMessageItem) Animate(msg anim.StepMsg) tea.Cmd {
	if msg.ID != d.toolCall.ID {
		return nil
	}
	if !d.isSpinning() {
		return nil
	}
	d.Bump()
	return d.anim.Animate(msg)
}

// HandleKeyEvent implements KeyEventHandler. When the group is expanded and
// focused it owns Up/Down (move the child cursor) and Enter (open the selected
// child's transcript if that child is terminal). Returns (true, cmd) when the
// key is consumed so Chat.HandleKeyMsg does not fall through to default
// navigation; (false, nil) otherwise (collapsed → fall through).
func (d *DelegateGroupMessageItem) HandleKeyEvent(key tea.KeyMsg) (bool, tea.Cmd) {
	if !d.expandedContent || len(d.children) == 0 {
		return false, nil
	}
	switch key.String() {
	case "up", "k":
		if d.selectedChild > 0 {
			d.selectedChild--
			d.clearCache()
			d.Bump()
		}
		return true, nil
	case "down", "j":
		if d.selectedChild < len(d.children)-1 {
			d.selectedChild++
			d.clearCache()
			d.Bump()
		}
		return true, nil
	case "enter":
		if d.selectedChild < 0 || d.selectedChild >= len(d.children) {
			return true, nil
		}
		c := d.children[d.selectedChild]
		if c.Status == "running" {
			// Consumed (no fallthrough) but no modal — the transcript only
			// exists for completed children.
			return true, nil
		}
		return true, func() tea.Msg {
			return OpenDelegateTranscriptMsg{GroupID: d.groupID, ChildIndex: d.selectedChild}
		}
	}
	return false, nil
}

// Render overrides the embedded base method to enforce the flag gate at the
// item level (acceptance #5): when the gate is off the item contributes NO
// output — not even the focus/selection prefix the base Render would prepend
// to an empty RenderTool result. When the gate is on, defer to the base.
func (d *DelegateGroupMessageItem) Render(width int) string {
	if !delegateUIEnabled.Load() {
		return ""
	}
	return d.baseToolMessageItem.Render(width)
}

// RawRender overrides the embedded base method for the same flag reason as
// Render: the base RawRender applies renderHighlighted to the RenderTool
// output, which is non-empty even when RenderTool returns "". Gating here
// keeps the flag-off contract total.
func (d *DelegateGroupMessageItem) RawRender(width int) string {
	if !delegateUIEnabled.Load() {
		return ""
	}
	return d.baseToolMessageItem.RawRender(width)
}

// DelegateGroupRenderContext is the ToolRenderer for the delegate group.
type DelegateGroupRenderContext struct {
	group *DelegateGroupMessageItem
}

// RenderTool implements ToolRenderer. When the flag gate is off it returns ""
// (acceptance #5). Otherwise compact = a single status line; expanded = the
// status line plus a per-child row.
func (r *DelegateGroupRenderContext) RenderTool(sty *styles.Styles, width int, opts *ToolRenderOpts) string {
	if !delegateUIEnabled.Load() {
		return ""
	}
	cappedWidth := cappedMessageWidth(width)
	running, done, failed := 0, 0, 0
	for _, c := range r.group.children {
		switch c.Status {
		case "running":
			running++
		case "done":
			done++
		default: // error | canceled
			failed++
		}
	}

	// Compact status line: "Delegates  N running / M done" (+ "/ K failed" if any).
	statusParts := []string{fmt.Sprintf("%d running", running), fmt.Sprintf("%d done", done)}
	if failed > 0 {
		statusParts = append(statusParts, fmt.Sprintf("%d failed", failed))
	}
	statusLine := "Delegates  " + strings.Join(statusParts, " / ")
	header := toolHeader(sty, opts.Status, "Delegates", cappedWidth, opts, statusLine)
	// Only show the spinner when there is no result yet AND children are still
	// running (matches agent.go:178's anim gate).
	spinning := !opts.HasResult() && running > 0
	if opts.Compact || !r.group.expandedContent {
		if spinning {
			return lipgloss.JoinVertical(lipgloss.Left, header, "", opts.Anim.Render())
		}
		return header
	}

	// Expanded: one row per child.
	var rows []string
	for i, c := range r.group.children {
		dur := ""
		if c.DurationMs > 0 {
			dur = formatDurationMs(c.DurationMs)
		}
		tools := ""
		if c.ToolUseCount > 0 {
			tools = fmt.Sprintf("%d tools", c.ToolUseCount)
		}
		row := fmt.Sprintf("%s %-14s %-9s %6s %7s  %s",
			statusIcon(c.Status), c.AgentType, c.Status, tools, dur, c.ActivityDesc)
		if i == r.group.selectedChild {
			row = sty.Tool.AgentPrompt.Render(row) // highlight the focused row
		}
		rows = append(rows, row)
	}
	body := strings.Join(rows, "\n")
	if spinning {
		body = lipgloss.JoinVertical(lipgloss.Left, body, "", opts.Anim.Render())
	}
	return joinToolParts(header, sty.Tool.Body.Render(body))
}

// formatDurationMs renders a millisecond duration compactly (e.g. "1.2s",
// "340ms"). Kept minimal — display only.
func formatDurationMs(ms int64) string {
	if ms < 1000 {
		return fmt.Sprintf("%dms", ms)
	}
	return fmt.Sprintf("%.1fs", float64(ms)/1000)
}
