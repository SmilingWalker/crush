// Package team provides the M3-09 debug snapshot UI panel for agent teams.
// The Panel type implements tea.Model and renders a left teams list (30%) +
// right detail (70%) split. It polls TeamWorkspace every 2s for live updates.
package team

import (
	"context"
	"fmt"
	"time"

	"charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/crush/internal/proto"
	"github.com/charmbracelet/crush/internal/ui/common"
	"github.com/charmbracelet/crush/internal/workspace"
)

// TickMsg is sent periodically while the panel is open to trigger a data
// refresh via poll.
type TickMsg struct{}

// TeamListUpdatedMsg is sent when the team list has been fetched.
type TeamListUpdatedMsg struct {
	Teams []proto.Team
	Err   error
}

// TeamDetailUpdatedMsg is sent when a team detail snapshot has been fetched.
type TeamDetailUpdatedMsg struct {
	Snapshot proto.TeamSnapshot
	Err      error
}

// TeamEventsUpdatedMsg is sent after a team events poll.
type TeamEventsUpdatedMsg struct {
	Events []proto.TeamEvent
	Err    error
}

// ClosePanelMsg is returned to signal the parent model to close this panel.
type ClosePanelMsg struct{}

// Panel is the debug snapshot UI panel for agent teams.
// It implements tea.Model and renders a left teams list (30%) + right detail
// (70%). Data is fetched from workspace.TeamWorkspace via type assertion.
type Panel struct {
	com    *common.Common
	width  int
	height int

	// Data
	teams        []proto.Team
	selectedTeam *proto.TeamSnapshot
	selectedIdx  int
	events       []proto.TeamEvent
	lastEventSeq int64

	// UI state
	loading     bool
	err         string
	focusLeft   bool // true = left list focused, false = right detail focused
	workspaceID string

	// Key bindings
	keyMap struct {
		Up     key.Binding
		Down   key.Binding
		Select key.Binding
		Close  key.Binding
		Tab    key.Binding
	}

	// teamWS is the type-asserted TeamWorkspace (nil if not available).
	teamWS workspace.TeamWorkspace
}

// New creates a new TeamPanel. It type-asserts com.Workspace to
// workspace.TeamWorkspace; the panel gracefully degrades if unavailable.
func New(com *common.Common) *Panel {
	p := &Panel{
		com:         com,
		workspaceID: com.Workspace.WorkingDir(),
		focusLeft:   true,
		selectedIdx: -1,
	}
	if tw, ok := com.Workspace.(workspace.TeamWorkspace); ok {
		p.teamWS = tw
	}

	p.keyMap.Up = key.NewBinding(key.WithKeys("up", "k"))
	p.keyMap.Down = key.NewBinding(key.WithKeys("down", "j"))
	p.keyMap.Select = key.NewBinding(key.WithKeys("enter"))
	p.keyMap.Close = key.NewBinding(key.WithKeys("q", "esc", "alt+esc"))
	p.keyMap.Tab = key.NewBinding(key.WithKeys("tab"))

	return p
}

// SetSize updates the panel dimensions.
func (p *Panel) SetSize(width, height int) {
	p.width = width
	p.height = height
}

// Init implements tea.Model. Fires the initial team list fetch + starts the
// polling tick.
func (p *Panel) Init() tea.Cmd {
	if p.teamWS == nil {
		return nil
	}
	p.loading = true
	return tea.Batch(p.fetchTeams, p.tickCmd())
}

// Update implements tea.Model.
func (p *Panel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyPressMsg:
		return p, p.handleKey(msg)
	case TeamListUpdatedMsg:
		if msg.Err != nil {
			p.err = msg.Err.Error()
		} else {
			p.teams = msg.Teams
			p.err = ""
		}
		p.loading = false
		// Clamp selected index
		if p.selectedIdx >= len(p.teams) {
			p.selectedIdx = len(p.teams) - 1
		}
		return p, nil
	case TeamDetailUpdatedMsg:
		if msg.Err != nil {
			p.err = msg.Err.Error()
		} else {
			p.selectedTeam = &msg.Snapshot
			p.err = ""
		}
		return p, nil
	case TeamEventsUpdatedMsg:
		if msg.Err == nil && len(msg.Events) > 0 {
			// Replace events and advance the seq cursor.
			p.events = msg.Events
			for _, e := range msg.Events {
				if e.Seq > p.lastEventSeq {
					p.lastEventSeq = e.Seq
				}
			}
		}
		return p, nil
	case TickMsg:
		cmds := []tea.Cmd{p.tickCmd()}
		if p.teamWS != nil {
			cmds = append(cmds, p.fetchTeams)
			if p.selectedTeam != nil {
				teamID := p.selectedTeam.Team.ID
				cmds = append(cmds, p.fetchDetail(teamID), p.fetchEvents(teamID))
			}
		}
		return p, tea.Batch(cmds...)
	}
	return p, nil
}

