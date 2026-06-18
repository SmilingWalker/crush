package team

import (
	"context"
	"testing"
	"time"

	"github.com/charmbracelet/crush/internal/actor"
	"github.com/charmbracelet/crush/internal/permission"
	"github.com/charmbracelet/crush/internal/pubsub"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestGrantStore_FindActiveGrant(t *testing.T) {
	gs := NewGrantStore()
	ctx := context.Background()
	err := gs.CreateGrant(ctx, &Grant{
		ID: "g1", MemberID: "m1", SessionID: "s1", ToolName: "bash", Action: "execute",
		Scope: "call", ExpiresAt: time.Now().Add(1 * time.Hour),
	})
	assert.NoError(t, err)

	grant, ok := gs.FindActiveGrant(ctx, "s1", "bash", "execute")
	assert.True(t, ok)
	assert.Equal(t, "g1", grant.ID)
}

func TestGrantStore_FindActiveGrant_Expired(t *testing.T) {
	gs := NewGrantStore()
	ctx := context.Background()
	err := gs.CreateGrant(ctx, &Grant{
		ID: "g2", MemberID: "m2", SessionID: "s2", ToolName: "write", Action: "create",
		Scope: "call", ExpiresAt: time.Now().Add(-1 * time.Hour),
	})
	assert.NoError(t, err)

	_, ok := gs.FindActiveGrant(ctx, "s2", "write", "create")
	assert.False(t, ok)
}

func TestGrantStore_FindActiveGrant_NoMatch(t *testing.T) {
	gs := NewGrantStore()
	ctx := context.Background()
	err := gs.CreateGrant(ctx, &Grant{
		ID: "g3", MemberID: "m3", SessionID: "s3", ToolName: "bash", Action: "execute",
		Scope: "call", ExpiresAt: time.Now().Add(1 * time.Hour),
	})
	assert.NoError(t, err)

	_, ok := gs.FindActiveGrant(ctx, "other-session", "bash", "execute")
	assert.False(t, ok)
}

func TestPermissionStore_CreateAndGet(t *testing.T) {
	ps := NewPermissionStore()
	ctx := context.Background()
	req := &PermissionRequest{ID: "r1", Status: "pending"}
	err := ps.CreateRequest(ctx, req)
	assert.NoError(t, err)

	got, err := ps.GetRequest(ctx, "r1")
	assert.NoError(t, err)
	assert.Equal(t, "pending", got.Status)
}

func TestPermissionStore_GetRequest_NotFound(t *testing.T) {
	ps := NewPermissionStore()
	ctx := context.Background()

	_, err := ps.GetRequest(ctx, "nonexistent")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "not found")
}

func TestPermissionStore_UpdateRequest(t *testing.T) {
	ps := NewPermissionStore()
	ctx := context.Background()
	req := &PermissionRequest{ID: "r2", Status: "pending"}
	err := ps.CreateRequest(ctx, req)
	assert.NoError(t, err)

	req.Status = "allowed"
	err = ps.UpdateRequest(ctx, req)
	assert.NoError(t, err)

	got, err := ps.GetRequest(ctx, "r2")
	assert.NoError(t, err)
	assert.Equal(t, "allowed", got.Status)
}

func TestPermissionBridge_NewPermissionBridge(t *testing.T) {
	// PermissionBridge with nil inner (valid construction, just testing defaults).
	// The inner service is not called until Request is invoked.
	bridge := NewPermissionBridge(nil)
	assert.NotNil(t, bridge)
	assert.NotNil(t, bridge.store)
	assert.NotNil(t, bridge.grantStore)
	assert.NotNil(t, bridge.auditFn)
	assert.NotNil(t, bridge.pendingRequests)
}

func TestPermissionBridge_SetAuditFunc(t *testing.T) {
	bridge := NewPermissionBridge(nil)
	called := false
	bridge.SetAuditFunc(func(ctx context.Context, event PermAuditEvent) {
		called = true
	})
	// Verify the function was set by calling it
	bridge.auditFn(context.Background(), PermAuditEvent{})
	assert.True(t, called)
}

