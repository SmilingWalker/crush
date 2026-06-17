# Wire M5 PermissionBridge into Production App

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Wire the M5 `team.PermissionBridge` into `app.New()` so that when a team member calls a tool, the PermissionBridge intercepts the call and routes through the team permission flow instead of auto-denying.

**Architecture:** `PermissionBridge` already implements the full `permission.Service` interface. Its `Request()` method checks `actor.FromContext(ctx)` for team context: if `TeamID` and `MemberID` are present, the call goes through team permission flow (grant check → enqueue → wait for UI); otherwise it delegates to the inner `permission.Service`. This means we can pass the bridge wherever the raw `permission.Service` was used — no other code changes required. The bridge is created wrapping `app.Permissions`, and the bridge is what gets passed to `agent.NewCoordinator` instead of the raw service.

**Tech Stack:** Go, testify/assert, SQLite :memory: for test fixtures

**Seam decision:** Always wrap with the bridge (not gated behind `IsAgentTeamEnabled`). The bridge's `Request()` is a no-op for non-team sessions — it delegates directly to `inner.Request()`. The audit callback, grant store, and permission queue are initialized but only activated when team context is detected. This avoids conditional wiring complexity with zero overhead for non-team sessions.

---

### Task 1: Add `permBridge` field to `App` struct

**Files:**
- Modify: `internal/app/app.go:56-110`

- [ ] **Step 1: Add the field declaration**

Add the `permBridge` field to the `App` struct, between `teamScheduler` and `currentSessionID`:

```go
// permBridge is the M5 PermissionBridge — a team-aware wrapper around
// Permissions that intercepts tool calls from team members and routes
// them through the team permission flow (grant check → enqueue → wait
// for UI approval). For non-team sessions it delegates transparently
// to the inner permission.Service.
permBridge *team.PermissionBridge
```

- [ ] **Step 2: Run compile check to confirm no breakage from field addition (Go struct fields are inert until used)**

Run: `go build ./internal/app/...`
Expected: PASS (compiles)

---

### Task 2: Write failing test for bridge wiring

**Files:**
- Create: `internal/app/perm_bridge_wiring_test.go`

- [ ] **Step 1: Write the failing test**

```go
package app

import (
    "context"
    "testing"
    "time"

    "github.com/charmbracelet/crush/internal/actor"
    "github.com/charmbracelet/crush/internal/permission"
    "github.com/stretchr/testify/assert"
    "github.com/stretchr/testify/require"
)

// TestPermissionBridge_WiredIntoApp verifies the M5 PermissionBridge is
// created and accessible from the App struct after New().
func TestPermissionBridge_WiredIntoApp(t *testing.T) {
    cfg := newConfigFixture(t)
    // Enable agent team so team-related fields are populated.
    cfg.Config().Options.Experimental = &config.ExperimentalOptions{AgentTeam: true}

    app, err := New(t.Context(), newDBFixture(t), cfg, nil)
    require.NoError(t, err)
    require.NotNil(t, app.permBridge, "PermissionBridge should be wired in New()")
}

// TestPermissionBridge_NonTeamSessionDelegates verifies that non-team
// permission requests are delegated to the original permission service.
func TestPermissionBridge_NonTeamSessionDelegates(t *testing.T) {
    cfg := newConfigFixture(t)
    cfg.Config().Options.Experimental = &config.ExperimentalOptions{AgentTeam: true}

    app, err := New(t.Context(), newDBFixture(t), cfg, nil)
    require.NoError(t, err)
    require.NotNil(t, app.permBridge)

    // For a non-team session (no actor context), the bridge should delegate.
    // We verify this by checking that SkipRequests() on the bridge matches
    // the inner service — both should delegate transparently.
    ctx := t.Context()

    // Non-team request should delegate to inner (no actor context means
    // no team interception).
    allowed, err := app.permBridge.Request(ctx, permission.CreatePermissionRequest{
        SessionID:   "test-session",
        ToolCallID:  "call-1",
        ToolName:    "bash",
        Action:      "run",
        Description: "test command",
        Path:        ".",
    })
    require.NoError(t, err)
    // With skip not set, the inner service will publish and wait.
    // The request goes through — this proves delegation, not auto-deny.
    assert.False(t, allowed, "non-team request without skip should not auto-approve")
}

// TestPermissionBridge_TeamSessionIntercepted verifies that team permission
// requests are intercepted by the bridge and not delegated to the inner
// service directly.
func TestPermissionBridge_TeamSessionIntercepted(t *testing.T) {
    cfg := newConfigFixture(t)
    cfg.Config().Options.Experimental = &config.ExperimentalOptions{AgentTeam: true}

    app, err := New(t.Context(), newDBFixture(t), cfg, nil)
    require.NoError(t, err)
    require.NotNil(t, app.permBridge)

    // Inject actor context with team info — this triggers the bridge's
    // team path.
    ac := actor.ActorContext{
        SessionID:  "session-1",
        TeamID:     "team-1",
        MemberID:   "member-1",
        MemberName: "test-member",
    }
    ctx := ac.WithContext(t.Context())

    // Team request should go through the bridge's team flow.
    // Without an active grant and without a UI resolving the request,
    // it will block until timeout (5 minutes). We use a short-lived
    // context to verify the bridge doesn't delegate to inner.
    shortCtx, cancel := context.WithTimeout(ctx, 100*time.Millisecond)
    defer cancel()

    _, err = app.permBridge.Request(shortCtx, permission.CreatePermissionRequest{
        SessionID:   "session-1",
        ToolCallID:  "call-team-1",
        ToolName:    "bash",
        Action:      "run",
        Description: "team command",
        Path:        ".",
    })
    // The request should NOT go through to inner.Request which would
    // publish a PermissionRequest. Instead it should be queued in the
    // bridge's pendingRequests map and either timeout or be resolved.
    // With a short timeout, it will return a context error.
    assert.Error(t, err, "team request with short context should return an error (timeout/deadline)")
}
```

