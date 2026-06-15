package team

import (
	"context"
	"testing"

	"charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/crush/internal/proto"
	"github.com/charmbracelet/crush/internal/workspace"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// mockWorkspace implements workspace.TeamWorkspace for panel tests.
type mockWorkspace struct {
	teams     []proto.Team
	snapshots map[string]proto.TeamSnapshot
	events    map[string][]proto.TeamEvent
	workingDir string
}

func (m *mockWorkspace) CreateTeam(ctx context.Context, req proto.CreateTeamRequest) (proto.TeamSnapshot, error) {
	return proto.TeamSnapshot{}, nil
}

func (m *mockWorkspace) ListTeams(ctx context.Context, req proto.ListTeamsRequest) (proto.ListTeamsResponse, error) {
	return proto.ListTeamsResponse{Teams: m.teams}, nil
}

func (m *mockWorkspace) GetTeamSnapshot(ctx context.Context, workspaceID, teamID string) (proto.TeamSnapshot, error) {
	if snap, ok := m.snapshots[teamID]; ok {
		return snap, nil
	}
	return proto.TeamSnapshot{}, nil
}

func (m *mockWorkspace) SpawnMember(ctx context.Context, req proto.SpawnMemberRequest) (proto.TeamMember, error) {
	return proto.TeamMember{}, nil
}

func (m *mockWorkspace) CreateTask(ctx context.Context, req proto.CreateTeamTaskRequest) (proto.TeamTask, error) {
	return proto.TeamTask{}, nil
}

func (m *mockWorkspace) UpdateTask(ctx context.Context, req proto.UpdateTeamTaskRequest) (proto.TeamTask, error) {
	return proto.TeamTask{}, nil
}

func (m *mockWorkspace) ListEventsAfter(ctx context.Context, workspaceID, teamID string, afterSeq int64, limit int) (proto.TeamEventsResponse, error) {
	if evts, ok := m.events[teamID]; ok {
		var filtered []proto.TeamEvent
		for _, e := range evts {
			if e.Seq > afterSeq {
				filtered = append(filtered, e)
			}
		}
		if len(filtered) > limit {
			filtered = filtered[:limit]
		}
		return proto.TeamEventsResponse{Events: filtered}, nil
	}
	return proto.TeamEventsResponse{}, nil
}

var _ workspace.TeamWorkspace = (*mockWorkspace)(nil)

// --- Tests ---

func TestNewPanel_NilTeamWorkspace(t *testing.T) {
	p := newTestPanel(nil, "/test")
	p.teamWS = nil // override to nil

	cmd := p.Init()
	assert.Nil(t, cmd)

	p.SetSize(80, 24)
	v := p.View()
	assert.Contains(t, v.Content, "not available")

	// Close via Esc
	closeCmd := p.handleKey(tea.KeyPressMsg{Code: tea.KeyEsc})
	assert.NotNil(t, closeCmd)
	msg := closeCmd()
	_, ok := msg.(ClosePanelMsg)
	assert.True(t, ok)
}

func TestPanelInitWithTeams(t *testing.T) {
	m := &mockWorkspace{
		teams: []proto.Team{
			{ID: "t1", Name: "Alpha Team", Status: proto.TeamStatusRunning},
			{ID: "t2", Name: "Beta Team", Status: proto.TeamStatusCreated},
		},
		workingDir: "/test",
	}
	p := newTestPanel(m, "/test")

	cmd := p.Init()
	assert.NotNil(t, cmd)
	assert.True(t, p.loading)
	assert.Equal(t, -1, p.selectedIdx)
}

func TestPanelViewEmptyState(t *testing.T) {
	m := &mockWorkspace{
		teams:      []proto.Team{},
		workingDir: "/test",
	}
	p := newTestPanel(m, "/test")
	p.SetSize(80, 24)

	v := p.View()
	assert.Contains(t, v.Content, "No teams found")
}

func TestPanelViewWithTeams(t *testing.T) {
	m := &mockWorkspace{
		teams: []proto.Team{
			{ID: "t1", Name: "Alpha Team", Status: proto.TeamStatusRunning},
			{ID: "t2", Name: "Beta Team", Status: proto.TeamStatusCreated},
		},
		workingDir: "/test",
	}
	p := newTestPanel(m, "/test")
	p.SetSize(80, 24)
	p.teams = m.teams
	p.loading = false

	v := p.View()
	assert.Contains(t, v.Content, "Alpha Team")
	assert.Contains(t, v.Content, "Beta Team")
	assert.Contains(t, v.Content, "Team Debug Panel")
}

func TestPanelViewSelectedTeamDetail(t *testing.T) {
	m := &mockWorkspace{
		teams: []proto.Team{
			{ID: "t1", Name: "Alpha Team", Status: proto.TeamStatusRunning, CostSoFarMicros: 1234},
		},
		snapshots: map[string]proto.TeamSnapshot{
			"t1": {
				Team:    proto.Team{ID: "t1", Name: "Alpha Team", Status: proto.TeamStatusRunning, CostSoFarMicros: 1234},
				Members: []proto.TeamMember{{ID: "m1", Name: "Alice", Role: "programmer", Status: proto.MemberStatusRunning}},
				Tasks:   []proto.TeamTask{{ID: "tk1", Title: "Build feature X", Status: proto.TaskStatusInProgress}},
			},
		},
		workingDir: "/test",
	}
	p := newTestPanel(m, "/test")
	p.SetSize(80, 24)
	p.teams = m.teams
	p.loading = false
	snap := m.snapshots["t1"]
	p.selectedTeam = &snap
	p.selectedIdx = 0

	v := p.View()
	assert.Contains(t, v.Content, "Alpha Team")
	assert.Contains(t, v.Content, "1234")
	assert.Contains(t, v.Content, "Alice")
	assert.Contains(t, v.Content, "Build feature X")
}

func TestPanelKeyNavigation(t *testing.T) {
	m := &mockWorkspace{
		teams: []proto.Team{
			{ID: "t1", Name: "Team 1", Status: proto.TeamStatusRunning},
			{ID: "t2", Name: "Team 2", Status: proto.TeamStatusCreated},
			{ID: "t3", Name: "Team 3", Status: proto.TeamStatusCompleted},
		},
		workingDir: "/test",
	}
	p := newTestPanel(m, "/test")
	p.teams = m.teams
	p.loading = false
	p.selectedIdx = 0

	// j/down moves selection down
	cmd := p.handleKey(tea.KeyPressMsg{Text: "j", Code: 'j'})
	assert.Nil(t, cmd)
	assert.Equal(t, 1, p.selectedIdx)

	// k/up moves selection up
	cmd = p.handleKey(tea.KeyPressMsg{Text: "k", Code: 'k'})
	assert.Nil(t, cmd)
	assert.Equal(t, 0, p.selectedIdx)

	// down at boundary (clamped)
	p.selectedIdx = 2
	cmd = p.handleKey(tea.KeyPressMsg{Text: "j", Code: 'j'})
	assert.Nil(t, cmd)
	assert.Equal(t, 2, p.selectedIdx)

	// up at boundary (clamped)
	p.selectedIdx = 0
	cmd = p.handleKey(tea.KeyPressMsg{Text: "k", Code: 'k'})
	assert.Nil(t, cmd)
	assert.Equal(t, 0, p.selectedIdx)

	// esc returns ClosePanelMsg
	closeCmd := p.handleKey(tea.KeyPressMsg{Code: tea.KeyEsc})
	assert.NotNil(t, closeCmd)
	msg := closeCmd()
	_, ok := msg.(ClosePanelMsg)
	assert.True(t, ok)

	// q returns ClosePanelMsg
	closeCmd = p.handleKey(tea.KeyPressMsg{Text: "q", Code: 'q'})
	assert.NotNil(t, closeCmd)
	msg = closeCmd()
	_, ok = msg.(ClosePanelMsg)
	assert.True(t, ok)

	// tab toggles focus
	assert.True(t, p.focusLeft)
	cmd = p.handleKey(tea.KeyPressMsg{Code: tea.KeyTab})
	assert.Nil(t, cmd)
	assert.False(t, p.focusLeft)
	cmd = p.handleKey(tea.KeyPressMsg{Code: tea.KeyTab})
	assert.Nil(t, cmd)
	assert.True(t, p.focusLeft)
}

func TestPanelEnterSelectsTeam(t *testing.T) {
	m := &mockWorkspace{
		teams: []proto.Team{
			{ID: "t1", Name: "Team 1", Status: proto.TeamStatusRunning},
		},
		snapshots: map[string]proto.TeamSnapshot{
			"t1": {
				Team:    proto.Team{ID: "t1", Name: "Team 1", Status: proto.TeamStatusRunning},
				Members: []proto.TeamMember{},
				Tasks:   []proto.TeamTask{},
				Runs:    []proto.TeamRun{},
			},
		},
		workingDir: "/test",
	}
	p := newTestPanel(m, "/test")
	p.teams = m.teams
	p.loading = false
	p.selectedIdx = 0
	p.focusLeft = true

	cmd := p.handleKey(tea.KeyPressMsg{Code: tea.KeyEnter})
	require.NotNil(t, cmd)
}

func TestPanelTeamListUpdated(t *testing.T) {
	m := &mockWorkspace{
		teams:      []proto.Team{},
		workingDir: "/test",
	}
	p := newTestPanel(m, "/test")
	p.loading = true

	_, cmd := p.Update(TeamListUpdatedMsg{
		Teams: []proto.Team{
			{ID: "t1", Name: "New Team", Status: proto.TeamStatusRunning},
		},
	})
	assert.Nil(t, cmd)
	assert.False(t, p.loading)
	assert.Len(t, p.teams, 1)
	assert.Equal(t, "New Team", p.teams[0].Name)
}

func TestPanelSetSize(t *testing.T) {
	p := &Panel{workspaceID: "/test", focusLeft: true, selectedIdx: -1}
	p.SetSize(100, 40)
	assert.Equal(t, 100, p.width)
	assert.Equal(t, 40, p.height)
}

func TestTeamListUpdatedClampsIndex(t *testing.T) {
	m := &mockWorkspace{
		teams:      []proto.Team{},
		workingDir: "/test",
	}
	p := newTestPanel(m, "/test")
	p.selectedIdx = 5
	p.loading = true

	_, _ = p.Update(TeamListUpdatedMsg{
		Teams: []proto.Team{{ID: "t1", Name: "Only", Status: proto.TeamStatusRunning}},
	})
	assert.Equal(t, 0, p.selectedIdx)
}

func TestStatusIcon(t *testing.T) {
	assert.Equal(t, "●", statusIcon("running"))
	assert.Equal(t, "●", statusIcon("in_progress"))
	assert.Equal(t, "✓", statusIcon("completed"))
	assert.Equal(t, "✗", statusIcon("failed"))
	assert.Equal(t, "⏳", statusIcon("blocked"))
	assert.Equal(t, "○", statusIcon("queued"))
	assert.Equal(t, "■", statusIcon("stopped"))
	assert.Equal(t, "◇", statusIcon("idle"))
	assert.Equal(t, " ", statusIcon("unknown"))
}

func TestIDPrefix(t *testing.T) {
	assert.Equal(t, "abc", idPrefix("abc", 8))
	assert.Equal(t, "12345678", idPrefix("1234567890abcdef", 8))
	assert.Equal(t, "", idPrefix("", 8))
}

// --- helpers ---

func newTestPanel(m *mockWorkspace, workingDir string) *Panel {
	p := &Panel{
		workspaceID: workingDir,
		focusLeft:   true,
		selectedIdx: -1,
		teamWS:      m,
	}
	p.keyMap.Up = key.NewBinding(key.WithKeys("up", "k"))
	p.keyMap.Down = key.NewBinding(key.WithKeys("down", "j"))
	p.keyMap.Select = key.NewBinding(key.WithKeys("enter"))
	p.keyMap.Close = key.NewBinding(key.WithKeys("q", "esc", "alt+esc"))
	p.keyMap.Tab = key.NewBinding(key.WithKeys("tab"))
	return p
}
