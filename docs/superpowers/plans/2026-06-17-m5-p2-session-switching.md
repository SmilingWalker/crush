# M5-P2 Session Switching — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Navigate between team members from the team bar and switch the chat view to any member's session.

**Architecture:** Add `SessionID` to status pipeline. Fix `team_create` to write leader's sessionID at creation time (leader = user's main session, always valid). Rewrite TeamBar as interactive component with pre-cached key bindings, `HasTeam()` guard, and unconditional ↑↓ escape. Wire `handleHistoryDown` for bar entry, route keys to TeamBar when focused, and handle `SessionSwitchMsg` with empty-ID guard (never load a nil session).

**Tech Stack:** Go, bubbletea V2, `team.TeamRunner.Status()`, `workspace.Workspace.GetSession()`/`ListMessages()`

---

## Interaction Model (from spec)

```
↓ (empty input, history exhausted, has team) → enter bar, leader highlighted
← → (in bar) → navigate members, session switches (only if sessionID valid)
↑ ↓ (in bar) → back to editor, chat stays on last-viewed session
```

### Core constraints

- **Leader = user's main session** — leader sessionID always valid, set at team_create time
- **Member sessionID may be empty** (never woken) — navigation to empty sessionID does nothing
- **Never set `m.session` to empty/nil session** — would make bar disappear, focus stuck

---

## File Map

| File | Action | Purpose |
|---|---|---|
| `internal/team/team_runner.go` | MODIFY | Add SessionID to MemberRuntimeState |
| `internal/team/member_runner.go` | MODIFY | Expose SessionID in Status() |
| `internal/team/leader_tools.go` | MODIFY | Write leader sessionID when creating leader member |
| `internal/ui/model/team_bar.go` | REWRITE | Interactive team bar (pre-cached bindings, HasTeam, unconditional escape) |
| `internal/ui/model/ui.go` | MODIFY | Focus system + session switch handling |
| `internal/ui/model/history.go` | MODIFY | Enter bar on history exhaustion |
| `internal/ui/model/session.go` | MODIFY | Revert empty-sessionID hack (NOT needed) |

---

### Task 1: Add SessionID to MemberRuntimeState

Same as before. Two-line change:

- `team_runner.go`: Add `SessionID string` field to `MemberRuntimeState`
- `member_runner.go`: Expose `m.sessionID` in `Status()` return

---

### Task 1b: Write leader sessionID in team_create

**File:** `internal/team/leader_tools.go`

In `NewTeamCreateTool`, when creating the leader member, the tool has access to the current session ID from the agent context. Need to pass it through so the leader member's session is set at creation time.

Find the `team_create` handler. Currently it creates a leader member and calls `SpawnMember`. Before `SpawnMember`, the leader member's `SessionID` must be set to the current agent session ID.

The tool handler receives an `agentCtx` or similar context that includes the current session ID. Check how other tools access the current session ID — it's typically available via the tool's input or the agent state.

---

### Task 2: Rewrite TeamBar (final version)

**File:** `internal/ui/model/team_bar.go`

Full rewrite incorporating all bug fixes:

Key points:
- **Pre-cached key bindings**: `keyLeft`, `keyRight`, `keyUpDown` created once in `NewTeamBar()`
- **`HasTeam()` method**: returns `b.status != nil && len(b.status.memberNames) > 0`
- **↑↓ before status guard**: always returns `FocusEditorMsg{}`, even when status is nil
- **`←` `→` navigation**: `switchSessionCmd()` emits `SessionSwitchMsg` with captured sessionID
- **`View()`**: highlights `selectedIndex` when focused (reverse video), dims when unfocused
- **`refresh()`**: captures `m.SessionID` in `TeamBarStatus.sessionIDs`, preserves `selectedIndex`
- **`SelectFirst()`**: sets `selectedIndex = 0`
- **`SelectedSessionID()`**: returns sessionID at current index, "" if out of bounds

---

### Task 3: Wire into UI

**Files:** `internal/ui/model/ui.go`, `internal/ui/model/history.go`, `internal/ui/model/session.go`

#### 3a: ui.go — focus constant

Add `uiFocusTeamBar` to `uiFocusState` iota (after `uiFocusMain`).

#### 3b: ui.go — key routing

In `handleKeyPressMsg`, after teamPanel delegation, before global keys:
```go
if m.focus == uiFocusTeamBar && m.teamBar != nil {
    if cmd := m.teamBar.Update(msg, m.com); cmd != nil {
        cmds = append(cmds, cmd)
    }
    return tea.Batch(cmds...)
}
```

#### 3c: ui.go — SessionSwitchMsg handler

```go
case SessionSwitchMsg:
    // Only switch if target has a session. Empty = member never woken.
    // Never set m.session to empty — would kill the bar and freeze focus.
    if msg.SessionID != "" {
        cmds = append(cmds, m.loadSession(msg.SessionID))
    }
```

#### 3d: ui.go — FocusEditorMsg handler

```go
case FocusEditorMsg:
    m.focus = uiFocusEditor
    if m.teamBar != nil {
        m.teamBar.SetFocused(false)
    }
    cmds = append(cmds, m.textarea.Focus())
```

#### 3e: history.go — enter bar on ↓

In `handleHistoryDown`, after `historyNext()` returns false:
```go
if m.teamBar != nil && m.com.Config().Options.IsAgentTeamEnabled() && m.teamBar.HasTeam() {
    m.focus = uiFocusTeamBar
    m.teamBar.SetFocused(true)
    m.teamBar.SelectFirst()
    m.textarea.Blur()
    return tea.Batch(
        m.updateTextareaWithPrevHeight(nil, prevHeight),
        m.loadSession(m.teamBar.SelectedSessionID()), // leader always has session
    )
}
```

#### 3f: session.go — revert empty-sessionID hack

Remove the `if sessionID == ""` special case added in `loadSession`. The caller is responsible for not passing empty IDs.

---

### Task 4: End-to-end verification

- `go build ./...` — no errors
- `go test -count=1 ./internal/ui/model/...` — PASS
- `go test -count=1 ./internal/team/...` — PASS (excluding known flake)
- Visual checklist:
  1. Create team → leader gets sessionID from current session
  2. ↓ enters bar → leader highlighted, chat shows leader session
  3. Spawn member, wake it → ← → switches between leader & member sessions
  4. Spawn member, don't wake → ← → switches to it, chat stays unchanged (no crash)
  5. ↑ ↓ exits bar → focus back to editor
  6. No team → ↓ scrolls history normally, no bar entry
