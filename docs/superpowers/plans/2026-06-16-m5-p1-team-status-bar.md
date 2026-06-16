# M5-P1 Team Status Bar — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Show team/member runtime status as a 1-line bar at the bottom of the main chat UI, polling every 2s.

**Architecture:** New `teamBar` component renders `TeamRuntimeStatus` via `uv.StyledString` in a 1-line layout region between editor and status bar. Polls via `tea.Tick` in `Update()`. Gated behind `IsAgentTeamEnabled()`.

**Tech Stack:** Go, `charm.land/uv` (styled string canvas), `tea.Tick`, `team.TeamRunner.Status()`

---

## File Map

| File | Action | Purpose |
|---|---|---|
| `internal/ui/model/team_bar.go` | NEW | TeamBar component: update + render |
| `internal/ui/model/team_bar_test.go` | NEW | Unit tests for rendering states |
| `internal/ui/model/ui.go` | MODIFY | Add teamBar field, layout region, Draw call, Update tick |
| `internal/ui/model/layout.go` | MODIFY | Add team bar region to generateLayout |

---

### Task 1: Create TeamBar component

**Files:**
- Create: `internal/ui/model/team_bar.go`

- [ ] **Step 1: Write team_bar.go**

```go
package model

import (
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/crush/internal/team"
	"github.com/charmbracelet/crush/internal/ui/common"
)

// teamBarTickMsg requests a refresh of the team status bar.
type teamBarTickMsg struct{}

// TeamBar shows team/member runtime status as a 1-line bar.
// Polls TeamRunner.Status() every 2s via tea.Tick.
type TeamBar struct {
	com    *common.Common
	status *team.TeamRuntimeStatus // nil = no team or not yet loaded
	width  int
	active bool // true when polling
}

// NewTeamBar creates a TeamBar. Returns nil if the feature gate is off.
func NewTeamBar(com *common.Common) *TeamBar {
	return &TeamBar{com: com}
}

// Init starts the 2s poll tick.
func (tb *TeamBar) Init() tea.Cmd {
	return tb.tick()
}

func (tb *TeamBar) tick() tea.Cmd {
	return tea.Tick(2*time.Second, func(t time.Time) tea.Msg {
		return teamBarTickMsg{}
	})
}

// Update handles tick messages to refresh status.
func (tb *TeamBar) Update(msg tea.Msg) tea.Cmd {
	switch msg.(type) {
	case teamBarTickMsg:
		tb.refresh()
		return tb.tick()
	}
	return nil
}

// refresh fetches current team status from the backend.
func (tb *TeamBar) refresh() {
	tw, ok := tb.com.Workspace.(TeamWorkspaceAccessor)
	if !ok {
		return
	}
	status, err := tw.TeamStatus()
	if err != nil {
		tb.status = nil
		return
	}
	tb.status = &status
}

// TeamWorkspaceAccessor is the interface to get team status.
type TeamWorkspaceAccessor interface {
	TeamStatus() (team.TeamRuntimeStatus, error)
}

// View renders the 1-line status bar.
func (tb *TeamBar) View(width int) string {
	tb.width = width

	if tb.status == nil || len(tb.status.Members) == 0 {
		return dim("🤖 No active team")
	}

	s := tb.status
	var parts []string

	// Team name
	parts = append(parts, bold("🤖 "+s.TeamID))

	// Members inline
	for id, ms := range s.Members {
		icon := statusIcon(string(ms.State))
		parts = append(parts, fmt.Sprintf("%s %s(%s)", icon, id, ms.Role))
	}

	// Totals
	totals := fmt.Sprintf("%dM %dA", len(s.Members), s.ActiveRuns)
	parts = append(parts, totals)

	// Join with separators
	left := strings.Join(parts[:1+len(s.Members)], " ") // team + members
	return dim(left + " │ ") + bold(totals)
}

func statusIcon(state string) string {
	switch state {
	case "running", "in_progress":
		return "●"
	case "idle":
		return "◇"
	case "blocked", "waiting_permission":
		return "⏳"
	case "stopped", "shutting_down", "canceling_turn":
		return "■"
	case "failed":
		return "✗"
	default:
		return "○"
	}
}

func dim(s string) string  { return s } // placeholder until styles wired
func bold(s string) string { return s } // placeholder until styles wired
```

Note: `TeamWorkspaceAccessor` needs to be implemented by the workspace. We'll add it in Task 3.

- [ ] **Step 2: Verify build compiles (will fail until wired)**

```bash
go build ./internal/ui/model/...
```
Expected: may fail if `TeamWorkspaceAccessor` not yet on workspace type. That's OK — wiring is Task 3.

- [ ] **Step 3: Commit**

```bash
git add internal/ui/model/team_bar.go
git commit -m "feat(ui): add TeamBar component"
```

---

### Task 2: Add team bar region to layout

**Files:**
- Modify: `internal/ui/model/ui.go`

