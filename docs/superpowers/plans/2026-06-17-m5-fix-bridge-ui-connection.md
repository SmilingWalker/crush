# Fix M5 PermissionBridge ↔ UI Connection

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Connect `PermissionBridge.ResolveRequest()` to the UI dialog decisions so that when a team member calls a tool, the permission dialog appears and user decisions (Allow Once / Allow for Task / Deny) unblock the waiting member.

**Architecture:** The bridge currently blocks on a channel waiting for `ResolveRequest()` which nobody calls. Three fixes: (1) The bridge publishes a `permission.PermissionRequest` to the inner broker so the UI shows a dialog. (2) `PermissionAllowOnce`/`PermissionAllowForTask`/`PermissionDenyTeam` actions route through a new `Workspace.PermBridgeResolve()` method to the bridge. (3) The bridge's select includes `ctx.Done()` for clean cancellation. The bridge request ID is set to `opts.ToolCallID` (instead of auto-generated) so the UI can reference it.

**Tech Stack:** Go, testify/assert

---

### Task 1: Add `pubsub.Publisher` to `permission.Service` and wire bridge delegation

**Files:**
- Modify: `internal/permission/permission.go:65-85` — Service interface
- Modify: `internal/team/permission_bridge.go` — add Publish delegation
- Modify: `internal/team/permission_bridge_test.go` — test Publish delegation

The `permission.Service` interface needs `pubsub.Publisher[PermissionRequest]` so the bridge can publish requests to the UI event stream. The concrete `*permissionService` already has `Publish` from its embedded `*pubsub.Broker[PermissionRequest]`.

- [ ] **Step 1: Extend the Service interface**

In `internal/permission/permission.go`, add `pubsub.Publisher[PermissionRequest]` to the `Service` interface:

```go
type Service interface {
	pubsub.Subscriber[PermissionRequest]
	pubsub.Publisher[PermissionRequest] // M5: bridge publishes team permission requests
	GrantPersistent(permission PermissionRequest) bool
	...
```

- [ ] **Step 2: Add Publish delegation to PermissionBridge**

In `internal/team/permission_bridge.go`, add a `Publish` method after the existing delegation methods (after line ~354, before the closing of the Subscribe method):

```go
// Publish delegates to the inner permission.Service so the bridge can
// publish team permission requests to the UI event stream.
func (b *PermissionBridge) Publish(et pubsub.EventType, payload permission.PermissionRequest) {
	b.inner.Publish(et, payload)
}
```