func TestPermissionBridge_ResolveRequest_NotFound(t *testing.T) {
	bridge := NewPermissionBridge(nil)
	err := bridge.ResolveRequest("nonexistent", true, "call")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "not pending")
}

// TestPermissionBridge_AutoAllowWhenSkipRequests verifies that team sessions
// auto-allow tool calls when SkipRequests (yolo mode) is enabled.
func TestPermissionBridge_AutoAllowWhenSkipRequests(t *testing.T) {
	inner := permission.NewPermissionService(t.TempDir(), true, nil) // skip=true
	bridge := NewPermissionBridge(inner)

	ac := actor.ActorContext{
		SessionID: "s1", TeamID: "team-1", MemberID: "member-1",
	}
	ctx := ac.WithContext(t.Context())

	allowed, err := bridge.Request(ctx, permission.CreatePermissionRequest{
		SessionID: "s1", ToolCallID: "tc-1", ToolName: "bash",
		Action: "run", Description: "test", Path: ".",
	})
	require.NoError(t, err)
	assert.True(t, allowed, "team session with SkipRequests should auto-allow")
}

// TestPermissionBridge_AutoDenyWhenNotSkipRequests verifies that team sessions
// auto-deny tool calls when SkipRequests (yolo mode) is disabled.
func TestPermissionBridge_AutoDenyWhenNotSkipRequests(t *testing.T) {
	inner := permission.NewPermissionService(t.TempDir(), false, nil) // skip=false
	bridge := NewPermissionBridge(inner)

	ac := actor.ActorContext{
		SessionID: "s1", TeamID: "team-1", MemberID: "member-1",
	}
	ctx := ac.WithContext(t.Context())

	allowed, err := bridge.Request(ctx, permission.CreatePermissionRequest{
		SessionID: "s1", ToolCallID: "tc-2", ToolName: "bash",
		Action: "run", Description: "test", Path: ".",
	})
	require.NoError(t, err)
	assert.False(t, allowed, "team session without SkipRequests should auto-deny")
}