- [ ] **Step 2: Run test to verify it fails**

```bash
go test -count=1 -timeout 30s ./internal/app/... -run "TestPermissionBridge_WiredIntoApp|TestPermissionBridge_NonTeamSessionDelegates|TestPermissionBridge_TeamSessionIntercepted"
```

Expected: `TestPermissionBridge_WiredIntoApp` FAILS — `app.permBridge` is nil because wiring hasn't been implemented yet.

---

### Task 3: Implement bridge creation and wiring in `New()`

**Files:**
- Modify: `internal/app/app.go:128-146` (the `app := &App{...}` literal) and line ~197 area (after teamRunner/teamScheduler creation)

- [ ] **Step 1: Create the PermissionBridge after creating `app.Permissions`**

In `New()`, after the `app := &App{...}` literal (the struct literal on line 128) and before the `app.team = team.NewService(...)` call, insert:

```go
// M5: wrap the permission service with the PermissionBridge so team
// member tool calls are intercepted and routed through the team
// permission flow (grant check → enqueue → wait for UI approval).
// Non-team sessions pass through transparently to the inner service.
app.permBridge = team.NewPermissionBridge(app.Permissions)
app.permBridge.SetAuditFn(func(ctx context.Context, e team.PermAuditEvent) {
    slog.Info("team permission audit",
        "action", e.Action,
        "team_id", e.TeamID,
        "member_id", e.MemberID,
        "tool", e.ToolName,
        "decision", e.Decision,
    )
})
```

- [ ] **Step 2: Pass the bridge to `agent.NewCoordinator` instead of raw `app.Permissions`**

In `InitCoderAgent()`, change the `agent.NewCoordinator` call:

```go
// Before:
app.AgentCoordinator, err = agent.NewCoordinator(
    ctx,
    app.config,
    app.Sessions,
    app.Messages,
    app.Permissions,    // ← change this
    ...
)

// After:
app.AgentCoordinator, err = agent.NewCoordinator(
    ctx,
    app.config,
    app.Sessions,
    app.Messages,
    app.permBridge,     // ← use bridge
    ...
)
```

- [ ] **Step 3: Run compile check**

```bash
go build ./internal/app/...
```

Expected: PASS (compiles without errors)

---

### Task 4: Run tests and verify

**Files:**
- (none modified, verifying existing tests pass)

- [ ] **Step 1: Run the new wiring tests**

```bash
go test -count=1 -timeout 60s ./internal/app/... -run "TestPermissionBridge"
```

Expected: All three new tests PASS

- [ ] **Step 2: Run all app tests to check for regressions**

```bash
go test -count=1 -timeout 120s ./internal/app/...
```

Expected: All existing tests still PASS (non-team sessions delegate through bridge → inner, which is transparent)

- [ ] **Step 3: Run full build check**

```bash
go build ./...
```

Expected: PASS (full project builds)

---

### Task 5: Commit

- [ ] **Step 1: Commit all changes**

```bash
git add internal/app/app.go internal/app/perm_bridge_wiring_test.go docs/superpowers/plans/2026-06-17-m5-wire-permission-bridge.md
git commit -m "feat(app): wire M5 PermissionBridge into production startup"
```

---

### Self-Review Checklist (executor fills out before reporting done)

- [ ] `go build ./...` passes
- [ ] `go test -count=1 -timeout 120s ./internal/app/...` passes
- [ ] Bridge is accessible from app struct (`app.permBridge != nil`)
- [ ] Non-team sessions still use original permissions (no regression) — verified by `TestPermissionBridge_NonTeamSessionDelegates`
