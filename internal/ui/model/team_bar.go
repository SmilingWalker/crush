// team_bar.go implements the M5 team status bar.
//
// M5-P1: read-only 1-line status display polling every 2s
// M5-P2: interactive navigation — ← → move highlight, Enter confirms switch, ↑ ↓ toggle focus
//
// Format:  🤖 teamName │ ● coder(programmer) ◇ reviewer(idle) │ 2M 1A
// No team: 🤖 No active team

package model

import (
	"context"
	"fmt"
	"strings"
	"time"

	"charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/crush/internal/proto"
	"github.com/charmbracelet/crush/internal/ui/common"
	teamui "github.com/charmbracelet/crush/internal/ui/team"
	"github.com/charmbracelet/crush/internal/workspace"
)

// TeamBarTickMsg is sent every 2s to trigger a status refresh.
type TeamBarTickMsg struct{}

// SessionSwitchMsg requests the UI to switch the chat view to a different
// session. Emitted by TeamBar when the user presses Enter on a member.
type SessionSwitchMsg struct {
	SessionID string
}

// FocusEditorMsg requests the UI to move focus back to the editor.
// Emitted by TeamBar when the user presses ↑ or ↓ in the bar.
type FocusEditorMsg struct{}

// TeamBar is the interactive team status bar (M5-P1 + M5-P2).
// It polls TeamRunner.Status every 2s and supports ← → ↑ ↓ Enter navigation.
type TeamBar struct {
	status  *TeamBarStatus // nil = no active team
	tickCmd tea.Cmd

	// M5-P2: navigation state
	selectedIndex int    // index into status.memberNames, 0 = leader/first
	focused       bool   // true when TeamBar is the active focus (receives keys)
	teamID        string // cached team ID for refresh

	// Pre-cached key bindings.
	keyLeft   key.Binding
	keyRight  key.Binding
	keyUpDown key.Binding
	keyEnter  key.Binding
}

// TeamBarStatus is the cached display data for the team bar.
type TeamBarStatus struct {
	teamName     string
	memberNames  []string // sorted for stable display
	memberIcons  []string
	memberRoles  []string
	sessionIDs   []string // per-member session ID for session switching (M5-P2)
	activeRuns   int
	totalMembers int
}

// NewTeamBar creates a TeamBar that polls the given workspace for team status.
func NewTeamBar() *TeamBar {
	tb := &TeamBar{
		keyLeft:   key.NewBinding(key.WithKeys("left")),
		keyRight:  key.NewBinding(key.WithKeys("right")),
		keyUpDown: key.NewBinding(key.WithKeys("up", "down")),
		keyEnter:  key.NewBinding(key.WithKeys("enter")),
	}
	tb.tickCmd = tb.tick()
	return tb
}

// Init starts the 2s polling tick.
func (b *TeamBar) Init() tea.Cmd {
	return b.tickCmd
}

// SetFocused sets the TeamBar focus state.
func (b *TeamBar) SetFocused(v bool) {
	b.focused = v
}

// IsFocused reports whether the TeamBar currently has focus.
func (b *TeamBar) IsFocused() bool {
	return b.focused
}

// HasTeam reports whether the TeamBar has a cached team with members.
func (b *TeamBar) HasTeam() bool {
	return b.status != nil && len(b.status.memberNames) > 0
}

// SelectedSessionID returns the session ID of the currently selected member,
// or "" if no status or no members.
func (b *TeamBar) SelectedSessionID() string {
	if b.status == nil || len(b.status.sessionIDs) == 0 {
		return ""
	}
	if b.selectedIndex < 0 || b.selectedIndex >= len(b.status.sessionIDs) {
		return ""
	}
	return b.status.sessionIDs[b.selectedIndex]
}

// SelectFirst selects the first member (leader) if available.
func (b *TeamBar) SelectFirst() {
	if b.status != nil && len(b.status.memberNames) > 0 {
		b.selectedIndex = 0
	}
}

// Update handles tick messages and key navigation.
func (b *TeamBar) Update(msg tea.Msg, com *common.Common) tea.Cmd {
	switch msg := msg.(type) {
	case TeamBarTickMsg:
		if tr := com.Workspace.TeamRunner(); tr != nil {
			b.refresh(com)
		}
		return b.tick()

	case tea.KeyPressMsg:
		if !b.focused {
			return nil
		}
		// ↑/↓ always return to editor, even without a team.
		if key.Matches(msg, b.keyUpDown) {
			return func() tea.Msg { return FocusEditorMsg{} }
		}
		if b.status == nil || len(b.status.memberNames) == 0 {
			return nil
		}
		// Clamp before navigation.
		if b.selectedIndex >= len(b.status.memberNames) {
			b.selectedIndex = len(b.status.memberNames) - 1
		}
		if b.selectedIndex < 0 {
			b.selectedIndex = 0
		}
		switch {
		case key.Matches(msg, b.keyLeft):
			if b.selectedIndex > 0 {
				b.selectedIndex--
			}
		case key.Matches(msg, b.keyRight):
			if b.selectedIndex < len(b.status.memberNames)-1 {
				b.selectedIndex++
			}
		case key.Matches(msg, b.keyEnter):
			return b.switchSessionCmd()
		}
	}
	return nil
}

