# 01 Notion：Crush 当前架构深挖

Source: https://www.notion.so/36e3d57b1417801f84f0da8c12c80bdc

## Current Single-Agent Flow

```text
TUI / HTTP / CLI
  -> workspace.AgentRun or backend.SendMessage
  -> AgentCoordinator.Run
  -> currentAgent.Run(SessionAgentCall)
  -> fantasy.NewAgent(...).Stream(...)
  -> PrepareStep creates assistant message, injects tools, merges queued prompts
  -> streaming callbacks update assistant message
  -> fantasy executes tools
  -> OnToolResult creates tool message
  -> OnStepFinish updates session usage / finish reason
  -> auto summarize or process queued prompt
```

Key files:

| Module | File | Observation |
| --- | --- | --- |
| App initialization | `internal/app/app.go` | Creates session/message/permission/filetracker/coordinator and fans in events. |
| Backend API | `internal/backend/agent.go` | `SendMessage` calls `ws.AgentCoordinator.Run`. |
| Workspace | `internal/workspace/workspace.go` | TUI depends on the workspace interface, not app/backend directly. |
| Coordinator | `internal/agent/coordinator.go` | Only `currentAgent` is truly scheduled today. |
| Agent loop | `internal/agent/agent.go` | `SessionAgent.Run` is a full turn facade. |
| Session | `internal/session/session.go` | Session has parent relation, todos, usage, cost. |
| Message | `internal/message/message.go` | Message update debounce; terminal updates use must-deliver. |
| Permission | `internal/permission/permission.go` | Session-scoped permission with in-process pending channel. |
| TUI | `internal/ui/model/ui.go` | Handles message/session/permission/notify events. |

## `SessionAgent` Supports Session Coexistence, Not Teammates

`SessionAgent` has:

```go
messageQueue   *csync.Map[string, []SessionAgentCall]
activeRequests *csync.Map[string, context.CancelFunc]
```

The key is `sessionID`. This means one `SessionAgent` can hold active requests
for multiple sessions and queue prompts for a busy session. It does not provide:

- `team_id/mate_id/role/name`.
- Long-lived mate lifecycle.
- Agent-to-agent mailbox.
- Team-scoped cancel semantics.
- Durable run/task ownership.
- Team-aware permission, audit, or UI.

Important warning: `SessionAgent.Run` can return `nil, nil` when a prompt is
queued. A TeamRunner must not treat that as "task completed".

## `Coordinator.agents` Is A Foothold, Not Team Mode

`Coordinator` has both:

```go
currentAgent SessionAgent
agents       map[string]SessionAgent
```

but critical APIs still point at `currentAgent`:

- `Run`
- `Cancel`
- `CancelAll`
- `ClearQueue`
- `IsBusy`
- `IsSessionBusy`
- `Model`
- `UpdateModels`
- `QueuedPrompts`
- `Summarize`

Directly inserting TeamRunner into this map would confuse semantics:

| API | Single-agent meaning | Team problem |
| --- | --- | --- |
| `IsBusy()` | Is current agent active? | Leader busy, any mate busy, or current session busy? |
| `CancelAll()` | Cancel current agent sessions. | Cancel leader, all mates, or one team task? |
| `Model()` | Foreground model. | A team has multiple profiles/models. |
| `UpdateModels()` | Update current runtime. | Should background mates be hot-updated? |
| `QueuedPrompts(sessionID)` | Session prompt queue. | Team mailbox/task queue is not prompt queue. |

Recommendation: keep `Coordinator` as single-agent turn facade; extract an
`AgentFactory` / `TurnRunner` capability for `internal/team`.

## Existing `agent` Tool Is A Short Task

`agent` tool behavior:

1. Generate child session ID with `CreateAgentToolSessionID(messageID, toolCallID)`.
2. Create task session under parent session.
3. Run child `SessionAgent.Run`.
4. Add child cost to parent session.
5. Return final text as tool result to the leader model.

This is more like synchronous RPC than teammate runtime. A real mate needs:

- Persistent identity and state.
- Multiple turns after spawn.
- Idle loop waiting for mailbox/task wakeup.
- Ability to proactively report.
- `cancel current turn` and graceful shutdown.
- Team-aware permission, audit, and write coordination.

## Child Session IDs Are Not Team Identity

Current child session format:

```go
fmt.Sprintf("%s$$%s", messageID, toolCallID)
```

This is good for a one-shot `agent` tool but not for teammates:

- No `team_id/mate_id`.
- Cannot naturally reuse the same mate for multiple turns.
- Retry/reschedule/handoff are unclear.
- UI finds a parent tool call, not a roster member.

Recommended direction: store real UUID session IDs and map them in team tables:

```text
team_id
mate_id
leader_session_id
mate_session_id
root_message_id
current_run_id
```

## Session Todos Are Not Shared Tasks

`sessions.todos` is single-session JSON. Team task needs:

- task id
- owner/assignee
- claim/release
- CAS version
- dependencies
- per-team list
- task audit
- concurrent update protection

Keep `todos` as private notes. Add `team_tasks` as shared work board.

## Message History Is Not Mailbox

Session messages lack:

- sender/recipient agent id
- recipient role
- unread/delivered/read
- message kind
- correlation id
- retry/idempotency key
- control payload

Each agent's reasoning still belongs in its own session messages. Cross-agent
coordination belongs in `team_mailbox_messages`.

## Permission Gaps

Current permission request has:

```go
SessionID
ToolCallID
ToolName
Description
Action
Params
Path
```

Team mode requires `TeamID`, `MateID`, `MateName`, `MateRole`,
`ParentSessionID`, and scoped grants. The current pending channel is in-process
and not restart-safe, which is acceptable for MVP only if audit and identity are
added early.

## TUI Baseline

The existing nested child-session UI can inspire a compact team item, but cannot
represent roster, blocked/running/idle state, queue depth, current tool, elapsed
time, cost, or team/mate cancel scope.
