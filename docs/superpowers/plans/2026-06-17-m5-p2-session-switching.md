# M5-P2 Session Switching — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Navigate between team members from the team bar and switch the chat view to any member's session. The team bar becomes an interactive navigation component.

**Architecture:** Add `SessionID` to the status pipeline (MemberRunner → TeamRunner → TeamBar). Rewrite TeamBar as an interactive component with navigation state + key handling. Add `uiFocusTeamBar` to UI focus system, intercept `↓` arrow key for focus toggle, route `←` `→` keys to TeamBar. When selection changes, emit `SessionSwitchMsg` to load target session's messages into chat view.

**Tech Stack:** Go, bubbletea V2 (`tea.KeyPressMsg`, `key.Matches`, `tea.Cmd`), `team.TeamRunner.Status()`, `workspace.Workspace.GetSession()`/`ListMessages()`

---

## Interaction Model (from spec)

```
↓ (empty input) → enter team bar, leader highlighted
↑ (in team bar) → back to input box
↓ (in team bar) → back to input box  (symmetric — ↑ ↓ do the same in bar)
← → (in team bar) → navigate members, focus stays in bar
```

- **选中即切换**: each ←/→ press immediately switches the chat area to that member's session
- **Highlight = current view**: one member always highlighted (default: leader)
- **Focus**: only two layers — editor / team bar

---

## File Map

| File | Action | Purpose |
|---|---|---|
| `internal/team/team_runner.go` | MODIFY | Add SessionID to MemberRuntimeState |
| `internal/team/member_runner.go` | MODIFY | Expose SessionID in Status() |
| `internal/ui/model/team_bar.go` | REWRITE | Interactive team bar with navigation + session switching |
| `internal/ui/model/ui.go` | MODIFY | Focus system + session switch message handling |

---

### Task 1: Add SessionID to MemberRuntimeState

**Files:**
- Modify: `internal/team/team_runner.go:52-60` (MemberRuntimeState struct)
- Modify: `internal/team/member_runner.go:147-155` (Status method)

The status pipeline currently omits `SessionID`. We need it exposed so the team bar knows which session to switch to when a member is selected.

- [ ] **Step 1: Add SessionID to MemberRuntimeState**

In `internal/team/team_runner.go`, find `MemberRuntimeState` (line 52) and add the field:

```go
type MemberRuntimeState struct {
	State        MemberStatus
	Role         string
	SessionID    string   // member's session for UI session switching (M5-P2)
	CurrentRunID string
	CurrentTask  string
	CurrentTool  string
	ActivityDesc string
	CostMicros   int64
}
```

- [ ] **Step 2: Expose SessionID in MemberRunner.Status()**

In `internal/team/member_runner.go`, find `func (m *MemberRunner) Status()` (line 147) and add `SessionID`:

```go
func (m *MemberRunner) Status() MemberRuntimeState {
	m.mu.Lock()
	defer m.mu.Unlock()
	return MemberRuntimeState{
		State:        m.State,
		Role:         m.Role,
		SessionID:    m.sessionID,
		CurrentRunID: m.currentRunID,
	}
}
```

`sessionID` is already a field on `MemberRunner` (line 70), set in `NewMemberRunner` from `dbMember.SessionID` (line 204-206) or via `createSession` on first wake (line 288). It may be empty if the member has never been woken — the UI handles empty session IDs gracefully.

- [ ] **Step 3: Verify build compiles**

```bash
go build ./internal/team/...
```

Expected: no errors.

- [ ] **Step 4: Commit**

```bash
git add internal/team/member_runner.go internal/team/team_runner.go
git commit -m "feat(team): expose SessionID in MemberRuntimeState for M5-P2"
```

---

### Task 2: Rewrite TeamBar as InteractiveTeamBar

**Files:**
- Rewrite: `internal/ui/model/team_bar.go`

The TeamBar becomes an interactive component that:
1. Maintains navigation state (`selectedIndex`, `focused`)
2. Handles `←` `→` keys to navigate members, returning `SessionSwitchMsg` command on each change
3. Handles `↑` `↓` keys by returning `FocusEditorMsg` to go back to the editor
4. Highlights the currently selected member in `View()`
5. Tracks per-member `sessionIDs` for session switching

