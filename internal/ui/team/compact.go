// Package team provides UI components for agent team display. M3-09 panel.go
// is the full-screen debug panel; compact.go (M4-09) provides inline compact
// team status cards (TeamCompactItem + ExpandedTeamView) suitable for embedding
// in the chat message stream.
package team

import (
	"fmt"
	"sort"
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/crush/internal/team"
)

// --- StatusIcon ---

// StatusIcon maps a status string to a single-char Unicode glyph for
// terminal display. Shared between Panel (M3-09) and CompactItem (M4-09).
func StatusIcon(status string) string {
	switch status {
	case "running", "in_progress":
		return "●"
	case "completed", "done":
		return "✓"
	case "failed", "error":
		return "✗"
	case "paused":
		return "⏸"
	case "blocked", "waiting_permission":
		return "⏳"
	case "queued", "assigned", "created", "starting":
		return "○"
	case "stopped", "canceled", "shutting_down", "canceling_turn":
		return "■"
	case "idle":
		return "◇"
	default:
		return " "
	}
}

// --- TeamStatusProvider ---

// TeamStatusProvider abstracts the data source for team runtime status.
// TeamRunner.Status() (M4-02) is the canonical implementation.
type TeamStatusProvider interface {
	TeamStatus() (team.TeamRuntimeStatus, error)
}

// --- TeamCompactItem ---

// TeamCompactItem renders a single team's runtime status as a compact
// card suitable for inline display in the chat message stream. Compact
// view is 1-3 lines; expanded view (RenderExpanded) is up to 24 lines.
type TeamCompactItem struct {
	status       team.TeamRuntimeStatus
	provider     TeamStatusProvider
	teamName     string
	teamStatus   string
	costMicros   int64
	expanded     bool
	scrollOffset int
}

// TeamCompactOption configures a TeamCompactItem.
type TeamCompactOption func(*TeamCompactItem)

// WithProvider sets the TeamStatusProvider for data fetching.
func WithProvider(p TeamStatusProvider) TeamCompactOption {
	return func(t *TeamCompactItem) { t.provider = p }
}

// WithTeamName sets the display name of the team.
func WithTeamName(name string) TeamCompactOption {
	return func(t *TeamCompactItem) { t.teamName = name }
}

// WithTeamStatus sets the team-level status string.
func WithTeamStatus(status string) TeamCompactOption {
	return func(t *TeamCompactItem) { t.teamStatus = status }
}

// WithCostMicros sets the total cost-so-far in micros.
func WithCostMicros(cost int64) TeamCompactOption {
	return func(t *TeamCompactItem) { t.costMicros = cost }
}

// NewTeamCompactItem creates a TeamCompactItem with optional configuration.
func NewTeamCompactItem(opts ...TeamCompactOption) *TeamCompactItem {
	t := &TeamCompactItem{}
	for _, o := range opts {
		o(t)
	}
	return t
}

// SetStatus replaces the current runtime status data.
func (t *TeamCompactItem) SetStatus(s team.TeamRuntimeStatus) {
	t.status = s
}

// ToggleExpanded flips the expanded/collapsed state and resets scroll.
func (t *TeamCompactItem) ToggleExpanded() {
	t.expanded = !t.expanded
	t.scrollOffset = 0
}

// IsExpanded reports whether the expanded view is active.
func (t *TeamCompactItem) IsExpanded() bool { return t.expanded }

