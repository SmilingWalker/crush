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
