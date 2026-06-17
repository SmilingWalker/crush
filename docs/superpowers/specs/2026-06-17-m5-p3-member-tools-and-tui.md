# M5-P3: Member Tools & TUI — Design Spec

> Date: 2026-06-17 | Status: draft | Scope: member toolset fix + TUI activity display

## Root Cause Analysis

### Problem 1: Members only run one turn, then idle forever

**DB evidence:** All `team_runs` show `status=completed`. Members run exactly one turn (1 user msg + 1 assistant response = 2 messages per session). They correctly return to `idle` but never get woken again.

**Root cause: Members have only 2 tools.** In `MemberRunner.Start()`:

```go
tools := []fantasy.AgentTool{
    NewTeamReportStatusTool(t, member.ID, teamID),  // report status
    NewTeamSendMessageTool(t, member.ID, teamID),    // send message to peers
}
```

`AgentSpec.ToolPolicy` is zero-value → `AllowedTools = nil` → `buildTools` filters out ALL standard tools (bash, read, write, edit, grep, glob, etc.). The member agent literally cannot do actual work.

The `SessionAgent.Run` multi-turn loop DOES work — it keeps looping as long as the model calls tools (`FinishReasonToolCalls`). But with only 2 communication tools, the model has nothing productive to call, so it outputs text and stops (`FinishReasonStop`).

**Fix:** Give members a proper toolset. The member's `AgentSpec` must include a `ToolPolicy` with `AllowedTools` that includes the standard coder tools, subject to a permission mode (default: ask user).

### Problem 2: TUI doesn't show member activity well

**Current state:**
- Team bar shows `icon name(role)` only — no current task, no current tool
- All states render the same color (no green for running, red for failed)
- `MemberRuntimeState` has `CurrentTask`, `CurrentTool`, `ActivityDesc` — unused by bar
- `TeamCompactItem` component exists but unwired
- No sidebar team/member content

**Fix:** Expand team bar with state-dependent color and optional expanded view. Wire `TeamCompactItem` into chat for inline member status cards.

## Design

### 1. Member Tool Policy

When `team_spawn_member` is called, the member's `AgentSpec` should include a tool policy that grants access to standard coder tools.

**New AgentSpec in team_runner.go SpawnMember:**

```go
spec := agent.AgentSpec{
    ToolPolicy: agent.ToolPolicyProfile{
        AllowedTools: nil,  // nil = all built-in tools (same as coder)
        PermissionMode: params.PermissionMode,  // "default" / "auto" / "bypass"
    },
    MaxTurns: 50,  // prevent infinite loops
}
```

The `team_spawn_member` tool already has a `permission_mode` param (defaults to `"default"`). When `"auto"` or `"bypass"`, permissions are auto-granted. When `"default"`, the member asks the user for each dangerous operation (same as coder agent with yolo off).

**Tools granted (nil = all built-in):**
- `bash` — shell command execution
- `read` — read files
- `write` — write files
- `edit` — edit files
- `glob` — file pattern matching
- `grep` — content search
- `web_search` — web search
- `web_fetch` — fetch URLs
- + team tools: `team_report_status`, `team_send_message`

### 2. TUI: Enhanced Team Bar

**State colors:**

| State | Icon | Color |
|---|---|---|
| `idle` | ◇ | dim/gray |
| `running` | ● | green (`#00ff00`) |
| `waiting_permission` | ⏳ | yellow (`#ffff00`) |
| `blocked` | ⏳ | yellow |
| `failed` | ✗ | red (`#ff0000`) |
| `stopped` / `shutting_down` | ■ | dim |

**Enhanced bar format (when running members exist):**

```
🤖 teamName │ ● coder(running) [bash: go build] ◇ reviewer(idle) │ 3M 2A
                                    ^^^^^^^^^^
                                   current tool
```

When a member is idle, only show `name(role)`. When running/active, also show `[tool_name: brief_desc]` using `CurrentTool` from `MemberRuntimeState`.

### 3. TUI: Inline Member Status Card (TeamCompactItem wiring)

Wire `TeamCompactItem` into the chat message stream as an inline rendered block. When the user or leader agent mentions team status, the compact view renders inline. This is a read-only render pass — no interactivity.

Requires a `TeamStatusProvider` implementation on the workspace side that delegates to `TeamRunner.Status()`.

### 4. Member Run Loop (out of scope for P3)

The member correctly returns to idle after a run completes. Wake signals come from:
- Leader sends message (mailbox → wake)
- Task dependency resolution
- Explicit leader wake

A continuous task loop (member auto-claims next task) is out of scope for P3 — it requires task assignment logic in the Scheduler.

## Files

| File | Action | Purpose |
|---|---|---|
| `internal/team/team_runner.go` | MODIFY | Set AgentSpec.ToolPolicy in SpawnMember |
| `internal/team/leader_tools.go` | MODIFY | Pass tool_policy params to SpawnMember |
| `internal/ui/model/team_bar.go` | MODIFY | State colors, current tool display |
| `internal/ui/team/compact.go` | MODIFY | Wire TeamStatusProvider |
| `internal/ui/model/ui.go` | MODIFY | Render TeamCompactItem in chat |

## Acceptance

1. Spawned member has full toolset (bash, read, write, edit, etc.) via ToolPolicy
2. Member performs multi-turn work (calls tools, gets results, continues, finishes)
3. Team bar shows state-specific colors (green=running, red=failed, etc.)
4. Team bar shows current tool when member is active (`[tool: desc]`)
5. `go build ./...` passes; existing tests pass

## Out of scope

- Continuous task loop (auto-claim next task)
- Permission approval UI for member tools (uses existing permission system)
- Sidebar team integration
- Full TeamCompactItem with interactive navigation
