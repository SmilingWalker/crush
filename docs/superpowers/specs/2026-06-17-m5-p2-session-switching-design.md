# M5-P2: Session Switching — Design Spec (v2)

> Date: 2026-06-17 | Status: approved | Scope: team bar navigation + session switching + leader session fix

## Goal

Navigate between team members from the team bar and switch the chat view to any member's session. Clean up the leader member confusion introduced by the FK workaround in `027f3f52`.

## Root Cause Analysis

`44add9a7` (original M4.5): `team_create` only created the team, NO leader member. Clean.
`027f3f52`: auto-created leader member as FK workaround for `from_member_id` in `leader_send_message`.

This poisoned:
- Data: leader member with its own session, separate from user's main session
- Team bar: shows "leader" as a member, confusing
- P2 switching: backward navigation to leader loads wrong/empty session

## Decisions

| # | Decision |
|---|---|
| 1 | Leader **is** a member in the bar, always first |
| 2 | Leader's sessionID = user's main session ID (written at `team_create` time) |
| 3 | Pass sessionID via `func() string` closure (same pattern as `NewTeamRunnerWithSession`) |
| 4 | Entering bar (↓) does NOT load any session — chat stays as-is |
| 5 | ← → moves highlight only, no session switch |
| 6 | Enter confirms, loads highlighted member's session (empty sid → skip) |
| 7 | ↑ ↓ exits bar, no session switch |

## Interaction Model

```
↓      → 进入 bar，leader 高亮，聊天区不变
← →    → 移动高亮，无切换
Enter   → 加载高亮 member 的 session（sid="" 跳过，保持当前）
↑ ↓    → 退出 bar，回编辑器
```

## Component Changes

### 1. team_create — pass main session ID

**File:** `internal/team/leader_tools.go`, `internal/app/app.go`

- `NewTeamCreateTool(svc Service, runner TeamRunner, sessionID func() string)` — new param
- `app.go` provides closure: `func() string { return currentSessionID }`
- When spawning leader: `SpawnMemberRequest.SessionID = &sid` (sid from closure)
- Leader member gets user's main session ID, immediately valid
- Remove `WakeMember` call (no longer needed — session already set)
- Remove `SpawnMemberRequest.SessionID` field addition (revert to original)

### 2. TeamBar — navigation only, no auto-load

**File:** `internal/ui/model/team_bar.go`

- `↓` entry via `handleHistoryDown`: set focus, highlight leader, **no session load**
- `←` `→`: move `selectedIndex`, highlight updates, no switch
- `Enter`: emit `SessionSwitchMsg{sid}` — only if `sid != ""`
- `↑` `↓`: emit `FocusEditorMsg` — unconditional escape
- Pre-cached key bindings: `keyLeft`, `keyRight`, `keyUpDown`, `keyEnter`
- `View()`: reverse-video highlight on `selectedIndex` when focused

### 3. UI wiring

**File:** `internal/ui/model/ui.go`, `internal/ui/model/history.go`, `internal/ui/model/session.go`

- `uiFocusTeamBar` constant
- `handleKeyPressMsg`: route keys to TeamBar when `focus == uiFocusTeamBar`
- `handleHistoryDown`: history exhausted + `HasTeam()` → enter bar, **no loadSession**
- `SessionSwitchMsg` handler: `if sid != ""` → `loadSession(sid)`
- `FocusEditorMsg` handler: focus back to editor
- `loadSession`: original version, no empty-sid hack

## Files

| File | Action | Purpose |
|---|---|---|
| `internal/team/leader_tools.go` | MODIFY | `sessionID func() string` param, write to leader |
| `internal/team/service.go` | REVERT | Remove SpawnMemberRequest.SessionID field |
| `internal/app/app.go` | MODIFY | Pass session ID closure to NewTeamCreateTool |
| `internal/team/leader_tools_test.go` | MODIFY | Update test signatures |
| `internal/ui/model/team_bar.go` | REWRITE | Navigation only, Enter to switch |
| `internal/ui/model/ui.go` | MODIFY | Focus routing, SessionSwitchMsg handler |
| `internal/ui/model/history.go` | MODIFY | Enter bar on ↓, no load |
| `internal/ui/model/session.go` | MODIFY | Remove empty-sid hack |

## Acceptance

1. `team_create` writes user's main session ID as leader's SessionID
2. Team bar shows leader + workers, leader always first
3. `↓` enters bar, leader highlighted, chat unchanged
4. `←` `→` moves highlight, no switch
5. `Enter` on worker switches chat to worker's session
6. `Enter` on worker with no session (never woken) does nothing
7. `↑` `↓` exits bar back to editor
8. No team → `↓` normal history/editor behavior
9. `go build ./...` passes; existing tests pass

## Out of scope

- FK constraint fix for `from_member_id` (leader send uses leader member ID, which is now valid)
- Direct messaging to members from editor (use leader tools)
- Multi-workspace / multi-team