- [ ] **Step 1: Rewrite team_bar.go**

Replace the entire file content:

```go
// team_bar.go implements the M5 team status bar.
//
// M5-P1: read-only 1-line status display polling every 2s
// M5-P2: interactive navigation — ← → select members, ↑ ↓ toggle focus
//
// Format:  🤖 teamName │ ● coder(programmer) ◇ reviewer(idle) │ 2M 1A
// No team: 🤖 No active team

package model

import (
	"context"
	"fmt"
	"strings"
	"time"

	"charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/crush/internal/proto"
	"github.com/charmbracelet/crush/internal/ui/common"
	teamui "github.com/charmbracelet/crush/internal/ui/team"
	"github.com/charmbracelet/crush/internal/workspace"
)

// TeamBarTickMsg is sent every 2s to trigger a status refresh.
type TeamBarTickMsg struct{}

// SessionSwitchMsg requests the UI to switch the chat view to a different
// session. Emitted by TeamBar when the user navigates to a different member.
type SessionSwitchMsg struct {
	SessionID string
}

// FocusEditorMsg requests the UI to move focus back to the editor.
// Emitted by TeamBar when the user presses ↑ or ↓ in the bar.
type FocusEditorMsg struct{}

// TeamBar is the interactive team status bar (M5-P1 + M5-P2).
// It polls TeamRunner.Status every 2s and supports ← → ↑ ↓ navigation.
type TeamBar struct {
	status  *TeamBarStatus // nil = no active team
	tickCmd tea.Cmd

	// M5-P2: navigation state
	selectedIndex int    // index into status.memberNames, 0 = leader/first
	focused       bool   // true when TeamBar is the active focus (receives keys)
	teamID        string // cached team ID for refresh
}

// TeamBarStatus is the cached display data for the team bar.
type TeamBarStatus struct {
	teamName     string
	memberNames  []string // sorted for stable display
	memberIcons  []string
	memberRoles  []string
	sessionIDs   []string // per-member session ID for session switching (M5-P2)
	activeRuns   int
	totalMembers int
}

// NewTeamBar creates a TeamBar that polls the given workspace for team status.
func NewTeamBar() *TeamBar {
	tb := &TeamBar{}
	tb.tickCmd = tb.tick()
	return tb
}

// Init starts the 2s polling tick.
func (b *TeamBar) Init() tea.Cmd {
	return b.tickCmd
}

// SetFocused sets the TeamBar focus state. When true, arrow keys navigate
// members; when false, arrow keys are ignored.
func (b *TeamBar) SetFocused(v bool) {
	b.focused = v
}

// IsFocused reports whether the TeamBar currently has focus.
func (b *TeamBar) IsFocused() bool {
	return b.focused
}

// SelectedSessionID returns the session ID of the currently selected member,
// or "" if no status or no members.
func (b *TeamBar) SelectedSessionID() string {
	if b.status == nil || len(b.status.sessionIDs) == 0 {
		return ""
	}
	if b.selectedIndex < 0 || b.selectedIndex >= len(b.status.sessionIDs) {
		return ""
	}
	return b.status.sessionIDs[b.selectedIndex]
}

// SelectFirst selects the first member (leader) if available.
func (b *TeamBar) SelectFirst() {
	if b.status != nil && len(b.status.memberNames) > 0 {
		b.selectedIndex = 0
	}
}

// Update handles tick messages and key navigation.
func (b *TeamBar) Update(msg tea.Msg, com *common.Common) tea.Cmd {
	switch msg := msg.(type) {
	case TeamBarTickMsg:
		if tr := com.Workspace.TeamRunner(); tr != nil {
			b.refresh(com)
		}
		return b.tick()

	case tea.KeyPressMsg:
		if !b.focused || b.status == nil || len(b.status.memberNames) == 0 {
			return nil
		}
		switch {
		case key.Matches(msg, key.NewBinding(key.WithKeys("left"))):
			if b.selectedIndex > 0 {
				b.selectedIndex--
				if b.selectedIndex >= len(b.status.memberNames) {
					b.selectedIndex = len(b.status.memberNames) - 1
				}
				return b.switchSessionCmd()
			}
		case key.Matches(msg, key.NewBinding(key.WithKeys("right"))):
			if b.selectedIndex < len(b.status.memberNames)-1 {
				b.selectedIndex++
				return b.switchSessionCmd()
			}
		case key.Matches(msg, key.NewBinding(key.WithKeys("up", "down"))):
			// ↑ and ↓ both return focus to editor (symmetric)
			return func() tea.Msg { return FocusEditorMsg{} }
		}
	}
	return nil
}

// switchSessionCmd returns a command that emits a SessionSwitchMsg for the
// currently selected member's session ID.
func (b *TeamBar) switchSessionCmd() tea.Cmd {
	sid := b.SelectedSessionID()
	return func() tea.Msg {
		return SessionSwitchMsg{SessionID: sid}
	}
}

// View renders the 1-line team status bar with the selected member highlighted.
func (b *TeamBar) View(width int) string {
	barStyle := lipgloss.NewStyle().
		Width(width).
		Padding(0, 1).
		Foreground(lipgloss.Color("241"))

	if b.status == nil {
		icon := lipgloss.NewStyle().Foreground(lipgloss.Color("69")).Render("🤖")
		label := lipgloss.NewStyle().Foreground(lipgloss.Color("241")).Render(" No active team")
		return barStyle.Render(icon + label)
	}

	// Clamp selectedIndex if member count changed during refresh
	if b.selectedIndex >= len(b.status.memberNames) {
		b.selectedIndex = len(b.status.memberNames) - 1
	}
	if b.selectedIndex < 0 {
		b.selectedIndex = 0
	}

	var parts []string
	parts = append(parts, lipgloss.NewStyle().Foreground(lipgloss.Color("69")).Render("🤖 "+b.status.teamName))

	// Build member status string with selection highlighting
	var memberStrs []string
	for i := range b.status.memberNames {
		memberStr := b.status.memberIcons[i] + " " + b.status.memberNames[i] + "(" + b.status.memberRoles[i] + ")"
		if b.focused && i == b.selectedIndex {
			// Highlight selected member with reverse video (blue bg, black fg)
			memberStr = lipgloss.NewStyle().
				Background(lipgloss.Color("69")).
				Foreground(lipgloss.Color("0")).
				Render(memberStr)
		}
		memberStrs = append(memberStrs, memberStr)
	}
	if len(memberStrs) > 0 {
		parts = append(parts, strings.Join(memberStrs, " "))
	}

	// Build stats: 2M 1A
	var stats []string
	if b.status.totalMembers > 0 {
		stats = append(stats, fmt.Sprintf("%dM", b.status.totalMembers))
	}
	if b.status.activeRuns > 0 {
		stats = append(stats, fmt.Sprintf("%dA", b.status.activeRuns))
	}
	var statStr string
	if len(stats) > 0 {
		statStr = "  " + strings.Join(stats, " ")
	}

	// Join with separator
	content := strings.Join(parts, " │ ") + statStr

	// Truncate to fit width (subtract padding)
	maxContent := width - 2
	if len(content) > maxContent && maxContent > 3 {
		content = content[:maxContent-1] + "…"
	}

	return barStyle.Render(content)
}

// refresh fetches the latest team status from the workspace and caches it.
func (b *TeamBar) refresh(com *common.Common) {
	teamID, teamName := b.findFirstTeam(com)
	if teamID == "" {
		b.status = nil
		return
	}
	b.teamID = teamID

	tr := com.Workspace.TeamRunner()
	if tr == nil {
		b.status = nil
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	runtimeStatus, err := tr.Status(ctx, teamID)
	if err != nil {
		b.status = nil
		return
	}

	// Sort member names for stable display.
	var names []string
	for name := range runtimeStatus.Members {
		names = append(names, name)
	}
	sortMemberNames(names)

	ts := &TeamBarStatus{
		teamName:     teamName,
		memberNames:  make([]string, 0, len(names)),
		memberIcons:  make([]string, 0, len(names)),
		memberRoles:  make([]string, 0, len(names)),
		sessionIDs:   make([]string, 0, len(names)),
		activeRuns:   runtimeStatus.ActiveRuns,
		totalMembers: len(runtimeStatus.Members),
	}

	for _, name := range names {
		m := runtimeStatus.Members[name]
		icon := teamui.StatusIcon(string(m.State))
		ts.memberNames = append(ts.memberNames, name)
		ts.memberIcons = append(ts.memberIcons, icon)
		ts.memberRoles = append(ts.memberRoles, m.Role)
		ts.sessionIDs = append(ts.sessionIDs, m.SessionID)
	}

	// Preserve selectedIndex on refresh if still in range.
	if b.selectedIndex >= len(names) {
		b.selectedIndex = 0
	}

	b.status = ts
}

// findFirstTeam returns the ID and name of the first non-archived team, or "" if none.
func (b *TeamBar) findFirstTeam(com *common.Common) (teamID, teamName string) {
	tw, ok := com.Workspace.(workspace.TeamWorkspace)
	if !ok {
		return "", ""
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	resp, err := tw.ListTeams(ctx, proto.ListTeamsRequest{
		WorkspaceID: "default",
	})
	if err != nil || len(resp.Teams) == 0 {
		return "", ""
	}

	t := resp.Teams[0]
	name := t.Name
	if name == "" {
		name = t.ID
	}
	return t.ID, name
}

// tick returns a command that fires a TeamBarTickMsg after 2s.
func (b *TeamBar) tick() tea.Cmd {
	return tea.Tick(2*time.Second, func(t time.Time) tea.Msg {
		return TeamBarTickMsg{}
	})
}

// sortMemberNames sorts member names alphabetically.
func sortMemberNames(names []string) {
	for i := 0; i < len(names); i++ {
		for j := i + 1; j < len(names); j++ {
			if names[i] > names[j] {
				names[i], names[j] = names[j], names[i]
			}
		}
	}
}
```

