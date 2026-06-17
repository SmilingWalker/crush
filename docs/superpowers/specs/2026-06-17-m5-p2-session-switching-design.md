# M5-P2: Session Switching — Design Spec

> Date: 2026-06-17 | Status: updated | Scope: team bar navigation + session switching

## Goal

Navigate between team members from the team bar and switch the chat view to any member's session. The team bar becomes an interactive navigation component — not just a read-only status display.

## Interaction Model

```
┌─────────────────────────────────────────────────────────┐
│  chat area                                               │
│  (shows current selected member's session)               │
│                                                         │
├─────────────────────────────────────────────────────────┤
│ 🤖 leader │ ● coder(programmer) ◇ reviewer(idle) │ 2M 1A │
│   ^^高亮                                              │
└─────────────────────────────────────────────────────────┘

  ↓      : 输入框空 + 历史到底 + 有 team → 进入 team bar，leader 高亮
           （历史非空时 ↓ 走历史导航，历史到底后 ↓ 才进 bar）
  ↓ ↑    : team bar 中 → 回到编辑器（双向对称）
  ← →    : team bar 中 → 切换选中成员，焦点留在 bar 内
```

**关键行为**：
- 选中即切换 — 在 team bar 里选中某个 member 时，上方的聊天区域立即显示该 member 的会话
- 高亮 = 当前正在看谁 — 始终有一个 member 高亮（默认 leader）
- 焦点只有两层：编辑器／team bar，`↑` `↓` 对称切换

## Session 模型（核心约束）

### Leader 的 session = 用户主 session

Leader **就是用户自己**。用户正在聊天的当前 session 就是 leader 的 session。Leader 的 sessionID **始终有效**，不可能为空。

`team_create` 创建 leader member 时，必须把当前 request 的 session ID 写入 leader member 的 `session_id` 字段。这样 `TeamRunner.Status()` → `MemberRuntimeState.SessionID` → leader 始终有有效 sessionID。

### Member 的 session 可能为空

非 leader member（spawn 出来的 teammate）的 session 在**首次被 wake** 时才由 `createSession` 自动创建。如果 member 从未被 wake（没有收到过消息），其 sessionID 为空。

### 切换规则

| 操作 | 目标 sessionID | 行为 |
|---|---|---|
| 进入 bar（↓） | leader = 用户主 session | loadSession(leaderSID) — 始终有效 |
| 导航（← →）到有 session 的 member | 有效 sessionID | loadSession(sid)，替换聊天区 |
| 导航（← →）到无 session 的 member | 空 sessionID | **不做任何事**，保持当前聊天区不变 |
| 退出 bar（↑ ↓） | — | 聊天区保持在最后显示的 session，不切回 |

### 致命约束：`m.session` 永远不能是空/无效 session

如果把 `m.session` 设为一个空 session（ID=""），会导致：
1. `hasSession()` → false
2. `generateLayout()` 中 team bar 高度 = 0 → bar 消失
3. focus 仍为 `uiFocusTeamBar` → 所有按键路由到不存在的 bar → 完全卡死

**所以必须保证**：任何 session 切换操作，如果目标 sessionID 为空，**什么都不做**。

## 交互路径

### 路径 1：进入 Bar（↓）

```
用户按 ↓
  → 输入框焦点 (uiFocusEditor)
  → 输入框为空 (m.textarea.Value() == "")
  → 历史已到底 (historyNext 返回 false)
  → HasTeam() → true
  → 进入 Bar：
      m.focus = uiFocusTeamBar
      bar.focused = true
      bar.selectedIndex = 0
      textarea.Blur()
      loadSession(leaderSID)  // leader 始终有 session
```

HasTeam() 定义：`bar.status != nil && len(bar.status.memberNames) > 0`

### 路径 2：Bar 内导航（← →）

```
用户按 ← 或 →
  → handleKeyPressMsg → focus == uiFocusTeamBar
  → bar.Update() → selectedIndex ± 1
  → switchSessionCmd() → SessionSwitchMsg{sid}
  → UI.Update:
      if sid != "" → loadSession(sid)
      if sid == "" → 什么都不做，保持当前聊天
```

`↑` `↓` 始终返回编辑器（focus=uiFocusEditor, bar.focused=false, textarea.Focus()），不受 status 是否为 nil 影响。

### 路径 3：退出 Bar（↑ ↓）

```
用户按 ↑ 或 ↓
  → bar.Update() → FocusEditorMsg
  → UI.Update:
      m.focus = uiFocusEditor
      bar.focused = false
      textarea.Focus()
      // 聊天区保持最后选中 session，不回切
```

## Data Changes

### `MemberRuntimeState` 增加 SessionID

```
MemberRuntimeState {
    State        MemberStatus
    Role         string
    SessionID    string   // NEW: for UI session switching
    CurrentRunID string
    ...
}
```

Leader 的 SessionID = 用户主 session（由 `team_create` 写入）。
Member 的 SessionID = auto-created on first wake（由 `MemberRunner` 设置），可能为空。

### Leader member 创建时写入 sessionID

`team_create` 工具创建 leader member 时需要获取当前 request 的 session ID，写入 `dbMember.SessionID`。这样 leader 的 session 始终有效。

## Component Changes

### TeamBar

- 新增 `HasTeam()` — 判断是否有缓存的 team（status != nil && members > 0）
- 预缓存 key bindings：`keyLeft`, `keyRight`, `keyUpDown`
- `↑` `↓` 在 status guard **之前**检查（无条件 escape）
- `←` `→` 导航 → `switchSessionCmd()` → `SessionSwitchMsg`
- `View()` 高亮选中 member（反转色）

### UI 焦点系统

三态：
```
uiFocusNone → uiFocusEditor → uiFocusMain → uiFocusTeamBar (新增)
```

- `handleHistoryDown`：历史到底 + HasTeam() → 进 bar，始终 loadSession(leader SID)
- `handleKeyPressMsg`：focus == uiFocusTeamBar → 路由到 bar.Update()
- `SessionSwitchMsg` handler：**仅当 sid != "" 时**调用 loadSession
- `FocusEditorMsg` handler：恢复编辑器焦点

### `loadSession`

不改。空 sessionID 不应该走到 loadSession。调用方负责检查。

## Acceptance

1. `↓` 空输入 + 历史到底 + 有 team → team bar leader 高亮，聊天区显示 leader 会话
2. `←` `→` 切换选中 member → 有 session 就加载，无 session 保持当前
3. `↑` `↓` 回到输入框，bar 焦点消失
4. 无 team → `↓` 正常走历史/文本编辑，不卡死
5. 无 session 的 member → `←` `→` 选中时不崩溃、不卡死
6. `go build ./...` passes; existing tests pass

## Out of scope

- 在 member 会话中直接发消息（走 leader chat → team_send_message 即可）
- Member 的实时消息流（P3）
- 多 workspace/多 team 支持
