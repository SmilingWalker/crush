package chat

import (
	"strings"
	"sync/atomic"

	"charm.land/fantasy"
	"github.com/charmbracelet/crush/internal/agent"
	"github.com/charmbracelet/crush/internal/team"
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
