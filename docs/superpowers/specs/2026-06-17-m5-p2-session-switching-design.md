# M5-P2: Session Switching — Design Spec

> Date: 2026-06-17 | Status: draft | Scope: team bar navigation + session switching

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

  ↓ ↑   : 输入框 ↔ team bar（双向切换）
          - 输入框空时 ↓ → 进入 team bar，leader 高亮
          - team bar 中 ↑ → 回到输入框
          - team bar 中 ↓ → 也回到输入框
          - ↑ ↓ 对称，同一个行为

  ← →   : 在 team bar 里切换选中成员
          - focus 留在 bar 内
          - 选中即切换 — 聊天区立即显示该 member 的会话
```

**关键行为**：
- 选中即切换 — 在 team bar 里选中某个 member 时，上方的聊天区域立即显示该 member 的会话
- 高亮 = 当前正在看谁 — 始终有一个 member 高亮（默认 leader）
- 焦点只有两层：输入框／team bar，`↑` `↓` 对称切换

## Data Changes

### Leader member in team bar

`team_create` 已经自动创建 leader member（M4.5b fix）。Team bar 需要显示它：
- `🤖 demo │ leader(idle) ● coder(programmer) ◇ reviewer(idle) │ 3M 1A`

### Session association

每个 member 有一个 session ID（M4.5b auto-create）。切换到 member 时，需要：
1. 加载该 member 的 session 历史消息
2. 如果 member 的 session 还没创建过（首次选中），显示空会话

## Component Changes

### TeamBar → InteractiveTeamBar

- 维护 `selectedIndex int` 当前选中位置
- 维护 `focused bool` 是否处于导航模式
- `View(width) string` → 渲染时高亮 selectedIndex
- `Update(msg) tea.Cmd` → 处理 `←` `→` `↓` `Enter`

### UI 焦点系统

在 `UI` 层加一个 focus 枚举，三态：

```go
type uiFocus int
const (
    focusEditor   uiFocus = iota // 输入框（默认）
    focusTeamBar                  // team bar 导航
)
```

- `↓` 空输入 → `focusTeamBar`
- `focusTeamBar` + `↓` → `focusEditor`

### Session 切换

TeamBar 选中变化时，发送一个 `SessionSwitchMsg{SessionID: ...}`。UI 收到后：
1. 保存当前 session 状态
2. 加载目标 session 的历史消息
3. 更新 chat view

涉及 `session.Service` + `message.Service` 的查询。

## Files

| File | Action | Purpose |
|---|---|---|
| `internal/ui/model/team_bar.go` | REWRITE | 交互式导航组件 |
| `internal/ui/model/ui.go` | MODIFY | 焦点系统 + 会话切换 |
| `internal/ui/model/chat.go` | MODIFY | 支持切换 session 加载 |

## Acceptance

1. `↓` 空输入 → team bar leader 高亮
2. `←` `→` 切换选中 member，聊天区立即切换会话
3. `↓` 回到输入框
4. Leader 始终在 team bar 中显示为第一个 member
5. 新 member 无历史消息时不报错
6. `go build ./...` passes; existing tests pass

## Out of scope

- 在 member 会话中直接发消息（走 leader chat → team_send_message 即可）
- Member 的实时消息流（P3）
- 多 workspace/多 team 支持