- [ ] **Step 2: Verify build compiles**

```bash
go build ./internal/ui/model/...
```

Expected: no errors.

- [ ] **Step 3: Commit**

```bash
git add internal/ui/model/team_bar.go
git commit -m "feat(ui): rewrite TeamBar as interactive navigation component for M5-P2"
```

---

### Task 3: Wire focus system + session switching into UI

**Files:**
- Modify: `internal/ui/model/ui.go`

Four changes needed:
1. Add `uiFocusTeamBar` to focus state enum
2. Route all key events to TeamBar when it has focus
3. Intercept `↓` arrow key from empty editor to enter team bar
4. Handle `SessionSwitchMsg` and `FocusEditorMsg` in Update

- [ ] **Step 1: Add uiFocusTeamBar constant**

In `internal/ui/model/ui.go`, find `uiFocusState` constants (line 100-104):

```go
const (
	uiFocusNone uiFocusState = iota
	uiFocusEditor
	uiFocusMain
	uiFocusTeamBar   // ADD: M5-P2 team bar navigation
)
```

- [ ] **Step 2: Route keys to TeamBar when focused**

In `handleKeyPressMsg` (line 1853), after the teamPanel delegation block:

```go
// Delegate key events to the team panel if it is active.
if m.teamPanel != nil {
	if consumed, cmd := m.handleTeamPanelMsg(msg); consumed {
		return cmd
	}
}
```