Requires importing `"github.com/charmbracelet/crush/internal/permission"` (already imported) and `"github.com/charmbracelet/crush/internal/pubsub"` (verify it's already imported).

- [ ] **Step 3: Test Publish delegation**

Add to `internal/team/permission_bridge_test.go`:

```go
// TestPermissionBridge_PublishDelegatesToInner verifies that Publish on the
// bridge delegates to inner.Publish, enabling team permission requests to
// reach the UI event stream.
func TestPermissionBridge_PublishDelegatesToInner(t *testing.T) {
	inner := mockPermissionService{skip: true}
	bridge := NewPermissionBridge(inner)
	ctx := context.Background()

	req := permission.PermissionRequest{
		ID: "req-1", SessionID: "s1", ToolCallID: "t1", ToolName: "bash",
	}
	bridge.Publish(pubsub.CreatedEvent, req)

	// Verify the inner service received the publish.
	assert.True(t, inner.published, "bridge.Publish should delegate to inner")
	assert.Equal(t, "req-1", inner.lastPublished.ID)
}
```

This needs a `mockPermissionService` type. Add it to the test file:

```go
type mockPermissionService struct {
	*permission.PermissionService // will be nil — we only test delegation
	skip                          bool
	published                     bool
	lastPublished                 permission.PermissionRequest
	grantPersistent               bool
	grantOneTime                  bool
	denied                        bool
}

func (m *mockPermissionService) Publish(_ pubsub.EventType, p permission.PermissionRequest) {
	m.published = true
	m.lastPublished = p
}
```
Wait — we can't embed `*permission.PermissionService` because it's unexported. Let's use a different approach: use a real `permission.Service` instead of a mock, and verify Publish reaches the inner service by subscribing and watching.

Actually, the simplest approach: create a real `permission.Service` via `permission.NewPermissionService`, publish through the bridge, then subscribe to the inner service and verify the event arrives.

```go
func TestPermissionBridge_PublishDelegatesToInner(t *testing.T) {
	inner := permission.NewPermissionService("/tmp", true, nil)
	bridge := NewPermissionBridge(inner)
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()

	ch := inner.Subscribe(ctx)

	req := permission.PermissionRequest{
		ID: "req-1", SessionID: "s1", ToolCallID: "t1", ToolName: "bash",
	}
	bridge.Publish(pubsub.CreatedEvent, req)

	select {
	case ev := <-ch:
		assert.Equal(t, "req-1", ev.Payload.ID)
	case <-ctx.Done():
		t.Fatal("timed out waiting for published event")
	}
}
```

- [ ] **Step 4: Run tests to verify**

```bash
go test -count=1 -timeout 30s ./internal/team/... -run "TestPermissionBridge_PublishDelegatesToInner" -v
```

Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/permission/permission.go internal/team/permission_bridge.go internal/team/permission_bridge_test.go
git commit -m "feat(permission): add Publisher to Service interface, delegate from bridge"
```

---

### Task 2: Fix bridge Request() — use ToolCallID, add ctx.Done(), publish to UI

**Files:**
- Modify: `internal/team/permission_bridge.go:236-292`

Three changes to the bridge's `Request()` method for team sessions:

1. Use `opts.ToolCallID` as the request ID (instead of auto-generated nanos)
2. Publish a `permission.PermissionRequest` to the inner broker so the UI shows a dialog
3. Add `<-ctx.Done()` to the select to prevent goroutine leaks on cancellation

- [ ] **Step 1: Write failing test for ctx.Done() in select**

In `internal/team/permission_bridge_test.go`:

```go
// TestPermissionBridge_RequestCancelable verifies that a team permission
// request respects context cancellation (ctx.Done() in select).
func TestPermissionBridge_RequestCancelable(t *testing.T) {
	inner := permission.NewPermissionService(t.TempDir(), false, nil)
	bridge := NewPermissionBridge(inner)

	ac := actor.ActorContext{
		SessionID: "s1", TeamID: "team-1", MemberID: "member-1",
	}
	ctx := ac.WithContext(t.Context())
	cancelCtx, cancel := context.WithCancel(ctx)

	// Fire Request in a goroutine; it should block on the team path.
	errCh := make(chan error, 1)
	go func() {
		_, err := bridge.Request(cancelCtx, permission.CreatePermissionRequest{
			SessionID: "s1", ToolCallID: "tc-1", ToolName: "bash",
			Action: "run", Description: "test", Path: ".",
		})
		errCh <- err
	}()

	// Give the goroutine time to enter the select.
	time.Sleep(50 * time.Millisecond)

	// Cancel the context — the select should wake up.
	cancel()

	select {
	case err := <-errCh:
		assert.ErrorIs(t, err, context.Canceled,
			"bridge should return context.Canceled when ctx is cancelled")
	case <-time.After(10 * time.Second):
		t.Fatal("Request did not return after context cancellation")
	}
}
```

- [ ] **Step 2: Run test — verify it FAILS**

```bash
go test -count=1 -timeout 30s ./internal/team/... -run "TestPermissionBridge_RequestCancelable" -v
```

Expected: FAIL — "Request did not return after context cancellation" (ctx.Done() not in select yet)

- [ ] **Step 3: Implement all three fixes in Request()**

Replace the team path section in `permission_bridge.go` `Request()` (the code after `// Create a permission request for UI approval.` through the select):

Before:
```go
	// Create a permission request for UI approval.
	reqID := fmt.Sprintf("perm-%d", time.Now().UnixNano())
	req := &PermissionRequest{
		ID: reqID, TeamID: ac.TeamID, MemberID: ac.MemberID,
		SessionID: opts.SessionID, Status: "pending", RequestedScope: "call",
		ToolName: opts.ToolName, Action: opts.Action,
		CreatedAt: time.Now(), ExpiresAt: time.Now().Add(5 * time.Minute),
	}
	_ = b.store.CreateRequest(ctx, req)

	// Enqueue in the permission queue for expiry management.
	if err := b.queue.Enqueue(ctx, req); err != nil {
		req.Status = "expired"
		_ = b.store.UpdateRequest(ctx, req)
		return false, nil
	}

	// Wait for UI decision via channel.
	ch := make(chan bool, 1)
	b.pendingMu.Lock()
	b.pendingRequests[reqID] = ch
	b.pendingMu.Unlock()

	b.auditFn(ctx, PermAuditEvent{
		Action: PermAuditPermissionRequested, TeamID: ac.TeamID, MemberID: ac.MemberID,
		ToolName: opts.ToolName, Timestamp: time.Now(),
	})

	select {
	case allowed := <-ch:
		return allowed, nil
	case <-time.After(5 * time.Minute):
		// Clean up the pending request entry to prevent channel leak.
		b.pendingMu.Lock()
		delete(b.pendingRequests, reqID)
		b.pendingMu.Unlock()
		req.Status = "expired"
		_ = b.store.UpdateRequest(ctx, req)
		return false, nil
	}
```

After:
```go
	// Use ToolCallID as the request ID so the UI can reference it when
	// calling ResolveRequest.
	reqID := opts.ToolCallID
	req := &PermissionRequest{
		ID: reqID, TeamID: ac.TeamID, MemberID: ac.MemberID,
		SessionID: opts.SessionID, Status: "pending", RequestedScope: "call",
		ToolName: opts.ToolName, Action: opts.Action,
		CreatedAt: time.Now(), ExpiresAt: time.Now().Add(5 * time.Minute),
	}
	_ = b.store.CreateRequest(ctx, req)

	// Enqueue in the permission queue for expiry management.
	if err := b.queue.Enqueue(ctx, req); err != nil {
		req.Status = "expired"
		_ = b.store.UpdateRequest(ctx, req)
		return false, nil
	}

	// Publish a permission.PermissionRequest to the inner broker so the
	// UI subscription sees it and shows the permission dialog.
	b.inner.Publish(pubsub.CreatedEvent, permission.PermissionRequest{
		ID:          reqID,
		SessionID:   opts.SessionID,
		ToolCallID:  opts.ToolCallID,
		ToolName:    opts.ToolName,
		Description: opts.Description,
		Action:      opts.Action,
		Params:      opts.Params,
		Path:        opts.Path,
	})

	// Wait for UI decision via channel.
	ch := make(chan bool, 1)
	b.pendingMu.Lock()
	b.pendingRequests[reqID] = ch
	b.pendingMu.Unlock()

	b.auditFn(ctx, PermAuditEvent{
		Action: PermAuditPermissionRequested, TeamID: ac.TeamID, MemberID: ac.MemberID,
		ToolName: opts.ToolName, Timestamp: time.Now(),
	})

	select {
	case allowed := <-ch:
		return allowed, nil
	case <-ctx.Done():
		// Clean up the pending request entry to prevent channel leak.
		b.pendingMu.Lock()
		delete(b.pendingRequests, reqID)
		b.pendingMu.Unlock()
		req.Status = "canceled"
		_ = b.store.UpdateRequest(ctx, req)
		return false, ctx.Err()
	case <-time.After(5 * time.Minute):
		// Clean up the pending request entry to prevent channel leak.
		b.pendingMu.Lock()
		delete(b.pendingRequests, reqID)
		b.pendingMu.Unlock()
		req.Status = "expired"
		_ = b.store.UpdateRequest(ctx, req)
		return false, nil
	}
```

Also remove the `"fmt"` import since `fmt.Sprintf` is no longer used for reqID.

- [ ] **Step 4: Run cancelable test — verify it PASSES**

```bash
go test -count=1 -timeout 30s ./internal/team/... -run "TestPermissionBridge_RequestCancelable" -v
```

Expected: PASS

- [ ] **Step 5: Run all team tests**

```bash
go test -count=1 -timeout 60s ./internal/team/...
```

Expected: PASS (all existing tests plus new ones)

- [ ] **Step 6: Commit**

```bash
git add internal/team/permission_bridge.go internal/team/permission_bridge_test.go
git commit -m "fix(team): use ToolCallID as bridge reqID, publish to UI, add ctx.Done()"
```

---

### Task 3: Wire UI → bridge via Workspace

**Files:**
- Modify: `internal/workspace/workspace.go:100-113` — add `PermBridgeResolve` to interface
- Modify: `internal/workspace/app_workspace.go` — implement `PermBridgeResolve`
- Modify: `internal/workspace/client_workspace.go` — add stub (returns error, client mode unsupported)
- Modify: `internal/ui/dialog/actions.go:69-72` — add `PermissionDenyTeam` action
- Modify: `internal/ui/dialog/permissions.go:266` — use `PermissionDenyTeam` when teamCtx is set
- Modify: `internal/ui/model/ui.go:1620-1629` — handle team permission actions

- [ ] **Step 1: Add `PermBridgeResolve` to Workspace interface**

In `internal/workspace/workspace.go`, after `PermissionSetSkipRequests`:

```go
	// PermBridgeResolve resolves a pending team permission request.
	// reqID is the ToolCallID. allowed=true grants, false denies.
	// scope is "call" for Allow Once, "task" for Allow for Task.
	PermBridgeResolve(reqID string, allowed bool, scope string) error
```

- [ ] **Step 2: Implement in AppWorkspace**

In `internal/workspace/app_workspace.go`, after the Permission methods (~line 216):

```go
// PermBridgeResolve resolves a pending team permission request via the
// PermissionBridge. It is called by the UI when the user clicks Allow Once,
// Allow for Task, or Deny on a team permission dialog.
func (w *AppWorkspace) PermBridgeResolve(reqID string, allowed bool, scope string) error {
	if w.app.PermBridge() == nil {
		return fmt.Errorf("permission bridge not configured")
	}
	if err := w.app.PermBridge().ResolveRequest(reqID, allowed, scope); err != nil {
		// If the request is no longer pending (e.g., timed out or already
		// resolved by another client), log and treat as non-fatal so the
		// UI can close the dialog gracefully.
		slog.Debug("team permission resolve failed", "req_id", reqID, "error", err)
		return err
	}
	return nil
}
```

Need to import `"log/slog"` and `"fmt"` in `app_workspace.go`.

- [ ] **Step 3: Add `PermBridge()` accessor to App**

In `internal/app/app.go`, add after the `SetCurrentSession` field or near the existing team accessors:

```go
// PermBridge returns the M5 PermissionBridge, or nil if not configured.
func (app *App) PermBridge() *team.PermissionBridge {
	return app.permBridge
}
```

- [ ] **Step 4: Add stub in ClientWorkspace**

In `internal/workspace/client_workspace.go`, after `PermissionDeny`:

```go
func (w *ClientWorkspace) PermBridgeResolve(_ string, _ bool, _ string) error {
	return fmt.Errorf("team permission bridge not available in client mode")
}
```

- [ ] **Step 5: Add `PermissionDenyTeam` action**

In `internal/ui/dialog/actions.go`, after `PermissionDeny`:

```go
	// M5 team deny — distinct from non-team deny so the UI handler can route
	// to PermBridgeResolve instead of PermissionDeny.
	PermissionDenyTeam PermissionAction = "deny_team"
```

- [ ] **Step 6: Use PermissionDenyTeam in team dialog**

In `internal/ui/dialog/permissions.go`, the `respond` method in the key handler for Deny (line ~267):

```go
case key.Matches(msg, p.keyMap.Deny):
	if p.teamCtx != nil {
		return p.respond(PermissionDenyTeam)
	}
	return p.respond(PermissionDeny)
```

And in `selectCurrentOption` (line ~319), the default branch for team:

```go
default:
	return p.respond(PermissionDenyTeam)
```

- [ ] **Step 7: Handle team actions in UI model**

In `internal/ui/model/ui.go`, the `dialog.ActionPermissionResponse` case (~line 1620), add team action handling BEFORE the existing cases:

```go
case dialog.ActionPermissionResponse:
	m.dialog.CloseDialog(dialog.PermissionsID)
	switch msg.Action {
	case dialog.PermissionAllowOnce:
		_ = m.com.Workspace.PermBridgeResolve(msg.Permission.ID, true, "call")
	case dialog.PermissionAllowForTask:
		_ = m.com.Workspace.PermBridgeResolve(msg.Permission.ID, true, "task")
	case dialog.PermissionDenyTeam:
		_ = m.com.Workspace.PermBridgeResolve(msg.Permission.ID, false, "")
	case dialog.PermissionAllow:
		m.com.Workspace.PermissionGrant(msg.Permission)
	case dialog.PermissionAllowForSession:
		m.com.Workspace.PermissionGrantPersistent(msg.Permission)
	case dialog.PermissionDeny:
		m.com.Workspace.PermissionDeny(msg.Permission)
	}
```

- [ ] **Step 8: Run build and tests**

```bash
go build ./... && go test -count=1 -timeout 60s ./internal/team/... ./internal/app/...
```

Expected: PASS

- [ ] **Step 9: Commit**

```bash
git add internal/workspace/workspace.go internal/workspace/app_workspace.go internal/workspace/client_workspace.go internal/ui/dialog/actions.go internal/ui/dialog/permissions.go internal/ui/model/ui.go internal/app/app.go
git commit -m "feat(app,ui): wire PermissionBridge.ResolveRequest to UI dialog decisions"
```

---

### Task 4: Integration verification and final commit

- [ ] **Step 1: Full build**

```bash
go build ./...
```

Expected: PASS

- [ ] **Step 2: All affected test suites**

```bash
go test -count=1 -timeout 120s ./internal/team/... ./internal/app/... ./internal/permission/...
```

Expected: PASS (all tests)

- [ ] **Step 3: Verify self-review checklist**

- [ ] `go build ./...` passes
- [ ] `go test -count=1 ./internal/team/...` passes
- [ ] `go test -count=1 ./internal/app/...` passes
- [ ] Bridge publishes to inner broker (verified by test)
- [ ] Bridge select includes ctx.Done() (verified by test)
- [ ] UI model handles PermissionAllowOnce/AllowForTask/DenyTeam via PermBridgeResolve
- [ ] Non-team permissions unaffected (original PermissionAllow/AllowForSession/Deny still route to inner service)