// View renders the panel. Layout:
//
//	empty / error / loading
//	| teams (30%) | separator | detail (70%) |
func (p *Panel) View() tea.View {
	var v tea.View
	if p.width <= 0 {
		return v
	}

	if p.teamWS == nil {
		v.Content = p.renderCentered("Agent team not available.\nEnable Experimental.AgentTeam in config.")
		return v
	}
	if p.err != "" {
		v.Content = p.renderCentered(fmt.Sprintf("Error: %s", p.err))
		return v
	}
	if p.loading && len(p.teams) == 0 {
		v.Content = p.renderCentered("Loading teams...")
		return v
	}
	if len(p.teams) == 0 {
		v.Content = p.renderCentered("No teams found.\nCreate a team to get started.")
		return v
	}

	// Calculate split
	leftWidth := p.width * 30 / 100
	if leftWidth < 20 {
		leftWidth = 20
	}
	rightWidth := p.width - leftWidth - 1 // 1 for separator
	if rightWidth < 20 {
		rightWidth = 20
	}

	left := p.renderTeamList(leftWidth)
	sep := p.renderSeparator(leftWidth)
	right := p.renderDetail(rightWidth)
	content := lipgloss.JoinHorizontal(lipgloss.Top, left, sep, right)

	// Wrap in a border
	border := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(lipgloss.Color("63")).
		Padding(0, 1).
		Width(p.width - 2)

	title := lipgloss.NewStyle().
		Bold(true).
		Foreground(lipgloss.Color("63")).
		Render("Team Debug Panel")

	footer := lipgloss.NewStyle().
		Foreground(lipgloss.Color("241")).
		Render("j/k navigate  enter select  tab switch  q/esc close")

	v.Content = lipgloss.JoinVertical(lipgloss.Top,
		title,
		border.Render(content),
		footer,
	)
	return v
}

// --- render helpers ---

func (p *Panel) renderCentered(msg string) string {
	style := lipgloss.NewStyle().
		Width(p.width).
		Height(p.height - 2).
		Align(lipgloss.Center, lipgloss.Center)
	return style.Render(msg)
}

func (p *Panel) renderSeparator(_ int) string {
	return lipgloss.NewStyle().
		Foreground(lipgloss.Color("63")).
		Render("│")
}

func (p *Panel) renderTeamList(width int) string {
	var lines []string
	headerStyle := lipgloss.NewStyle().
		Bold(true).
		Foreground(lipgloss.Color("63")).
		Width(width).
		Border(lipgloss.NormalBorder(), false, false, true, false).
		BorderForeground(lipgloss.Color("63")).
		Padding(0, 1)
	lines = append(lines, headerStyle.Render("Teams"))

	for i, t := range p.teams {
		name := t.Name
		if name == "" {
			name = t.ID
		}
		// Truncate name if too long
		maxNameLen := width - 12
		if len(name) > maxNameLen && maxNameLen > 0 {
			name = name[:maxNameLen]
		}

		statusIcon := statusIcon(string(t.Status))
		indicator := "  "
		if i == p.selectedIdx {
			if p.focusLeft {
				indicator = "> "
			} else {
				indicator = "  "
			}
		}

		line := fmt.Sprintf("%s%s %s", indicator, statusIcon, name)
		s := lipgloss.NewStyle().Width(width).Padding(0, 1)
		if i == p.selectedIdx && p.focusLeft {
			s = s.Background(lipgloss.Color("63")).Foreground(lipgloss.Color("255"))
		} else if i == p.selectedIdx {
			s = s.Background(lipgloss.Color("240"))
		}
		lines = append(lines, s.Render(line))
	}

	teamStyle := lipgloss.NewStyle().Width(width).Padding(0, 1)
	return teamStyle.Render(lipgloss.JoinVertical(lipgloss.Top, lines...))
}