Add immediately after:

```go
// Delegate key events to the team bar if it has focus (M5-P2).
if m.focus == uiFocusTeamBar && m.teamBar != nil {
	if cmd := m.teamBar.Update(msg, m.com); cmd != nil {
		cmds = append(cmds, cmd)
	}
	return tea.Batch(cmds...)
}
```

This MUST go before `handleGlobalKeys` — when the team bar has focus, it owns all keyboard input.

- [ ] **Step 3: Intercept ↓ from empty editor to enter team bar**

In the `uiFocusEditor` section of `handleKeyPressMsg`, find the `case` for `m.keyMap.Editor.Commands && m.textarea.Value() == ""` (around line 2085). Add BEFORE the `default:` case:

```go
case m.teamBar != nil && m.com.Config().Options.IsAgentTeamEnabled() &&
	key.Matches(msg, key.NewBinding(key.WithKeys("down"))) &&
	m.textarea.Value() == "":
	// M5-P2: ↓ on empty input → enter team bar navigation
	m.focus = uiFocusTeamBar
	m.teamBar.SetFocused(true)
	m.teamBar.SelectFirst()
	// Switch to leader's session on entry
	if sid := m.teamBar.SelectedSessionID(); sid != "" {
		cmds = append(cmds, m.loadSession(sid))
	}
	m.textarea.Blur()
```