The `uiLayout` struct and `generateLayout` method need a 1-line team bar region between editor and status.

- [ ] **Step 1: Find uiLayout struct and add teamBar field**

Search for `type uiLayout struct` in the ui package. Find it, then add:
```go
teamBar image.Rectangle
```

- [ ] **Step 2: Add team bar region calculation in generateLayout**

In `generateLayout`, after calculating `editor` region and before `status` region, add:
```go
// Team bar: 1 line between editor and status
teamBarHeight := 1
if !m.com.Config().Options.IsAgentTeamEnabled() {
    teamBarHeight = 0
}
layout.teamBar = image.Rect(0, 0, w, 0) // placeholder, calculated below
```

Actually, calculate it properly: the team bar sits between editor bottom and status top:
```go
// Status bar
layout.status = image.Rect(0, area.Dy()-helpHeight-editorHeight-teamBarHeight, area.Dx(), area.Dy()-editorHeight-teamBarHeight)

// Team bar (1 line above status)
layout.teamBar = image.Rect(0, area.Dy()-editorHeight-teamBarHeight, area.Dx(), area.Dy()-editorHeight)
```

Note: this needs exact positioning based on the current layout logic. Read `generateLayout` first to understand the exact coordinate calculations.

- [ ] **Step 3: Draw team bar in Draw() method**

In `Draw()`, find `m.status.Draw(scr, layout.status)` at ~line 2302. Before it, add:
```go
// Draw team status bar
if m.teamBar != nil && layout.teamBar.Dy() > 0 {
    teamBarView := uv.NewStyledString(m.teamBar.View(layout.teamBar.Dx()))
    teamBarView.Draw(scr, layout.teamBar)
}
```

- [ ] **Step 4: Add teamBar Update call**

In `Update()`, find where other components' Update is called. Add:
```go
if m.teamBar != nil {
    cmds = append(cmds, m.teamBar.Update(msg))
}
```

- [ ] **Step 5: Add teamBar Init in UI init**

Find where the UI is initialized (NewUI or similar). After creating the UI struct:
```go
if m.com.Config().Options.IsAgentTeamEnabled() {
    m.teamBar = NewTeamBar(m.com)
    cmds = append(cmds, m.teamBar.Init())
}
```

- [ ] **Step 6: Add TeamBar field to UI struct**

In the UI struct definition, add:
```go
teamBar *TeamBar
```

- [ ] **Step 7: Verify build**

```bash
go build ./...
```
Expected: no errors.

- [ ] **Step 8: Commit**

```bash
git add internal/ui/model/ui.go
git commit -m "feat(ui): wire TeamBar into main UI layout"
```

---

### Task 3: Wire workspace to provide TeamStatus

**Files:**
- Modify: `internal/workspace/app_team.go` (or wherever TeamWorkspace is defined)

The TeamBar needs a `TeamWorkspaceAccessor` on the workspace. The existing `TeamWorkspace` interface already has team methods. Add:

- [ ] **Step 1: Add TeamStatus method to workspace**

Find the `TeamWorkspace` interface. Add:
```go
TeamStatus() (team.TeamRuntimeStatus, error)
```

- [ ] **Step 2: Implement TeamStatus on AppWorkspace**

Find the AppWorkspace struct. Add the method:
```go
func (aw *AppWorkspace) TeamStatus() (team.TeamRuntimeStatus, error) {
    runner := aw.App.TeamRunner()
    if runner == nil {
        return team.TeamRuntimeStatus{}, fmt.Errorf("team runner not available")
    }
    // Get the first team's status (or iterate)
    // For now, return status for the first active team
    teams, err := aw.App.TeamService().ListTeams(context.Background(), "default", team.TeamFilter{})
    if err != nil || len(teams) == 0 {
        return team.TeamRuntimeStatus{}, nil
    }
    return runner.Status(context.Background(), teams[0].ID)
}
```

- [ ] **Step 3: Wire into TeamBar refresh**

Update `TeamBar.refresh()` to use `TeamWorkspaceAccessor` from workspace.

- [ ] **Step 4: Verify build + test**

```bash
go build ./... && go test -count=1 ./internal/workspace/...
```
Expected: no errors, tests pass.

- [ ] **Step 5: Commit**

```bash
git add internal/workspace/
git commit -m "feat(workspace): add TeamStatus to AppWorkspace"
```

---

### Task 4: Verify end-to-end

- [ ] **Step 1: Full build**

```bash
go build ./...
```
Expected: no errors.

- [ ] **Step 2: Full test**

```bash
go test -count=1 -timeout 60s ./internal/ui/model/... ./internal/workspace/...
```
Expected: PASS.

- [ ] **Step 3: Visual verify**

Enable `agent_team: true` in config, restart crush, create a team via chat. Team bar should appear at bottom showing team name + no members. Spawn a member → bar updates to show member with idle state.

- [ ] **Step 4: Commit any fixes**
