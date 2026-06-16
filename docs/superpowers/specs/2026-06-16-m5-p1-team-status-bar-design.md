# M5-P1: Team Status Bar — Design Spec

> Date: 2026-06-16 | Status: approved | Scope: 1 UI component + wiring

## Goal

Show team/member runtime status in a 1-line bar at the bottom of the main chat UI. Users can see at a glance what their agent team is doing without opening the debug panel (Ctrl+T).

## Background

M4 built a complete backend (TeamRunner, MemberRunner, Scheduler). M4.5 gave the coder agent tools to create teams and spawn members. But after spawning members, users have **zero visibility** — they can't see member states, active runs, or team status from the main UI.

## Design

### Visual layout

```
┌─────────────────────────────────────────────────────────┐
│  chat area                                               │
│                                                         │
├─────────────────────────────────────────────────────────┤
│ 🤖 demo │ ● coder(programmer) ◇ reviewer(idle) │ 2M 1A $0.05 │
└─────────────────────────────────────────────────────────┘
```

- No active team: `🤖 No active team` in dim/gray
- Has team but no members: `🤖 demo │ (no members) │ 0M`
- Members shown inline with status icon + name(role), separated by space
- Right side: totals — M members, A active runs, cost
- Status icons: `●` running, `◇` idle, `⏳` blocked, `■` stopped, `✗` failed

### Data source

`TeamRunner.Status(ctx, teamID)` → `TeamRuntimeStatus` (already exists):

```go
type TeamRuntimeStatus struct {
    TeamID     string
    Members    map[string]MemberRuntimeState
    ActiveRuns int
}
```

### Update mechanism

2-second poll via `tea.Tick` (same pattern as existing `team.Panel`). On each tick, call `TeamRunner.Status()` and update the view.

### Component

New file `internal/ui/model/team_bar.go`:
- `TeamBar` struct — holds `TeamRuntimeStatus`, team name, dimensions
- `Update(msg)` — handles TickMsg to refresh, KeyPressMsg for expand toggle
- `View()` — renders 1-line bar
- `SetStatus(status)` — called on each poll

### Wiring

- In `UI` struct, add `teamBar *TeamBar` field
- In `View()`, append team bar at bottom
- In `Update()`, delegate TickMsg to teamBar
- Team bar gets `TeamWorkspace` from `com.Workspace` (same as team panel)
- Feature-gated behind `IsAgentTeamEnabled()`

## Files

| File | Action | Purpose |
|---|---|---|
| `internal/ui/model/team_bar.go` | NEW | TeamBar component |
| `internal/ui/model/ui.go` | MODIFY | Add teamBar field, wire in View/Update |
| `internal/ui/model/keys.go` | MODIFY | Optional: key binding to expand |

## Acceptance

1. No team → shows `🤖 No active team` in dim style
2. Active team → shows team name, member list with status icons, totals
3. Polls every 2s, updates live
4. Feature-gated: hidden when `IsAgentTeamEnabled()` is false
5. `go build ./...` passes; existing tests unchanged

## Out of scope (P2/P3)

- Clicking a member to view session
- Sending messages from the bar
- Expanding to multi-line detail
