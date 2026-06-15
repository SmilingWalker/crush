// Package team provides UI components for agent team display. M3-09 panel.go
// is the full-screen debug panel; compact.go (M4-09) provides inline compact
// team status cards (TeamCompactItem + ExpandedTeamView) suitable for embedding
// in the chat message stream.
package team

import (
	"fmt"
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
