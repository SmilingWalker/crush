package model

import (
	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/crush/internal/ui/team"
)

// toggleTeamPanel opens or closes the M3-09 team debug snapshot panel.
// Feature-gated: if IsAgentTeamEnabled() is false, this is a no-op.
func (m *UI) toggleTeamPanel() {
	if m.teamPanel != nil {
		m.teamPanel = nil
		return
	}
	m.teamPanel = team.New(m.com)
}

// handleTeamPanelMsg delegates messages to the team panel when it is active.
// Returns true if the message was consumed by the team panel.
func (m *UI) handleTeamPanelMsg(msg tea.Msg) (bool, tea.Cmd) {
	if m.teamPanel == nil {
		return false, nil
	}
	switch msg := msg.(type) {
	case tea.KeyPressMsg:
		// Ctrl+T already handled in toggleTeamPanel; close panel on Esc/q
		// is handled by the panel itself via ClosePanelMsg.
		_, cmd := m.teamPanel.Update(msg)
		return true, cmd
	case team.ClosePanelMsg:
		m.teamPanel = nil
		return true, nil
	default:
		// Forward other messages to the panel (tick, data updates, etc.)
		_, cmd := m.teamPanel.Update(msg)
		return true, cmd
	}
}

// renderTeamPanel renders the team panel over the full UI area.
func (m *UI) renderTeamPanel() string {
	if m.teamPanel == nil {
		return ""
	}
	m.teamPanel.SetSize(m.width, m.height)
	return m.teamPanel.View().Content
}
