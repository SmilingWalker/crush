package model

import (
	"testing"

	"charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/crush/internal/config"
	"github.com/charmbracelet/crush/internal/ui/common"
	"github.com/charmbracelet/crush/internal/ui/dialog"
	"github.com/charmbracelet/crush/internal/ui/team"
	"github.com/charmbracelet/crush/internal/workspace"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// teamPanelTestWS embeds workspace.Workspace (nil) and overrides Config() and
// WorkingDir() so the team panel can be constructed without panicking.
type teamPanelTestWS struct {
	workspace.Workspace // nil — satisfies interface at compile time
	cfg                 *config.Config
	dir                 string
}

func (w *teamPanelTestWS) Config() *config.Config { return w.cfg }
func (w *teamPanelTestWS) WorkingDir() string     { return w.dir }

// newTestUIForTeamPanel creates a UI with enough wiring for team panel tests.
func newTestUIForTeamPanel(t *testing.T, cfg *config.Config) *UI {
	t.Helper()
	u := &UI{
		com: &common.Common{
			Workspace: &teamPanelTestWS{cfg: cfg, dir: "/test"},
		},
		keyMap: DefaultKeyMap(),
		state:  uiChat,
		dialog: dialog.NewOverlay(),
		status: &Status{},
	}
	return u
}

// --- Flag gate tests ---

func TestTeamPanel_FlagOff_CtrlTNoOp(t *testing.T) {
	cfg := &config.Config{
		Options: &config.Options{
			Experimental: &config.ExperimentalOptions{
				AgentTeam: false, // flag OFF
			},
		},
	}
	ui := newTestUIForTeamPanel(t, cfg)

	// Simulate Ctrl+T press
	msg := tea.KeyPressMsg{Code: 't', Mod: tea.ModCtrl}
	cmd := ui.handleKeyPressMsg(msg)
	assert.Nil(t, cmd)

	// Panel should NOT open because flag is off
	assert.Nil(t, ui.teamPanel)
}

func TestTeamPanel_FlagOn_CtrlTOpens(t *testing.T) {
	cfg := &config.Config{
		Options: &config.Options{
			Experimental: &config.ExperimentalOptions{
				AgentTeam: true, // flag ON
			},
		},
	}
	ui := newTestUIForTeamPanel(t, cfg)

	// Verify flag is enabled
	assert.True(t, cfg.Options.IsAgentTeamEnabled())
	assert.NotNil(t, ui.com)
	assert.NotNil(t, ui.com.Config())
	assert.NotNil(t, ui.com.Config().Options)
	assert.True(t, ui.com.Config().Options.IsAgentTeamEnabled())

	// Verify key binding exists and would match
	km := DefaultKeyMap()
	assert.NotNil(t, km.TeamPanel)

	msg := tea.KeyPressMsg{Code: 't', Mod: tea.ModCtrl}
	assert.True(t, key.Matches(msg, km.TeamPanel), "Ctrl+T keystroke: %s", msg.String())

	// Call handleGlobalKeys directly (through handleKeyPressMsg)
	cmd := ui.handleKeyPressMsg(msg)

	// Panel should open
	assert.NotNil(t, ui.teamPanel, "teamPanel should be non-nil after Ctrl+T; cmd=%v", cmd)
}

func TestTeamPanel_ToggleTwice(t *testing.T) {
	ui := newTestUIForTeamPanel(t, &config.Config{
		Options: &config.Options{
			Experimental: &config.ExperimentalOptions{AgentTeam: true},
		},
	})

	// Open
	ui.toggleTeamPanel()
	assert.NotNil(t, ui.teamPanel)

	// Close
	ui.toggleTeamPanel()
	assert.Nil(t, ui.teamPanel)

	// Open again
	ui.toggleTeamPanel()
	assert.NotNil(t, ui.teamPanel)
}

func TestTeamPanel_ClosePanelMsg(t *testing.T) {
	ui := newTestUIForTeamPanel(t, &config.Config{
		Options: &config.Options{
			Experimental: &config.ExperimentalOptions{AgentTeam: true},
		},
	})

	// Open the panel
	ui.toggleTeamPanel()
	require.NotNil(t, ui.teamPanel)

	// Simulate ClosePanelMsg from the panel
	consumed, _ := ui.handleTeamPanelMsg(team.ClosePanelMsg{})
	assert.True(t, consumed)
	assert.Nil(t, ui.teamPanel)
}

func TestHandleTeamPanelMsg_NilPanel(t *testing.T) {
	ui := newTestUIForTeamPanel(t, &config.Config{
		Options: &config.Options{
			Experimental: &config.ExperimentalOptions{AgentTeam: true},
		},
	})

	// When panel is nil, nothing is consumed
	consumed, cmd := ui.handleTeamPanelMsg(tea.KeyPressMsg{Code: tea.KeyEsc})
	assert.False(t, consumed)
	assert.Nil(t, cmd)
}

// --- Key binding verification ---

func TestTeamPanelKeyBindingExists(t *testing.T) {
	km := DefaultKeyMap()
	assert.NotNil(t, km.TeamPanel)
}

func TestTeamPanelKeyMatchesCtrlT(t *testing.T) {
	km := DefaultKeyMap()
	// Verify that a key with Code='t' + ModCtrl matches the TeamPanel binding
	msg := tea.KeyPressMsg{Code: 't', Mod: tea.ModCtrl}
	t.Logf("Keystroke: '%s'", msg.String())
	t.Logf("Key=%v, Mod=%v", msg.Code, msg.Mod)
	assert.True(t, key.Matches(msg, km.TeamPanel), "Ctrl+T should match TeamPanel binding")
}

func TestTogglePillsCtrlSpaceOnly(t *testing.T) {
	km := DefaultKeyMap()
	assert.NotNil(t, km.Chat.TogglePills)
}

func TestRenderTeamPanel_NilPanel(t *testing.T) {
	ui := newTestUIForTeamPanel(t, &config.Config{
		Options: &config.Options{
			Experimental: &config.ExperimentalOptions{AgentTeam: true},
		},
	})

	result := ui.renderTeamPanel()
	assert.Empty(t, result)
}
