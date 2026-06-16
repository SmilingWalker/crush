// app_team.go exposes the M3-05 team.Service constructed in app.New so the
// server (internal/server) can build a workspace.AppWorkspace and inject it via
// SetTeamService (M3-07 Seam 1). The wiring is server-side (not in app) because
// package workspace already imports app — app cannot import workspace without a
// cycle. The server constructs the AppWorkspace per-resolved-workspace and
// injects app.TeamService(); M3-08 wires the real config-driven feature gate.
package app

import (
	"github.com/charmbracelet/crush/internal/agent"
	"github.com/charmbracelet/crush/internal/team"
)

// TeamService returns the team.Service constructed in New, or nil if none was
// built. The server uses this to inject into a workspace.AppWorkspace via
// SetTeamService. M3-08 wired the config-driven gate via IsAgentTeamEnabled.
func (a *App) TeamService() team.Service {
	return a.team
}

// TeamAgentFactory returns the M4-14 production AgentFactory backed by the M1
// coordinator. Returns nil if the app was created via NewForTest (no coordinator).
func (a *App) TeamAgentFactory() agent.AgentFactory {
	return a.teamFactory
}

// TeamRunner returns the M4-02 TeamRunner — the runtime orchestrator that manages
// long-lived team members. Returns nil if the app was created via NewForTest.
func (a *App) TeamRunner() team.TeamRunner {
	return a.teamRunner
}

// TeamScheduler returns the M4-03 Scheduler — task claiming, heartbeat, and
// stale-run recovery. Returns nil if the app was created via NewForTest.
func (a *App) TeamScheduler() *team.Scheduler {
	return a.teamScheduler
}
