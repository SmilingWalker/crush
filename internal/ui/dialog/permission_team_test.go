package dialog

import (
	"testing"

	"github.com/charmbracelet/crush/internal/permission"
	"github.com/charmbracelet/crush/internal/team"
	"github.com/charmbracelet/crush/internal/ui/common"
	"github.com/charmbracelet/x/ansi"
	"github.com/stretchr/testify/require"
)

// stripAnsi removes ANSI escape sequences so substring assertions work
// on lipgloss-rendered output (which contains color/style codes).
func stripAnsi(s string) string {
	return ansi.Strip(s)
}

func TestTeamPermissionDialog_RendersTeamContext(t *testing.T) {
	t.Parallel()

	com := common.DefaultCommon(nil)

	ctx := team.TeamPermissionContext{
		TeamName:   "my-team",
		MemberName: "coder-1",
		MemberRole: "programmer",
		TaskTitle:  "Implement login",
		ToolName:   "bash",
		Action:     "execute",
		Resource:   "go build ./...",
	}

	perm := permission.PermissionRequest{
		ID:         "perm-test-1",
		ToolCallID: "tc-1",
		ToolName:   "bash",
		Action:     "execute",
		Path:       ".",
	}

	dlg := NewTeamPermissionDialog(com, ctx, perm)
	content := stripAnsi(dlg.View())

	require.Contains(t, content, "my-team")
	require.Contains(t, content, "coder-1")
	require.Contains(t, content, "programmer")
	require.Contains(t, content, "Implement login")
}

func TestTeamPermissionDialog_ButtonsAreTeamSpecific(t *testing.T) {
	t.Parallel()

	com := common.DefaultCommon(nil)

	ctx := team.TeamPermissionContext{
		TeamName:   "my-team",
		MemberName: "coder-1",
		MemberRole: "programmer",
		TaskTitle:  "Implement login",
	}

	perm := permission.PermissionRequest{
		ID:         "perm-test-2",
		ToolCallID: "tc-2",
		ToolName:   "bash",
		Action:     "execute",
		Path:       ".",
	}

	dlg := NewTeamPermissionDialog(com, ctx, perm)
	content := stripAnsi(dlg.View())

	require.Contains(t, content, "Allow Once")
	require.Contains(t, content, "Allow for Task")
	require.Contains(t, content, "Deny")

	// Team dialog should NOT render the standard session option.
	require.NotContains(t, content, "Allow for Session")
}

func TestTeamPermissionDialog_NoTeamContext_UsesStandardButtons(t *testing.T) {
	t.Parallel()

	com := common.DefaultCommon(nil)

	perm := permission.PermissionRequest{
		ID:         "perm-test-3",
		ToolCallID: "tc-3",
		ToolName:   "bash",
		Action:     "execute",
		Path:       ".",
	}

	// Use standard constructor -- no team context.
	dlg := NewPermissions(com, perm)

	width := 80
	totalWidth := width - com.Styles.Dialog.View.GetHorizontalFrameSize() - 2
	buttons := stripAnsi(dlg.renderButtons(totalWidth))

	require.Contains(t, buttons, "Allow for Session")
	require.NotContains(t, buttons, "Allow for Task")
}

func TestTeamPermissionDialog_TeamContext(t *testing.T) {
	t.Parallel()

	com := common.DefaultCommon(nil)

	ctx := team.TeamPermissionContext{
		TeamName:   "test-team",
		MemberName: "reviewer",
		MemberRole: "quality-checker",
		TaskTitle:  "Review code",
	}

	perm := permission.PermissionRequest{
		ID:         "perm-test-4",
		ToolCallID: "tc-4",
		ToolName:   "write",
		Action:     "write",
		Path:       ".",
	}

	dlg := NewTeamPermissionDialog(com, ctx, perm)

	// Verify team context is accessible.
	gotCtx := dlg.TeamContext()
	require.NotNil(t, gotCtx)
	require.Equal(t, "test-team", gotCtx.TeamName)
	require.Equal(t, "reviewer", gotCtx.MemberName)
	require.Equal(t, "quality-checker", gotCtx.MemberRole)
	require.Equal(t, "Review code", gotCtx.TaskTitle)
}

func TestTeamPermissionDialog_TaskTitleOptional(t *testing.T) {
	t.Parallel()

	com := common.DefaultCommon(nil)

	ctx := team.TeamPermissionContext{
		TeamName:   "my-team",
		MemberName: "coder-1",
		MemberRole: "programmer",
		// TaskTitle intentionally empty.
	}

	perm := permission.PermissionRequest{
		ID:         "perm-test-5",
		ToolCallID: "tc-5",
		ToolName:   "glob",
		Action:     "search",
		Path:       ".",
	}

	dlg := NewTeamPermissionDialog(com, ctx, perm)
	content := stripAnsi(dlg.View())

	require.Contains(t, content, "my-team")
	require.Contains(t, content, "coder-1")
	require.NotContains(t, content, "Task:")
}

func TestDefaultTeamButtons(t *testing.T) {
	t.Parallel()

	buttons := DefaultTeamButtons()
	require.Len(t, buttons, 3)
	require.Equal(t, "Allow Once", buttons[0].Label)
	require.Equal(t, "allow_once", buttons[0].Action)
	require.Equal(t, "Allow for Task", buttons[1].Label)
	require.Equal(t, "allow_task", buttons[1].Action)
	require.Equal(t, "Deny", buttons[2].Label)
	require.Equal(t, "deny", buttons[2].Action)
}

func TestBuildTeamHeaderLines(t *testing.T) {
	t.Parallel()

	ctx := team.TeamPermissionContext{
		TeamName:   "my-team",
		MemberName: "coder-1",
		MemberRole: "programmer",
		TaskTitle:  "Implement login",
	}

	lines := BuildTeamHeaderLines(ctx)
	require.Contains(t, lines, "Team: my-team")
	require.Contains(t, lines, "Member: coder-1 (programmer)")
	require.Contains(t, lines, "Task: Implement login")
}

func TestBuildTeamHeaderLines_NoTask(t *testing.T) {
	t.Parallel()

	ctx := team.TeamPermissionContext{
		TeamName:   "my-team",
		MemberName: "coder-1",
		MemberRole: "programmer",
	}

	lines := BuildTeamHeaderLines(ctx)
	require.Contains(t, lines, "Team: my-team")
	require.Contains(t, lines, "Member: coder-1 (programmer)")
	require.NotContains(t, lines, "Task:")
}