// TestPermissionBridge_PublishDelegatesToInner verifies that Publish on the
// bridge delegates to inner.Publish, enabling team permission requests to
// reach the UI event stream.
func TestPermissionBridge_PublishDelegatesToInner(t *testing.T) {
	inner := permission.NewPermissionService(t.TempDir(), true, nil)
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

// TestPermissionBridge_StoresAndClearsTeamContext verifies that when a team
// session is the active view, Request routes through requestWithUI, stores a
// TeamPermissionContext under the request ID, and clears it once resolved.
func TestPermissionBridge_StoresAndClearsTeamContext(t *testing.T) {
	inner := permission.NewPermissionService(t.TempDir(), false, nil)
	bridge := NewPermissionBridge(inner)
	bridge.SetRequestTimeout(5 * time.Second) // generous; we resolve promptly

	tracker := NewActiveSessionTracker()
	tracker.SetSession("s-active", "member-1")
	bridge.SetActiveSessionTracker(tracker)

	ac := actor.ActorContext{
		SessionID: "s-active", TeamID: "team-1", MemberID: "member-1",
		MemberName: "coder-1", MemberRole: "programmer",
	}
	ctx := ac.WithContext(t.Context())

	opts := permission.CreatePermissionRequest{
		SessionID: "s-active", ToolCallID: "tc-ctx-1", ToolName: "bash",
		Action: "run", Description: "test", Path: ".",
	}

	type result struct {
		allowed bool
		err     error
	}
	resCh := make(chan result, 1)
	go func() {
		allowed, err := bridge.Request(ctx, opts)
		resCh <- result{allowed, err}
	}()

	// Poll until the bridge has stored the team context (stored before publish/block).
	deadline := time.Now().Add(2 * time.Second)
	var tctx *TeamPermissionContext
	for time.Now().Before(deadline) {
		if c, ok := bridge.TeamContextFor("tc-ctx-1"); ok {
			tctx = c
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	require.NotNil(t, tctx, "team context should be stored while request is pending")
	assert.Equal(t, "team-1", tctx.TeamName)
	assert.Equal(t, "coder-1", tctx.MemberName)
	assert.Equal(t, "programmer", tctx.MemberRole)

	// Resolve from the "UI".
	require.NoError(t, bridge.ResolveRequest("tc-ctx-1", true, "call"))

	select {
	case r := <-resCh:
		require.NoError(t, r.err)
		assert.True(t, r.allowed, "resolved-allow should return true")
	case <-time.After(2 * time.Second):
		t.Fatal("Request did not return after ResolveRequest")
	}

	// Context must be cleared after resolution.
	_, ok := bridge.TeamContextFor("tc-ctx-1")
	assert.False(t, ok, "team context should be cleared after resolve")
}

// TestPermissionBridge_TeamContextFor_NotFound verifies the accessor returns
// false for an unknown request ID.
func TestPermissionBridge_TeamContextFor_NotFound(t *testing.T) {
	bridge := NewPermissionBridge(nil)
	_, ok := bridge.TeamContextFor("nope")
	assert.False(t, ok)
}

// TestPermissionBridge_TimeoutDeniesAndCleansUp verifies that an unresolved
// request denies after the bridge-local timeout and clears its state.
func TestPermissionBridge_TimeoutDeniesAndCleansUp(t *testing.T) {
	inner := permission.NewPermissionService(t.TempDir(), false, nil)
	bridge := NewPermissionBridge(inner)
	bridge.SetRequestTimeout(40 * time.Millisecond) // short for the test

	tracker := NewActiveSessionTracker()
	tracker.SetSession("s-to", "member-2")
	bridge.SetActiveSessionTracker(tracker)

	ac := actor.ActorContext{
		SessionID: "s-to", TeamID: "team-1", MemberID: "member-2",
	}
	ctx := ac.WithContext(t.Context())

	start := time.Now()
	allowed, err := bridge.Request(ctx, permission.CreatePermissionRequest{
		SessionID: "s-to", ToolCallID: "tc-to-1", ToolName: "bash",
		Action: "run", Description: "test", Path: ".",
	})
	elapsed := time.Since(start)

	require.NoError(t, err)
	assert.False(t, allowed, "timeout should deny")
	assert.GreaterOrEqual(t, elapsed, 30*time.Millisecond, "should wait for the timeout")

	_, ok := bridge.TeamContextFor("tc-to-1")
	assert.False(t, ok, "team context should be cleared after timeout")

	// A late ResolveRequest must not panic and must report not-pending.
	err = bridge.ResolveRequest("tc-to-1", true, "call")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "not pending")
}

// TestPermissionBridge_CtxCancelDeniesAndCleansUp verifies that cancelling the
// request context returns promptly and clears state.
func TestPermissionBridge_CtxCancelDeniesAndCleansUp(t *testing.T) {
	inner := permission.NewPermissionService(t.TempDir(), false, nil)
	bridge := NewPermissionBridge(inner)
	bridge.SetRequestTimeout(5 * time.Second)

	tracker := NewActiveSessionTracker()
	tracker.SetSession("s-cc", "member-3")
	bridge.SetActiveSessionTracker(tracker)

	ctx, cancel := context.WithCancel(context.Background())
	ac := actor.ActorContext{
		SessionID: "s-cc", TeamID: "team-1", MemberID: "member-3",
	}
	ctx = ac.WithContext(ctx)

	resCh := make(chan error, 1)
	go func() {
		_, err := bridge.Request(ctx, permission.CreatePermissionRequest{
			SessionID: "s-cc", ToolCallID: "tc-cc-1", ToolName: "bash",
			Action: "run", Description: "test", Path: ".",
		})
		resCh <- err
	}()

	// Wait for the context to be stored, then cancel.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if _, ok := bridge.TeamContextFor("tc-cc-1"); ok {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	cancel()

	select {
	case err := <-resCh:
		require.Error(t, err)
		assert.ErrorIs(t, err, context.Canceled)
	case <-time.After(2 * time.Second):
		t.Fatal("Request did not return after cancel")
	}

	_, ok := bridge.TeamContextFor("tc-cc-1")
	assert.False(t, ok, "team context should be cleared after cancel")
}
