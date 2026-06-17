// team_bar.go implements the M5-P1 team status bar — a 1-line component that
// polls TeamRunner.Status every 2s and renders a compact team/member summary
// at the bottom of the chat UI.
//
// Format:  🤖 teamName │ ● coder(programmer) ◇ reviewer(idle) │ 2M 1A
// No team: 🤖 No active team

package model

import (
	"context"
	"fmt"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/crush/internal/proto"
	"github.com/charmbracelet/crush/internal/ui/common"
	teamui "github.com/charmbracelet/crush/internal/ui/team"
	"github.com/charmbracelet/crush/internal/workspace"
)

// TeamBarTickMsg is sent every 2s to trigger a status refresh.
type TeamBarTickMsg struct{}

// TeamBar is the M5-P1 team status bar component. It holds a cached
// TeamRuntimeStatus (nil = no active team) and polls the workspace every 2s.
type TeamBar struct {
	status   *TeamBarStatus // nil = no active team
	tickCmd  tea.Cmd
}

// TeamBarStatus is the cached display data for the team bar.
type TeamBarStatus struct {
	teamName      string
	memberNames   []string // sorted for stable display
	memberIcons   []string
	memberRoles   []string
	activeRuns    int
	totalMembers  int
}

// NewTeamBar creates a TeamBar that polls the given workspace for team status.
func NewTeamBar() *TeamBar {
	tb := &TeamBar{}
	tb.tickCmd = tb.tick()
	return tb
}

// Init starts the 2s polling tick. Returns nil so the caller can batch.
func (b *TeamBar) Init() tea.Cmd {
	return b.tickCmd
}

// Update handles tick messages and refreshes the cached status.
func (b *TeamBar) Update(msg tea.Msg, com *common.Common) tea.Cmd {
	switch msg.(type) {
	case TeamBarTickMsg:
		if tr := com.Workspace.TeamRunner(); tr != nil {
			b.refresh(com)
		}
		return b.tick()
	}
	return nil
}

// View renders the 1-line team status bar.
// Format:  🤖 teamName │ ● coder(programmer) ◇ reviewer(idle) │ 2M 1A
// No team: 🤖 No active team
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

	var parts []string
	parts = append(parts, lipgloss.NewStyle().Foreground(lipgloss.Color("69")).Render("🤖 "+b.status.teamName))

	// Build member status string
	var memberStrs []string
	for i := range b.status.memberNames {
		memberStrs = append(memberStrs,
			b.status.memberIcons[i]+" "+b.status.memberNames[i]+"("+b.status.memberRoles[i]+")")
	}
	if len(memberStrs) > 0 {
		parts = append(parts, strings.Join(memberStrs, " "))
	}

	// Build stats: 2M 1A
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

	// Join with separator
	content := strings.Join(parts, " │ ") + statStr

	// Truncate to fit width (subtract padding)
	maxContent := width - 2
	if len(content) > maxContent && maxContent > 3 {
		content = content[:maxContent-1] + "…"
	}

	return barStyle.Render(content)
}

// refresh fetches the latest team status from the workspace and caches it.
func (b *TeamBar) refresh(com *common.Common) {
	// Find the first active team.
	teamID, teamName := b.findFirstTeam(com)
	if teamID == "" {
		b.status = nil
		return
	}

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

	// Sort member names for stable display.
	var names []string
	for name := range runtimeStatus.Members {
		names = append(names, name)
	}
	// Sort member names for deterministic order.
	sortMemberNames(names)

	ts := &TeamBarStatus{
		teamName:     teamName,
		memberNames:  make([]string, 0, len(names)),
		memberIcons:  make([]string, 0, len(names)),
		memberRoles:  make([]string, 0, len(names)),
		activeRuns:   runtimeStatus.ActiveRuns,
		totalMembers: len(runtimeStatus.Members),
	}

	for _, name := range names {
		m := runtimeStatus.Members[name]
		icon := teamui.StatusIcon(string(m.State))
		ts.memberNames = append(ts.memberNames, name)
		ts.memberIcons = append(ts.memberIcons, icon)
		ts.memberRoles = append(ts.memberRoles, m.Role)
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
