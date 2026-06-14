package dialog

import (
	"charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/crush/internal/ui/common"
	uv "github.com/charmbracelet/ultraviolet"
)

const (
	// DelegateTranscriptID is the dialog ID for the read-only child-transcript
	// modal opened from a DelegateGroupMessageItem.
	DelegateTranscriptID              = "delegate-transcript"
	delegateTranscriptMaxWidth        = 90
	delegateTranscriptPreviewMaxLines = 18
)

// DelegateTranscript is a read-only modal showing one delegate child's result
// content (the markdown the delegate returned). Opened by Enter on a completed
// child in a DelegateGroupMessageItem; closed by Esc. Mirrors dialog.Reasoning
// structurally but drops the selectable list (a transcript is a single markdown
// blob rendered via renderPreviewBox).
type DelegateTranscript struct {
	com       *common.Common
	groupID   string
	agentType string
	content   string // markdown text (agent.AgentToolResult.Content)
}

var _ Dialog = (*DelegateTranscript)(nil)

// NewDelegateTranscript builds the modal. agentType is shown in the title
// (e.g. "Delegate Transcript · explore"); content is the markdown body.
func NewDelegateTranscript(com *common.Common, groupID, agentType, content string) *DelegateTranscript {
	return &DelegateTranscript{
		com:       com,
		groupID:   groupID,
		agentType: agentType,
		content:   content,
	}
}

// ID implements Dialog.
func (d *DelegateTranscript) ID() string { return DelegateTranscriptID }

// HandleMsg implements Dialog. Esc (the dialog CloseKey) closes; everything
// else is a no-op (the modal is read-only — no selection, no input).
func (d *DelegateTranscript) HandleMsg(msg tea.Msg) Action {
	k, ok := msg.(tea.KeyPressMsg)
	if !ok {
		return nil
	}
	if key.Matches(k, CloseKey) {
		return ActionClose{}
	}
	return nil
}

// render builds the modal's view string (the same split reasoning.go uses:
// Draw delegates here, and tests assert on the string directly). Width is the
// available screen width; the dialog is capped to delegateTranscriptMaxWidth.
func (d *DelegateTranscript) render(width int) string {
	t := d.com.Styles
	w := max(0, min(delegateTranscriptMaxWidth, width))
	innerWidth := w - t.Dialog.View.GetHorizontalFrameSize()

	rc := NewRenderContext(t, w)
	if d.agentType != "" {
		rc.Title = "Delegate Transcript · " + d.agentType
	} else {
		rc.Title = "Delegate Transcript"
	}

	body := renderPreviewBox(previewBoxConfig{
		content:  d.content,
		width:    innerWidth,
		maxLines: delegateTranscriptPreviewMaxLines,
		styles:   t,
	})
	rc.AddPart(body)

	return rc.Render()
}

// Draw implements Dialog. Centers the rendered view (no cursor — read-only).
func (d *DelegateTranscript) Draw(scr uv.Screen, area uv.Rectangle) *tea.Cursor {
	view := d.render(area.Dx())
	DrawCenter(scr, area, view)
	return nil
}