func (p *Panel) renderDetail(width int) string {
	if p.selectedTeam == nil {
		return lipgloss.NewStyle().
			Width(width).
			Height(p.height - 4).
			Align(lipgloss.Center, lipgloss.Center).
			Foreground(lipgloss.Color("241")).
			Render("Select a team to view details")
	}

	snap := p.selectedTeam
	var sections []string

	headerStyle := lipgloss.NewStyle().
		Bold(true).
		Foreground(lipgloss.Color("63")).
		Width(width).
		Border(lipgloss.NormalBorder(), false, false, true, false).
		BorderForeground(lipgloss.Color("63")).
		Padding(0, 1)
	sectionStyle := lipgloss.NewStyle().
		Bold(true).
		Foreground(lipgloss.Color("69")).
		Padding(0, 1)
	itemStyle := lipgloss.NewStyle().
		Padding(0, 1).
		Width(width)

	// Team info header
	sections = append(sections, headerStyle.Render(snap.Team.Name))
	info := fmt.Sprintf("Status: %s  Members: %d  Tasks: %d  Runs: %d  Cost: %dμs",
		snap.Team.Status, len(snap.Members), len(snap.Tasks), len(snap.Runs),
		snap.Team.CostSoFarMicros)
	sections = append(sections, itemStyle.Render(info))
	if snap.Team.Description != "" {
		sections = append(sections, itemStyle.Render("Desc: "+snap.Team.Description))
	}

	// Members
	if len(snap.Members) > 0 {
		sections = append(sections, sectionStyle.Render("--- Members ---"))
		for _, m := range snap.Members {
			task := ""
			if m.CurrentTaskID != nil {
				task = fmt.Sprintf(" [task:%s]", idPrefix(*m.CurrentTaskID, 8))
			}
			line := fmt.Sprintf("%s %s %s%s", statusIcon(string(m.Status)), m.Name, m.Role, task)
			sections = append(sections, itemStyle.Render(line))
		}
	}

	// Tasks
	if len(snap.Tasks) > 0 {
		sections = append(sections, sectionStyle.Render("--- Tasks ---"))
		for _, t := range snap.Tasks {
			assignee := ""
			if t.AssigneeMemberID != nil {
				assignee = fmt.Sprintf(" [%s]", idPrefix(*t.AssigneeMemberID, 8))
			}
			line := fmt.Sprintf("%s %s %s%s", statusIcon(string(t.Status)), t.Status, t.Title, assignee)
			sections = append(sections, itemStyle.Render(line))
		}
	}

	// Runs
	if len(snap.Runs) > 0 {
		sections = append(sections, sectionStyle.Render("--- Runs ---"))
		for _, r := range snap.Runs {
			errStr := ""
			if r.Error != "" {
				errStr = " ERR"
			}
			line := fmt.Sprintf("%s %s #%d%s", statusIcon(string(r.Status)), r.Status, r.Attempt, errStr)
			sections = append(sections, itemStyle.Render(line))
		}
	}

	// Events
	if len(p.events) > 0 {
		sections = append(sections, sectionStyle.Render("--- Events ---"))
		maxEvents := 10
		start := 0
		if len(p.events) > maxEvents {
			start = len(p.events) - maxEvents
		}
		for _, e := range p.events[start:] {
			line := fmt.Sprintf("[%d] %s %s/%s", e.Seq, e.EventType, e.EntityType, idPrefix(e.EntityID, 8))
			sections = append(sections, itemStyle.Render(line))
		}
	}

	return lipgloss.JoinVertical(lipgloss.Top, sections...)
}

// --- data fetching ---

func (p *Panel) fetchTeams() tea.Msg {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	resp, err := p.teamWS.ListTeams(ctx, proto.ListTeamsRequest{
		WorkspaceID: p.workspaceID,
	})
	return TeamListUpdatedMsg{Teams: resp.Teams, Err: err}
}

func (p *Panel) fetchDetail(teamID string) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		snap, err := p.teamWS.GetTeamSnapshot(ctx, p.workspaceID, teamID)
		return TeamDetailUpdatedMsg{Snapshot: snap, Err: err}
	}
}

func (p *Panel) fetchEvents(teamID string) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		resp, err := p.teamWS.ListEventsAfter(ctx, p.workspaceID, teamID, p.lastEventSeq, 50)
		return TeamEventsUpdatedMsg{Events: resp.Events, Err: err}
	}
}

func (p *Panel) tickCmd() tea.Cmd {
	return tea.Tick(2*time.Second, func(t time.Time) tea.Msg {
		return TickMsg{}
	})
}

// --- keyboard handling ---

func (p *Panel) handleKey(msg tea.KeyPressMsg) tea.Cmd {
	switch {
	case key.Matches(msg, p.keyMap.Close):
		return func() tea.Msg { return ClosePanelMsg{} }
	case key.Matches(msg, p.keyMap.Up):
		if p.focusLeft {
			p.moveSelection(-1)
		}
		return nil
	case key.Matches(msg, p.keyMap.Down):
		if p.focusLeft {
			p.moveSelection(1)
		}
		return nil
	case key.Matches(msg, p.keyMap.Select):
		if p.focusLeft && p.selectedIdx >= 0 && p.selectedIdx < len(p.teams) {
			team := p.teams[p.selectedIdx]
			return tea.Batch(p.fetchDetail(team.ID), p.fetchEvents(team.ID))
		}
		return nil
	case key.Matches(msg, p.keyMap.Tab):
		p.focusLeft = !p.focusLeft
		return nil
	}
	return nil
}

func (p *Panel) moveSelection(delta int) {
	if len(p.teams) == 0 {
		return
	}
	p.selectedIdx += delta
	if p.selectedIdx < 0 {
		p.selectedIdx = 0
	}
	if p.selectedIdx >= len(p.teams) {
		p.selectedIdx = len(p.teams) - 1
	}
}

// --- helpers ---

func statusIcon(status string) string {
	switch status {
	case "running", "in_progress":
		return "●" // ●
	case "completed":
		return "✓" // ✓
	case "failed":
		return "✗" // ✗
	case "paused":
		return "⏸" // ⏸
	case "blocked", "waiting_permission":
		return "⏳" // ⏳
	case "queued", "assigned", "created", "starting":
		return "○" // ○
	case "stopped", "canceled":
		return "■" // ■
	case "idle":
		return "◇" // ◇
	default:
		return " "
	}
}

func idPrefix(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n]
}