// switchSessionCmd returns a command that emits a SessionSwitchMsg for the
// currently selected member's session ID.
func (b *TeamBar) switchSessionCmd() tea.Cmd {
	sid := b.SelectedSessionID()
	return func() tea.Msg {
		return SessionSwitchMsg{SessionID: sid}
	}
}

// View renders the 1-line team status bar with the selected member highlighted.
func (b *TeamBar) View(width int) string {
	barStyle := lipgloss.NewStyle().
		Width(width).
		Padding(0, 1).
		Foreground(lipgloss.Color("241"))

	if b.status == nil {
		icon := lipgloss.NewStyle().Foreground(lipgloss.Color("69")).Render("🤖")
		label := lipgloss.NewStyle().Foreground(lipgloss.Color("241")).Render(" No active team")
		return barStyle.Render(icon + label)
	}

	// Clamp selectedIndex if member count changed during refresh.
	if b.selectedIndex >= len(b.status.memberNames) {
		b.selectedIndex = len(b.status.memberNames) - 1
	}
	if b.selectedIndex < 0 {
		b.selectedIndex = 0
	}

	var parts []string
	parts = append(parts, lipgloss.NewStyle().Foreground(lipgloss.Color("69")).Render("🤖 "+b.status.teamName))

	var memberStrs []string
	for i := range b.status.memberNames {
		memberStr := b.status.memberIcons[i] + " " + b.status.memberNames[i] + "(" + b.status.memberRoles[i] + ")"
		if b.focused && i == b.selectedIndex {
			memberStr = lipgloss.NewStyle().
				Background(lipgloss.Color("69")).
				Foreground(lipgloss.Color("0")).
				Render(memberStr)
		}
		memberStrs = append(memberStrs, memberStr)
	}
	if len(memberStrs) > 0 {
		parts = append(parts, strings.Join(memberStrs, " "))
	}

	var stats []string
	if b.status.totalMembers > 0 {
		stats = append(stats, fmt.Sprintf("%dM", b.status.totalMembers))
	}
	if b.status.activeRuns > 0 {
		stats = append(stats, fmt.Sprintf("%dA", b.status.activeRuns))
	}
	var statStr string
	if len(stats) > 0 {
		statStr = "  " + strings.Join(stats, " ")
	}

	content := strings.Join(parts, " │ ") + statStr

	maxContent := width - 2
	if len(content) > maxContent && maxContent > 3 {
		content = content[:maxContent-1] + "…"
	}

	return barStyle.Render(content)
}

// refresh fetches the latest team status from the workspace and caches it.
func (b *TeamBar) refresh(com *common.Common) {
	teamID, teamName := b.findFirstTeam(com)
	if teamID == "" {
		b.status = nil
		return
	}
	b.teamID = teamID

	tr := com.Workspace.TeamRunner()
	if tr == nil {
		b.status = nil
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	runtimeStatus, err := tr.Status(ctx, teamID)
	if err != nil {
		b.status = nil
		return
	}

	var names []string
	for name := range runtimeStatus.Members {
		names = append(names, name)
	}
	sortMemberNames(names)

	ts := &TeamBarStatus{
		teamName:     teamName,
		memberNames:  make([]string, 0, len(names)),
		memberIcons:  make([]string, 0, len(names)),
		memberRoles:  make([]string, 0, len(names)),
		sessionIDs:   make([]string, 0, len(names)),
		activeRuns:   runtimeStatus.ActiveRuns,
		totalMembers: len(runtimeStatus.Members),
	}

	for _, name := range names {
		m := runtimeStatus.Members[name]
		icon := teamui.StatusIcon(string(m.State))
		ts.memberNames = append(ts.memberNames, name)
		ts.memberIcons = append(ts.memberIcons, icon)
		ts.memberRoles = append(ts.memberRoles, m.Role)
		ts.sessionIDs = append(ts.sessionIDs, m.SessionID)
	}

	if b.selectedIndex >= len(names) {
		b.selectedIndex = 0
	}

	b.status = ts
}

// findFirstTeam returns the ID and name of the first non-archived team, or "" if none.
func (b *TeamBar) findFirstTeam(com *common.Common) (teamID, teamName string) {
	tw, ok := com.Workspace.(workspace.TeamWorkspace)
	if !ok {
		return "", ""
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	resp, err := tw.ListTeams(ctx, proto.ListTeamsRequest{
		WorkspaceID: "default",
	})
	if err != nil || len(resp.Teams) == 0 {
		return "", ""
	}

	t := resp.Teams[0]
	name := t.Name
	if name == "" {
		name = t.ID
	}
	return t.ID, name
}

// tick returns a command that fires a TeamBarTickMsg after 2s.
func (b *TeamBar) tick() tea.Cmd {
	return tea.Tick(2*time.Second, func(t time.Time) tea.Msg {
		return TeamBarTickMsg{}
	})
}

// sortMemberNames sorts member names alphabetically.
func sortMemberNames(names []string) {
	for i := 0; i < len(names); i++ {
		for j := i + 1; j < len(names); j++ {
			if names[i] > names[j] {
				names[i], names[j] = names[j], names[i]
			}
		}
	}
}