**Key binding note:** We use `key.NewBinding(key.WithKeys("down"))` — NOT `m.keyMap.Chat.Down` (which also matches `"j"` and `"ctrl+j"`). We only want the actual ↓ arrow key, not letter aliases.

- [ ] **Step 4: Handle SessionSwitchMsg and FocusEditorMsg in Update**

In `UI.Update()` (line 568), find the `switch msg := msg.(type)` block. Add cases for the new messages:

```go
case SessionSwitchMsg:
	// M5-P2: team bar navigation switched to a different member.
	// Load the target session's messages into the chat view.
	if msg.SessionID != "" {
		cmds = append(cmds, m.loadSession(msg.SessionID))
	}

case FocusEditorMsg:
	// M5-P2: ↑ or ↓ pressed in team bar → return focus to editor.
	m.focus = uiFocusEditor
	if m.teamBar != nil {
		m.teamBar.SetFocused(false)
	}
	cmds = append(cmds, m.textarea.Focus())
```

These should go in the main `switch msg := msg.(type)` block (the same level as `case loadSessionMsg:`, `case sendMessageMsg:`, etc.), before the `case tea.KeyPressMsg:` line.

- [ ] **Step 5: Set initial focus on team bar init**

In the `Update` function, find where the teamBar tick is dispatched (line ~944-946):

```go
if m.teamBar != nil {
	if cmd := m.teamBar.Update(msg, m.com); cmd != nil {
		cmds = append(cmds, cmd)
```

This already works for tick messages. No change needed — the TeamBar tick fires on init and every 2s, refreshing status. The `SessionSwitchMsg`/`FocusEditorMsg` are produced by key handling in Step 2 (when the bar has focus), not from tick.

- [ ] **Step 6: Verify build compiles**

```bash
go build ./...
```

Expected: no errors.

- [ ] **Step 7: Commit**

```bash
git add internal/ui/model/ui.go
git commit -m "feat(ui): wire focus system + session switching for M5-P2"
```

---

### Task 4: End-to-end verification

- [ ] **Step 1: Full build**

```bash
go build ./...
```

Expected: no errors.

- [ ] **Step 2: Run tests**

```bash
go test -count=1 -timeout 60s ./internal/team/... ./internal/ui/model/...
```

Expected: PASS.

- [ ] **Step 3: Visual verification checklist**

1. Enable `agent_team: true` in config, restart crush
2. Create a team + spawn members via chat
3. **Team bar shows** at bottom with `🤖 teamName │ members │ 2M 1A`
4. **↓ on empty input** → team bar focuses, leader highlighted (reverse video)
5. **← →** navigate members, highlight moves, chat view switches session immediately
6. **↑ or ↓ in bar** → focus returns to editor, highlight turns off
7. **New member with no session** → "No active team" or empty session (no crash)
8. **Switch back to leader** via ←/→ → chat shows leader's session

- [ ] **Step 4: Commit any fixes**

---

## Self-Review

### 1. Spec coverage
- `↓` empty input → team bar leader highlight: Task 3 Step 3
- `←` `→` switch selected member, chat switches session: Task 2 Step 1 (key handling) + Task 3 Step 4 (SessionSwitchMsg)
- `↓` / `↑` from bar → back to input box: Task 2 Step 1 (FocusEditorMsg) + Task 3 Step 4
- Leader always displayed as first member: Task 2 Step 1 (SelectFirst selects index 0, which is leader in sorted names)
- New member with no history doesn't error: `loadSession` handles missing session gracefully via existing error path

### 2. Placeholder scan
No TBDs, TODOs, or "implement later" tokens. All code is fully specified.

### 3. Type consistency
- `MemberRuntimeState.SessionID string` (Task 1) consumed by `TeamBarStatus.sessionIDs []string` (Task 2)
- `SessionSwitchMsg{SessionID: string}` (Task 2) consumed by `case SessionSwitchMsg:` (Task 3 Step 4)
- `FocusEditorMsg{}` (Task 2) consumed by `case FocusEditorMsg:` (Task 3 Step 4)
- `TeamBar.SetFocused(bool)`, `SelectFirst()`, `IsFocused()` all used in Task 3
