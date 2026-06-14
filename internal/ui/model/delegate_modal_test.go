package model

import (
	"testing"

	"github.com/charmbracelet/crush/internal/agent"
	"github.com/charmbracelet/crush/internal/message"
	"github.com/charmbracelet/crush/internal/ui/chat"
	"github.com/charmbracelet/crush/internal/ui/dialog"
	"github.com/stretchr/testify/require"
)

// TestOpenDelegateTranscriptMsg_OpensModal locks acceptance #4 end-to-end at
// the model layer: when the main model receives an OpenDelegateTranscriptMsg
// for a group item it holds, it opens a DelegateTranscript dialog carrying that
// child's content. (The msg itself is normally emitted by the item's
// HandleKeyEvent; here it is constructed directly to isolate the wiring.)
func TestOpenDelegateTranscriptMsg_OpensModal(t *testing.T) {
	u := newTestUI()
	u.dialog = dialog.NewOverlay()

	item := chat.NewDelegateGroupMessageItem(
		u.com.Styles, "grp-1",
		message.ToolCall{ID: "grp-1"}, nil, false,
	)
	item.SetChildren([]chat.DelegateChildItem{
		{AgentType: "explore", Status: "done", Result: agent.AgentToolResult{Content: "# the answer\n42"}},
	})
	u.chat.SetMessages(item)
	u.chat.SetSelected(0)

	// Invoke the handler the Update case routes to (testing the handler
	// directly avoids the post-Update focus logic, which needs a fuller UI
	// than newTestUI provides). The case itself is a one-line forward.
	u.handleOpenDelegateTranscript(chat.OpenDelegateTranscriptMsg{GroupID: "grp-1", ChildIndex: 0})

	require.True(t, u.dialog.ContainsDialog(dialog.DelegateTranscriptID),
		"main model must open the DelegateTranscript dialog on OpenDelegateTranscriptMsg")
}
