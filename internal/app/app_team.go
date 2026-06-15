// app_team.go exposes the M3-05 team.Service constructed in app.New so the
// server (internal/server) can build a workspace.AppWorkspace and inject it via
// SetTeamService (M3-07 Seam 1). The wiring is server-side (not in app) because
// package workspace already imports app — app cannot import workspace without a
// cycle. The server constructs the AppWorkspace per-resolved-workspace and
// injects app.TeamService(); M3-08 wires the real config-driven feature gate.
package app

import "github.com/charmbracelet/crush/internal/team"

// TeamService returns the team.Service constructed in New, or nil if none was
// built. The server uses this to inject into a workspace.AppWorkspace via
// SetTeamService. M3-08 will re-construct with the config-driven gate; for now
// the gate defaults to disabled (ErrFeatureDisabled) until M3-08 wires it.
func (a *App) TeamService() team.Service {
	return a.team
}
