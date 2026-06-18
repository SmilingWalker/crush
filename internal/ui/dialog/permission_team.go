// permission_team.go implements the M5 Team Permission UI — extending the
// existing permissions dialog with team context (team name, member name,
// member role, task title) when a member's tool call is pending approval.
//
// TeamPermissionDialog wraps the existing Permissions dialog. When teamCtx
// is set on Permissions, the header prepends team info and the buttons
// switch to team-specific labels (Allow Once / Allow for Task / Deny).
// When teamCtx is nil, behavior is identical to before this change.

package dialog

import (
	"strings"

	"github.com/charmbracelet/crush/internal/permission"
	"github.com/charmbracelet/crush/internal/team"
	"github.com/charmbracelet/crush/internal/ui/common"
)

// TeamPermissionButton represents a single action button in the team
// permission dialog.
type TeamPermissionButton struct {
	Label  string
	Action string // "allow_once", "allow_task", "deny"
}

// TeamPermissionDialog wraps the base Permissions dialog to add team
// context to the display (M5 Permission UI).
//
// It implements View() for use in tests and can be used as a Dialog
// since it embeds *Permissions (which already implements Dialog).
type TeamPermissionDialog struct {
	*Permissions
}

// NewTeamPermissionDialog creates a Permissions dialog pre-configured
// with team context for M5 team permission UI.
//
// It returns a *TeamPermissionDialog that wraps the underlying
// *Permissions dialog. Callers can use it anywhere a Dialog is expected.
func NewTeamPermissionDialog(com *common.Common, ctx team.TeamPermissionContext, perm permission.PermissionRequest, opts ...PermissionsOption) *TeamPermissionDialog {
	p := NewPermissions(com, perm, opts...)
	p.teamCtx = &ctx
	return &TeamPermissionDialog{Permissions: p}
}

// View renders the team-enhanced permission dialog as a string.
// Uses a default content width of 80 characters.
//
// For P3 this is a string renderer; full interactive Dialog integration
// is provided by the embedded *Permissions (which implements Dialog).
func (d *TeamPermissionDialog) View() string {
	const defaultWidth = 80

	t := d.com.Styles
	contentWidth := defaultWidth - t.Dialog.View.GetHorizontalFrameSize() - 2 // 2 = dialog horizontal padding

	header := d.renderHeader(contentWidth)
	content := d.renderContent(contentWidth)
	buttons := d.renderButtons(contentWidth)

	var parts []string
	if header != "" {
		parts = append(parts, header)
	}
	if content != "" {
		parts = append(parts, content)
	}
	if buttons != "" {
		parts = append(parts, "", buttons)
	}

	return strings.Join(parts, "\n")
}

// TeamContext returns the team context associated with this dialog, or nil
// if this is a standard (non-team) permission dialog.
func (d *TeamPermissionDialog) TeamContext() *team.TeamPermissionContext {
	return d.teamCtx
}

// DefaultTeamButtons returns the standard set of team permission buttons.
func DefaultTeamButtons() []TeamPermissionButton {
	return []TeamPermissionButton{
		{Label: "Allow Once", Action: "allow_once"},
		{Label: "Allow for Task", Action: "allow_task"},
		{Label: "Deny", Action: "deny"},
	}
}

// buildTeamHeaderLines builds the team context header lines rendered
// above the standard permission header. Exported for use by external
// renderers that need to build team context separately.
func BuildTeamHeaderLines(ctx team.TeamPermissionContext) string {
	var sb strings.Builder
	sb.WriteString("Team: ")
	sb.WriteString(ctx.TeamName)
	sb.WriteString("\nMember: ")
	sb.WriteString(ctx.MemberName)
	sb.WriteString(" (")
	sb.WriteString(ctx.MemberRole)
	sb.WriteString(")")
	if ctx.TaskTitle != "" {
		sb.WriteString("\nTask: ")
		sb.WriteString(ctx.TaskTitle)
	}
	return sb.String()
}

