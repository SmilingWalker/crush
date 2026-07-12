# M5-08b Permission Requested + Late Response Audit Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Fire `auditFn` for the 2 remaining untriggered bridge-level audit actions (`permission_requested` and `late_response`) so all team permission audit events persist to `team_audit_events`.

**Architecture:** Two surgical insertions in `internal/team/permission_bridge.go`: (1) fire `PermAuditPermissionRequested` after `requestWithUI` enqueues the entry, (2) fire `PermAuditLateResponse` when `ResolveRequest` finds the entry already gone. Both use the existing `b.auditFn` callback which M5-08a wired to `AuditStore.AppendAudit`.

**Tech Stack:** Go 1.26, `log/slog`, existing `internal/team` permission plumbing.

## Global Constraints

- `-race` is broken (cgo toolchain fault) — tests use `-count=3`, not `-race`.
- `go build ./...` is authoritative (gopls in worktrees often misreports).
- Single-line conventional commit, no trailer (no Co-Authored-By, no Signed-off-by). One commit per task.
- workspaceID is the hardcoded string `"default"` (bridge constructed with it in tests).
- `ResolveRequest` signature is NOT changed — `late_response` audit uses `context.Background()` because the caller's ctx may be cancelled in the late scenario.

---

## File Map

| File | Responsibility | Action |
|------|----------------|--------|
| `internal/team/permission_bridge.go` | `requestWithUI` (add `permission_requested` audit) + `ResolveRequest` (add `late_response` audit) | Modify |
| `internal/team/permission_bridge_test.go` | 2 new tests for the 2 new audit events | Modify |

---

## Task 1: Fire permission_requested audit on enqueue + late_response audit on missing resolve

**Files:**
- Modify: `internal/team/permission_bridge.go`
- Modify: `internal/team/permission_bridge_test.go`

**Interfaces:**
- Consumes: `PermAuditPermissionRequested` and `PermAuditLateResponse` constants (already declared at lines 28, 36). `b.auditFn` callback (already wired). `b.workspaceID` field (from M5-08a Task 1).
- Produces: all 8 bridge-reachable audit actions now fire (6 existing + 2 new).

- [ ] **Step 1: Write the failing test for permission_requested**

In `internal/team/permission_bridge_test.go`, add this test at the end of the file. It follows the pattern of `TestPermissionBridge_TeamSessionShowsPopupWhenSkipOff` — drives `Request` in a goroutine, polls `TeamContextFor` to detect enqueue, then cancels ctx to exit:

```go
// TestPermissionBridge_RequestedAuditFires verifies that requestWithUI fires
// the permission_requested audit action when a team member's permission
// request is enqueued for UI display.
func TestPermissionBridge_RequestedAuditFires(t *testing.T) {
	inner := permission.NewPermissionService(t.TempDir(), false, nil) // skip=false
	bridge := NewPermissionBridge("default", inner)
	bridge.SetRequestTimeout(5 * time.Second)

	var mu sync.Mutex
	var captured []PermAuditEvent
	bridge.SetAuditFunc(func(ctx context.Context, event PermAuditEvent) {
		mu.Lock()
		captured = append(captured, event)
		mu.Unlock()
	})

	ctx, cancel := context.WithCancel(context.Background())
	ac := actor.ActorContext{
		SessionID: "sess-req", TeamID: "team-1", MemberID: "member-1",
		TaskID: "task-1", RunID: "run-1",
	}
	ctx = ac.WithContext(ctx)

	go func() {
		_, _ = bridge.Request(ctx, permission.CreatePermissionRequest{
			SessionID:  "sess-req",
			ToolCallID: "call-req",
			ToolName:   "write",
			Action:     "execute",
			Path:       "docs/foo.md",
		})
	}()

	// Wait for the request to reach requestWithUI (entry appears in teamContexts).
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if _, ok := bridge.TeamContextFor("call-req"); ok {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	_, visible := bridge.TeamContextFor("call-req")
	require.True(t, visible, "request should reach requestWithUI")

	// permission_requested should have fired by now.
	mu.Lock()
	var reqEvent *PermAuditEvent
	for i := range captured {
		if captured[i].Action == PermAuditPermissionRequested {
			reqEvent = &captured[i]
			break
		}
	}
	mu.Unlock()

	require.NotNil(t, reqEvent, "permission_requested audit should fire on enqueue")
	require.Equal(t, "default", reqEvent.WorkspaceID)
	require.Equal(t, "sess-req", reqEvent.SessionID)
	require.Equal(t, "call-req", reqEvent.ToolCallID)
	require.Equal(t, "team-1", reqEvent.TeamID)
	require.Equal(t, "member-1", reqEvent.MemberID)
	require.Equal(t, "task-1", reqEvent.TaskID)
	require.Equal(t, "run-1", reqEvent.RunID)
	require.Equal(t, "write", reqEvent.ToolName)

	cancel() // exit the blocking Request
}
```

