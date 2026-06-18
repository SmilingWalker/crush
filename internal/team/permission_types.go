// permission_types.go holds team-permission domain types shared between the
// PermissionBridge (producer) and the UI dialog (consumer).
//
// It lives in internal/team (not internal/ui/dialog) so the bridge — a domain
// component — can construct it without depending on the UI layer. The dialog
// package imports team to consume it (UI → domain, correct direction).
package team

// TeamPermissionContext provides team metadata for permission dialogs.
// It is produced by PermissionBridge (stored under the request ID) and read by
// the TUI when building a team permission dialog.
type TeamPermissionContext struct {
	TeamName   string
	MemberName string
	MemberRole string
	TaskTitle  string
	ToolName   string // informational only — the dialog renders p.permission.ToolName
	Action     string // informational only
	Resource   string // informational only
}
