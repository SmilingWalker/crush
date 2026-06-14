package dialog

import (
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/crush/internal/ui/common"
	"github.com/charmbracelet/x/ansi"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestDelegateTranscript_ID locks the const so the main model can refer to it.
func TestDelegateTranscript_ID(t *testing.T) {
	com := common.DefaultCommon(nil)
	d := NewDelegateTranscript(com, "grp-1", "explore", "# found it\nsome detail")
	assert.Equal(t, DelegateTranscriptID, d.ID())
}

// TestDelegateTranscript_EscCloses locks acceptance #4's "Esc 关闭": Esc returns
// ActionClose, an arbitrary key is a no-op (the modal is read-only).
func TestDelegateTranscript_EscCloses(t *testing.T) {
	com := common.DefaultCommon(nil)
	d := NewDelegateTranscript(com, "grp-1", "explore", "# answer")

	_, ok := d.HandleMsg(tea.KeyPressMsg{Code: tea.KeyEsc}).(ActionClose)
	require.True(t, ok, "Esc must return ActionClose")

	// An arbitrary key is a no-op (nil action).
	assert.Nil(t, d.HandleMsg(tea.KeyPressMsg{Code: 'x', Text: "x"}))

	// A non-key message is also a no-op.
	assert.Nil(t, d.HandleMsg(struct{}{}))
}

// TestDelegateTranscript_RenderContainsTitleAndContent locks that the rendered
// view contains the title (with the agent type) and the child's content.
// Asserts on the string returned by the internal render() helper (the same
// split reasoning.go uses: Draw delegates to render()).
func TestDelegateTranscript_RenderContainsTitleAndContent(t *testing.T) {
	com := common.DefaultCommon(nil)
	d := NewDelegateTranscript(com, "grp-1", "explore", "# found it\nsome detail")

	view := ansi.Strip(d.render(90))
	assert.Contains(t, view, "Delegate Transcript")
	assert.Contains(t, view, "explore")
	assert.Contains(t, view, "found it")
	assert.Contains(t, view, "some detail")
}

// TestDelegateTranscript_RenderEmptyContent does not panic and shows a
// placeholder line (renderPreviewBox renders "No preview available" for empty
// content).
func TestDelegateTranscript_RenderEmptyContent(t *testing.T) {
	com := common.DefaultCommon(nil)
	d := NewDelegateTranscript(com, "grp-1", "", "")

	view := ansi.Strip(d.render(90))
	assert.Contains(t, view, "Delegate Transcript")
	assert.Contains(t, view, "No preview available")
}
