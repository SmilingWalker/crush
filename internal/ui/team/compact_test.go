package team

import (
	"strings"
	"testing"

	"github.com/charmbracelet/crush/internal/team"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestStatusIconMapping(t *testing.T) {
	tests := []struct {
		status string
		want   string
	}{
		// Active/running states
		{"running", "●"},
		{"in_progress", "●"},

		// Completed/done states
		{"completed", "✓"},
		{"done", "✓"},

		// Failed/error states
		{"failed", "✗"},
		{"error", "✗"},

		// Paused
		{"paused", "⏸"},

		// Blocked/waiting
		{"blocked", "⏳"},
		{"waiting_permission", "⏳"},

		// Queued/pending (not yet started)
		{"queued", "○"},
		{"assigned", "○"},
		{"created", "○"},
		{"starting", "○"},

		// Stopped/terminal
		{"stopped", "■"},
		{"canceled", "■"},
		{"shutting_down", "■"},
		{"canceling_turn", "■"},

		// Idle
		{"idle", "◇"},

		// Unknown / empty
		{"unknown", " "},
		{"", " "},
	}

	for _, tt := range tests {
		t.Run(tt.status, func(t *testing.T) {
			got := StatusIcon(tt.status)
			assert.Equal(t, tt.want, got, "StatusIcon(%q) = %q, want %q", tt.status, got, tt.want)
		})
	}
}

// --- TeamCompactItem tests ---

func makeTestStatus(members int, active int) team.TeamRuntimeStatus {
	s := team.TeamRuntimeStatus{
		TeamID:     "team-1",
		Members:    make(map[string]team.MemberRuntimeState, members),
		ActiveRuns: active,
	}
	for i := 0; i < members; i++ {
		id := "m" + string(rune('a'+i))
		s.Members[id] = team.MemberRuntimeState{
			State: team.MemberIdle,
			Role:  "programmer",
		}
	}
	return s
}

func TestNewTeamCompactItem(t *testing.T) {
	t.Run("empty status", func(t *testing.T) {
		item := NewTeamCompactItem()
		require.NotNil(t, item)
		assert.False(t, item.IsExpanded())
		assert.Equal(t, 0, item.scrollOffset)
	})

	t.Run("with provider", func(t *testing.T) {
		p := &stubProvider{}
		item := NewTeamCompactItem(WithProvider(p))
		require.NotNil(t, item)
		assert.NotNil(t, item.provider)
	})

	t.Run("with team name", func(t *testing.T) {
		item := NewTeamCompactItem(WithTeamName("My Team"))
		assert.Equal(t, "My Team", item.teamName)
	})
}

func TestTeamCompactItem_SetStatus(t *testing.T) {
	item := NewTeamCompactItem()
	s := makeTestStatus(3, 1)
	item.SetStatus(s)
	assert.Len(t, item.status.Members, 3)
	assert.Equal(t, 1, item.status.ActiveRuns)
}

func TestTeamCompactItem_ToggleExpanded(t *testing.T) {
	item := NewTeamCompactItem()
	assert.False(t, item.IsExpanded())

	item.ToggleExpanded()
	assert.True(t, item.IsExpanded())

	item.ToggleExpanded()
	assert.False(t, item.IsExpanded())
}

func TestTeamCompactItem_ToggleExpandedResetsScroll(t *testing.T) {
	item := NewTeamCompactItem()
	item.scrollOffset = 5
	item.ToggleExpanded()
	assert.Equal(t, 0, item.scrollOffset)
}

func TestTeamCompactItem_RenderCompact_NoData(t *testing.T) {
	item := NewTeamCompactItem()
	out := item.RenderCompact(80)
	assert.Contains(t, out, "No data")
}

func TestTeamCompactItem_RenderCompact_NoMembers(t *testing.T) {
	item := NewTeamCompactItem(WithTeamName("Empty Team"))
	item.SetStatus(team.TeamRuntimeStatus{TeamID: "t1"})
	out := item.RenderCompact(80)
	assert.Contains(t, out, "Empty Team")
	assert.Contains(t, out, "0 members")
}

func TestTeamCompactItem_RenderCompact_Normal(t *testing.T) {
	item := NewTeamCompactItem(WithTeamName("Build Team"))
	item.SetStatus(makeTestStatus(5, 3))
	out := item.RenderCompact(80)
	assert.Contains(t, out, "Build Team")
	assert.Contains(t, out, "5 members")
	assert.Contains(t, out, "3 active")
}

func TestTeamCompactItem_RenderCompact_NarrowWidth(t *testing.T) {
	item := NewTeamCompactItem(WithTeamName("Build"))
	item.SetStatus(makeTestStatus(5, 3))
	out := item.RenderCompact(30)
	assert.NotEmpty(t, out)
	// Should not panic or produce empty output at narrow widths
	lines := strings.Split(strings.TrimSpace(out), "\n")
	assert.Greater(t, len(lines), 0)
}

// stubProvider implements TeamStatusProvider for tests.
type stubProvider struct {
	status team.TeamRuntimeStatus
	err    error
}

func (s *stubProvider) TeamStatus() (team.TeamRuntimeStatus, error) {
	return s.status, s.err
}

// --- ExpandedTeamView tests ---

// makeTestStatusWithMembers creates a TeamRuntimeStatus with member states
// varying role, status, task, and tool for realistic expanded view rendering.
func makeTestStatusWithMembers(members []struct {
	Name string
	Role string
	State team.MemberStatus
	Task  string
	Tool  string
}) team.TeamRuntimeStatus {
	s := team.TeamRuntimeStatus{
		TeamID:     "team-1",
		Members:    make(map[string]team.MemberRuntimeState),
		ActiveRuns: 0,
	}
	for _, m := range members {
		s.Members[m.Name] = team.MemberRuntimeState{
			State:       m.State,
			Role:        m.Role,
			CurrentTask: m.Task,
			CurrentTool: m.Tool,
		}
		if m.State == team.MemberRunning {
			s.ActiveRuns++
		}
	}
	return s
}

func TestTeamCompactItem_RenderExpanded_NoData(t *testing.T) {
	item := NewTeamCompactItem()
	out := item.RenderExpanded(80, 24)
	assert.Contains(t, out, "No data")
}

func TestTeamCompactItem_RenderExpanded_NoMembers(t *testing.T) {
	item := NewTeamCompactItem(WithTeamName("Empty Team"))
	item.SetStatus(team.TeamRuntimeStatus{TeamID: "t1"})
	out := item.RenderExpanded(80, 24)
	assert.Contains(t, out, "Empty Team")
	assert.Contains(t, out, "0 members")
}

func TestTeamCompactItem_RenderExpanded_Normal(t *testing.T) {
	members := []struct {
		Name string
		Role string
		State team.MemberStatus
		Task  string
		Tool  string
	}{
		{"Alice", "planner", team.MemberRunning, "T1", "write_plan"},
		{"Bob", "programmer", team.MemberIdle, "", ""},
		{"Charlie", "programmer", team.MemberRunning, "T3", "edit_file"},
		{"Diana", "reviewer", team.MemberStopped, "T2", ""},
		{"Eve", "programmer", team.MemberBlocked, "T4", "wait_deps"},
	}

	item := NewTeamCompactItem(WithTeamName("Build Team"))
	item.SetStatus(makeTestStatusWithMembers(members))
	out := item.RenderExpanded(80, 24)

	assert.Contains(t, out, "Build Team")
	assert.Contains(t, out, "Alice")
	assert.Contains(t, out, "Bob")
	assert.Contains(t, out, "Charlie")
	assert.Contains(t, out, "Diana")
	assert.Contains(t, out, "Eve")
	assert.Contains(t, out, "5 members")
	assert.Contains(t, out, "2 active")
}

func TestTeamCompactItem_RenderExpanded_24LineConstraint(t *testing.T) {
	// Create 50 members — should truncate at 24 lines
	members := make([]struct {
		Name string
		Role string
		State team.MemberStatus
		Task  string
		Tool  string
	}, 50)
	for i := range members {
		members[i] = struct {
			Name string
			Role string
			State team.MemberStatus
			Task  string
			Tool  string
		}{
			Name:  "m" + string(rune('a'+i%26)),
			Role:  "programmer",
			State: team.MemberIdle,
		}
	}

	item := NewTeamCompactItem(WithTeamName("Large Team"))
	item.SetStatus(makeTestStatusWithMembers(members))
	out := item.RenderExpanded(80, 24)

	lines := strings.Split(strings.TrimSpace(out), "\n")
	assert.LessOrEqual(t, len(lines), 24, "expanded view must not exceed 24 lines (got %d)", len(lines))
	// Should have a truncation footer
	full := strings.Join(lines, "\n")
	assert.Contains(t, full, "more members")
}

func TestTeamCompactItem_RenderExpanded_ScrollOffset(t *testing.T) {
	// Create 30 members to test scrolling
	members := make([]struct {
		Name string
		Role string
		State team.MemberStatus
		Task  string
		Tool  string
	}, 30)
	for i := range members {
		members[i] = struct {
			Name string
			Role string
			State team.MemberStatus
			Task  string
			Tool  string
		}{
			Name:  "m" + string(rune('a'+i%26))+string(rune('0'+i/26)),
			Role:  "programmer",
			State: team.MemberIdle,
		}
	}

	item := NewTeamCompactItem(WithTeamName("Scroll Team"))
	item.SetStatus(makeTestStatusWithMembers(members))

	// Without scroll — first member "ma0" should be visible
	outNoScroll := item.RenderExpanded(80, 24)
	assert.Contains(t, outNoScroll, "ma0")

	// With scroll offset = 10 — first member should be hidden
	item.scrollOffset = 10
	outScroll := item.RenderExpanded(80, 24)
	assert.NotEqual(t, outNoScroll, outScroll, "scrolled output should differ from unscrolled")
	assert.NotContains(t, outScroll, "ma0", "scrolled output should hide first member")
	// Should show members above footer
	assert.Contains(t, outScroll, "members above")
}

func TestTeamCompactItem_RenderExpanded_NarrowWidth(t *testing.T) {
	members := []struct {
		Name string
		Role string
		State team.MemberStatus
		Task  string
		Tool  string
	}{
		{"Ali", "planner", team.MemberRunning, "T1", "write"},
		{"Bob", "pgmr", team.MemberIdle, "", ""},
	}

	item := NewTeamCompactItem(WithTeamName("T"))
	item.SetStatus(makeTestStatusWithMembers(members))
	out := item.RenderExpanded(40, 24)
	assert.NotEmpty(t, out)
	assert.Contains(t, out, "Ali")
	assert.Contains(t, out, "Bob")
}