// RenderCompact renders the 1-3 line compact summary card.
// Layout:
//
//	● TeamName ── 5 members · 3 active runs
//	Cost: 1.2M tokens · Status: running
func (t *TeamCompactItem) RenderCompact(width int) string {
	if width < 20 {
		width = 20
	}

	name := t.teamName
	if name == "" {
		name = t.status.TeamID
	}
	if len(name) > width-16 {
		name = name[:width-16]
	}

	memberCount := len(t.status.Members)
	active := t.status.ActiveRuns

	// Empty state
	if memberCount == 0 && active == 0 && t.teamName == "" && t.teamStatus == "" {
		return t.renderCompactCard(width, "No data")
	}

	// Build status icon based on team status or active runs
	icon := StatusIcon(t.teamStatus)
	if icon == " " && active > 0 {
		icon = "●"
	}

	// Line 1: icon + team name
	titleStyle := lipgloss.NewStyle().Bold(true).Width(width).Padding(0, 1)
	header := titleStyle.Render(fmt.Sprintf("%s %s", icon, name))

	// Line 2: member count + active runs
	var parts []string
	parts = append(parts, fmt.Sprintf("%d members", memberCount))
	if active > 0 {
		parts = append(parts, fmt.Sprintf("%d active", active))
	}
	summary := lipgloss.NewStyle().Width(width).Padding(0, 1).Render(strings.Join(parts, " · "))

	// Line 3 (optional): cost + team status
	var extras []string
	if t.costMicros > 0 {
		extras = append(extras, fmt.Sprintf("Cost: %s", formatCost(t.costMicros)))
	}
	statusLabel := t.teamStatus
	if statusLabel != "" {
		extras = append(extras, fmt.Sprintf("Status: %s", statusLabel))
	}

	var bodyLines []string
	bodyLines = append(bodyLines, header, summary)
	if len(extras) > 0 {
		bodyLines = append(bodyLines, lipgloss.NewStyle().Width(width).Padding(0, 1).Render(strings.Join(extras, " · ")))
	}

	return t.renderCompactCard(width, strings.Join(bodyLines, "\n"))
}

// renderCompactCard wraps content in a bordered card with team-color accent.
func (t *TeamCompactItem) renderCompactCard(width int, content string) string {
	cardStyle := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(lipgloss.Color("63")).
		Padding(0, 1).
		Width(width - 2)

	return cardStyle.Render(content)
}

// RenderExpanded renders the full member status list capped at maxLines lines.
// When there are more members than can fit, a truncation footer is shown with
// scroll hints. The scrollOffset field controls which members are visible.
//
// Layout (max 24 lines):
//
//	┌─ ● TeamName ──────────────────────────────────┐
//	│ 5 members · 2 active                            │
//	│                                                 │
//	│ ● Alice   planner     running    T1  write_plan │
//	│ ◇ Bob     programmer  idle       —    —         │
//	│ ✓ Diana   reviewer    stopped    T2  —          │
//	│                                                 │
//	│ ... and 2 more members                          │
//	│ j/k scroll  space collapse                      │
//	└─────────────────────────────────────────────────┘
func (t *TeamCompactItem) RenderExpanded(width int, maxLines int) string {
	if width < 40 {
		width = 40
	}
	if maxLines <= 0 {
		maxLines = 24
	}

	name := t.teamName
	if name == "" {
		name = t.status.TeamID
	}

	memberCount := len(t.status.Members)
	active := t.status.ActiveRuns

	// Empty state
	if memberCount == 0 && name == "" {
		return t.renderCompactCard(width, "No data")
	}

	// Header line: icon + team name
	icon := StatusIcon(t.teamStatus)
	if icon == " " && active > 0 {
		icon = "●"
	}
	headerStyle := lipgloss.NewStyle().Bold(true).Width(width).Padding(0, 1)
	header := headerStyle.Render(fmt.Sprintf("%s %s", icon, name))

	// Summary line
	summaryParts := []string{fmt.Sprintf("%d members", memberCount)}
	if active > 0 {
		summaryParts = append(summaryParts, fmt.Sprintf("%d active", active))
	}
	if t.costMicros > 0 {
		summaryParts = append(summaryParts, formatCost(t.costMicros))
	}
	summaryStyle := lipgloss.NewStyle().Width(width).Padding(0, 1).Foreground(lipgloss.Color("241"))
	summary := summaryStyle.Render(strings.Join(summaryParts, " · "))

	// Collect sorted member names for stable output
	memberNames := make([]string, 0, len(t.status.Members))
	for name := range t.status.Members {
		memberNames = append(memberNames, name)
	}
	// Sort for deterministic output
	sort.Strings(memberNames)

	// Calculate how many member rows can fit
	// Overhead: 2 (border) + 2 (header+summary) + 1 (blank above members)
	//          + 2 (blank below + nav) + 1 (footer if truncated)
	overhead := 8 // border-top + header + summary + blank + blank + nav + blank + border-bottom
	truncated := false
	maxMemberRows := maxLines - overhead
	if maxMemberRows < 0 {
		maxMemberRows = 0
	}

	// Apply scroll offset
	startIdx := t.scrollOffset
	if startIdx < 0 {
		startIdx = 0
	}
	if startIdx > len(memberNames) {
		startIdx = len(memberNames)
	}

	visibleNames := memberNames[startIdx:]
	if len(visibleNames) > maxMemberRows {
		visibleNames = visibleNames[:maxMemberRows]
		truncated = true
	}

	hiddenAbove := startIdx
	hiddenBelow := len(memberNames) - startIdx - len(visibleNames)
	if hiddenBelow < 0 {
		hiddenBelow = 0
	}

	// Build member rows
	itemStyle := lipgloss.NewStyle().Width(width).Padding(0, 1)
	var memberLines []string
	if hiddenAbove > 0 {
		memberLines = append(memberLines, itemStyle.Foreground(lipgloss.Color("241")).Render(
			fmt.Sprintf("  ... %d members above", hiddenAbove),
		))
	}
	for _, name := range visibleNames {
		m := t.status.Members[name]
		row := t.renderMemberRow(width-4, name, m)
		memberLines = append(memberLines, itemStyle.Render(row))
	}
	if truncated && hiddenBelow > 0 {
		memberLines = append(memberLines, itemStyle.Foreground(lipgloss.Color("241")).Render(
			fmt.Sprintf("  ... and %d more members", hiddenBelow),
		))
	}

	// Navigation footer
	navStyle := lipgloss.NewStyle().Width(width).Padding(0, 1).Foreground(lipgloss.Color("241"))
	nav := navStyle.Render("j/k scroll  space collapse")

	var bodyLines []string
	bodyLines = append(bodyLines, header, summary)
	if len(memberLines) > 0 {
		bodyLines = append(bodyLines, "") // blank separator
		bodyLines = append(bodyLines, memberLines...)
	}
	bodyLines = append(bodyLines, "", nav) // blank + nav

	return t.renderCompactCard(width, strings.Join(bodyLines, "\n"))
}

