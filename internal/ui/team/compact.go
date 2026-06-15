// Package team provides UI components for agent team display. M3-09 panel.go
// is the full-screen debug panel; compact.go (M4-09) provides inline compact
// team status cards (TeamCompactItem + ExpandedTeamView) suitable for embedding
// in the chat message stream.
package team

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
