# M5: Permission Bridge — Design Spec

> Date: 2026-06-17 | Status: draft | Based on: `docs/agent-team-mode/plan/tasks/06-m5-permission-bridge.md`

## Goal

Members can call standard coder tools (bash, read, write, edit, etc.) with permission control. The leader can approve/deny member tool calls in real-time.

## Root Cause

**Members run one turn then stop.** Reason: `AgentSpec.ToolPolicy` is zero-value (`AllowedTools=nil`), causing `buildTools` to filter out ALL standard tools. Members only get `team_report_status` + `team_send_message`. Without productive tools, the model outputs text and stops.

**Fix:** Set `ToolPolicy` in `SpawnMember` so members get the full standard toolset. Then M5 PermissionBridge intercepts dangerous tool calls and prompts the user for approval.

## Design

### Phase 0: Tool Policy Fix (prerequisite)

In `TeamRunner.SpawnMember`, set `AgentSpec.ToolPolicy`:

```go
spec := agent.AgentSpec{
    ToolPolicy: agent.ToolPolicyProfile{
        AllowedTools:  nil,  // nil = all built-in tools
    },
    MaxTurns: 50,
}
```

This gives members the same standard tools as the coder agent: `bash`, `read`, `write`, `edit`, `grep`, `glob`, `web_search`, `web_fetch`, etc. + team tools `report_status`, `send_message`.

With tools available, the model can do multi-turn work: call tool → get result → think → call next tool → ... → finish.

### Phase 1-10: PermissionBridge (per original M5 plan)

See `docs/agent-team-mode/plan/tasks/06-m5-permission-bridge.md` for full task breakdown. Core architecture:

```
PermissionBridge wraps permission.Service:
  - Non-team session → delegate to inner (behavior unchanged)
  - Team session → check scoped grants → create permission request → wait UI decision
  - Grant scopes: call (once), task (same task), session (entire session)
  - Queue: max 3 pending, 5min TTL, auto-expire
  - Audit: 15 action types, immutable
```

DB tables: `team_permission_requests`, `team_permission_grants`  
New struct: `PermissionBridge`, `PermissionFSM`, `AuditService`  
UI: extend existing permission dialog with member/task/run context

## Files

| File | Action | Purpose |
|---|---|---|
| `internal/team/team_runner.go` | MODIFY | Set AgentSpec.ToolPolicy in SpawnMember |
| `internal/team/permission_bridge.go` | NEW | Team-aware permission wrapper |
| `internal/team/permission_store.go` | NEW | DB store for requests + grants |
| `internal/team/permission_fsm.go` | NEW | State machine: pending→allowed/denied/expired/canceled |
| `internal/team/permission_queue.go` | NEW | Queue + timeout |
| `internal/team/permission_audit.go` | NEW | Audit logging |
| `internal/ui/dialog/permission.go` | MODIFY | Show member/task/run context in permission dialog |

## Acceptance

1. Spawned member has full standard toolset
2. Member tool calls trigger permission requests (team session)
3. Non-team sessions unchanged
4. Leader can allow/deny per call or per task
5. Timeouts and queue limits enforced
6. Audit trail for all decisions
7. `go build ./...` + tests pass