// renderMemberRow formats a single member's status row for the expanded view.
// Columns: icon, name (12), role (12), status (12), task (8), tool (rest).
func (t *TeamCompactItem) renderMemberRow(width int, name string, m team.MemberRuntimeState) string {
	icon := StatusIcon(string(m.State))

	task := m.CurrentTask
	if task == "" {
		task = "—"
	}
	tool := m.CurrentTool
	if tool == "" {
		tool = "—"
	}

	// Fixed-width columns
	colName := truncateOrPad(name, 12)
	colRole := truncateOrPad(m.Role, 12)
	colStatus := truncateOrPad(string(m.State), 12)
	colTask := truncateOrPad(task, 10)
	colTool := tool // variable

	return fmt.Sprintf("%s %s %s %s %s %s",
		icon, colName, colRole, colStatus, colTask, colTool)
}

// truncateOrPad truncates s to maxLen chars or right-pads with spaces.
func truncateOrPad(s string, maxLen int) string {
	if len(s) > maxLen {
		return s[:maxLen-1] + "…"
	}
	return fmt.Sprintf("%-*s", maxLen, s)
}

// --- helpers ---

// formatCost renders cost in micros to a human-readable string.
// Uses the same scale as the team domain: 1 token ≈ 1 micro.
func formatCost(micros int64) string {
	if micros >= 1_000_000_000_000 {
		return fmt.Sprintf("%.1fT tokens", float64(micros)/1_000_000_000_000)
	}
	if micros >= 1_000_000_000 {
		return fmt.Sprintf("%.1fB tokens", float64(micros)/1_000_000_000)
	}
	if micros >= 1_000_000 {
		return fmt.Sprintf("%.1fM tokens", float64(micros)/1_000_000)
	}
	if micros >= 1_000 {
		return fmt.Sprintf("%.1fK tokens", float64(micros)/1_000)
	}
	return fmt.Sprintf("%d tokens", micros)
}