Ensure `sync` is imported (it likely already is — check the import block).

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/team/... -run "TestPermissionBridge_RequestedAuditFires" -count=1 -v`
Expected: FAIL — `reqEvent` is nil because `permission_requested` audit never fires.

- [ ] **Step 3: Write the failing test for late_response**

Add this test right after the previous one:

```go
// TestPermissionBridge_LateResponseAudit verifies that ResolveRequest fires
// the late_response audit action when called for a request that is no longer
// pending (already resolved / timed out / cancelled / never existed).
func TestPermissionBridge_LateResponseAudit(t *testing.T) {
	bridge := NewPermissionBridge("default", nil)

	var captured []PermAuditEvent
	var mu sync.Mutex
	bridge.SetAuditFunc(func(ctx context.Context, event PermAuditEvent) {
		mu.Lock()
		captured = append(captured, event)
		mu.Unlock()
	})

	err := bridge.ResolveRequest("nonexistent-req", true, "call")
	require.Error(t, err)

	mu.Lock()
	require.Len(t, captured, 1)
	require.Equal(t, PermAuditLateResponse, captured[0].Action)
	require.Equal(t, "nonexistent-req", captured[0].ToolCallID)
	require.Equal(t, "default", captured[0].WorkspaceID)
	mu.Unlock()
}
```

- [ ] **Step 4: Run both tests to verify they fail**

Run: `go test ./internal/team/... -run "TestPermissionBridge_RequestedAuditFires|TestPermissionBridge_LateResponseAudit" -count=1 -v`
Expected: Both FAIL.

- [ ] **Step 5: Implement permission_requested audit in requestWithUI**

In `internal/team/permission_bridge.go`, find `requestWithUI`. After the `b.queueMu.Unlock()` that follows `b.entries[reqID] = entry` (around line 379), and before `_ = b.queue.Enqueue(...)`, insert:

```go
	// M5-08b: audit that a team permission request was enqueued for UI decision.
	b.auditFn(ctx, PermAuditEvent{
		WorkspaceID: b.workspaceID, SessionID: opts.SessionID, ToolCallID: reqID,
		Action: PermAuditPermissionRequested, TeamID: ac.TeamID, MemberID: ac.MemberID,
		TaskID: ac.TaskID, RunID: ac.RunID, ToolName: opts.ToolName,
		Resource: opts.Path, Timestamp: now,
	})
```

This goes between the `b.queueMu.Unlock()` line and the `_ = b.queue.Enqueue(...)` line. The `now` variable is already defined earlier in the function (around line 363).

- [ ] **Step 6: Implement late_response audit in ResolveRequest**

In the same file, find `ResolveRequest`. In the `if !ok` branch (around line 441), replace:

```go
	if !ok {
		b.queueMu.Unlock()
		slog.Debug("perm_bridge: ResolveRequest not pending", "tool_call_id", reqID)
		return fmt.Errorf("request not pending: %s", reqID)
	}
```

with:

```go
	if !ok {
		b.queueMu.Unlock()
		slog.Debug("perm_bridge: ResolveRequest not pending (late or unknown)", "tool_call_id", reqID)
		// M5-08b: audit the late response. We cannot distinguish "never existed"
		// from "already terminated" here — reqID is a program-generated UUID, so
		// treating all misses as late_response is acceptable.
		b.auditFn(context.Background(), PermAuditEvent{
			WorkspaceID: b.workspaceID, ToolCallID: reqID,
			Action: PermAuditLateResponse, Timestamp: time.Now(),
		})
		return fmt.Errorf("request not pending: %s", reqID)
	}
```

- [ ] **Step 7: Run both tests to verify they pass**

Run: `go test ./internal/team/... -run "TestPermissionBridge_RequestedAuditFires|TestPermissionBridge_LateResponseAudit" -count=1 -v`
Expected: Both PASS.

- [ ] **Step 8: Run full team suite to verify no regressions**

Run: `go test ./internal/team/... -count=1`
Expected: PASS.

Run: `go build ./...`
Expected: PASS.

- [ ] **Step 9: Commit**

```bash
git add internal/team/permission_bridge.go internal/team/permission_bridge_test.go
git commit -m "feat(team): fire permission_requested and late_response audit events"
```

---

## Self-Review

**Spec coverage:**
- ✅ `permission_requested` fires in `requestWithUI` on enqueue (Task 1 Step 5)
- ✅ `late_response` fires in `ResolveRequest` on missing entry (Task 1 Step 6)
- ✅ Both persist to `team_audit_events` automatically (M5-08a's `PermEventToAuditEvent` + app.go wiring handles this — no extra work needed)
- ✅ `go build` + `go test` verification (Task 1 Steps 8)

**Placeholder scan:** No TBD/TODO. All code shown verbatim. Test code is complete and runnable.

**Type consistency:** `PermAuditPermissionRequested` and `PermAuditLateResponse` are the exact constant names declared at `permission_bridge.go:28,36`. `PermAuditEvent` field names match the struct from M5-08a Task 1. `b.workspaceID`, `b.auditFn`, `opts.SessionID`, `opts.ToolName`, `opts.Path`, `ac.TeamID`, `ac.MemberID`, `ac.TaskID`, `ac.RunID` all verified against source.
