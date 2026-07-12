package team

import (
	"context"
	"io"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/charmbracelet/crush/internal/actor"
	"github.com/charmbracelet/crush/internal/permission"
	"github.com/charmbracelet/crush/internal/pubsub"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// newSyncWriter returns an io.Writer that appends each Write to *lines under mu.
func newSyncWriter(mu *sync.Mutex, lines *[]string) io.Writer {
	return &syncWriter{mu: mu, lines: lines}
}

type syncWriter struct {
	mu    *sync.Mutex
	lines *[]string
}

func (w *syncWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	*w.lines = append(*w.lines, string(p))
	return len(p), nil
}

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
	bridge := NewPermissionBridge("default", nil)
	assert.NotNil(t, bridge)
	assert.NotNil(t, bridge.store)
	assert.NotNil(t, bridge.grantStore)
	assert.NotNil(t, bridge.auditFn)
	assert.NotNil(t, bridge.pendingRequests)
}

func TestPermissionBridge_SetAuditFunc(t *testing.T) {
	bridge := NewPermissionBridge("default", nil)
	called := false
	bridge.SetAuditFunc(func(ctx context.Context, event PermAuditEvent) {
		called = true
	})
	// Verify the function was set by calling it
	bridge.auditFn(context.Background(), PermAuditEvent{})
	assert.True(t, called)
}

func TestPermissionBridge_ResolveRequest_NotFound(t *testing.T) {
	bridge := NewPermissionBridge("default", nil)
	err := bridge.ResolveRequest("nonexistent", true, "call")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "not pending")
}

// TestPermissionBridge_AutoAllowWhenSkipRequests verifies that team sessions
// auto-allow tool calls when SkipRequests (yolo mode) is enabled.
func TestPermissionBridge_AutoAllowWhenSkipRequests(t *testing.T) {
	inner := permission.NewPermissionService(t.TempDir(), true, nil) // skip=true
	bridge := NewPermissionBridge("default", inner)

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

// TestPermissionBridge_TeamSessionShowsPopupWhenSkipOff verifies that a team
// session with SkipRequests=false routes through requestWithUI (shows a popup)
// rather than auto-denying. Resolve from the UI returns the decision.
func TestPermissionBridge_TeamSessionShowsPopupWhenSkipOff(t *testing.T) {
	inner := permission.NewPermissionService(t.TempDir(), false, nil) // skip=false
	bridge := NewPermissionBridge("default", inner)
	bridge.SetRequestTimeout(5 * time.Second)

	ac := actor.ActorContext{SessionID: "s1", TeamID: "team-1", MemberID: "member-1"}
	ctx := ac.WithContext(t.Context())

	resCh := make(chan bool, 1)
	go func() {
		allowed, _ := bridge.Request(ctx, permission.CreatePermissionRequest{
			SessionID: "s1", ToolCallID: "tc-popup", ToolName: "bash",
			Action: "run", Description: "test", Path: ".",
		})
		resCh <- allowed
	}()

	// Wait for the request to reach requestWithUI.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if _, ok := bridge.TeamContextFor("tc-popup"); ok {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	_, visible := bridge.TeamContextFor("tc-popup")
	require.True(t, visible, "should route to requestWithUI (popup) when SkipRequests=false")

	require.NoError(t, bridge.ResolveRequest("tc-popup", false, "call"))
	select {
	case allowed := <-resCh:
		assert.False(t, allowed, "UI deny should return false")
	case <-time.After(2 * time.Second):
		t.Fatal("Request did not return")
	}
}

// TestPermissionBridge_AlwaysShowNotActiveSession verifies that a team member's
// request routes through requestWithUI (and can be resolved by the UI) even when
// the active-session tracker points at a DIFFERENT member. The IsActiveSession
// gate is gone.
func TestPermissionBridge_AlwaysShowNotActiveSession(t *testing.T) {
	inner := permission.NewPermissionService(t.TempDir(), false, nil)
	bridge := NewPermissionBridge("default", inner)
	bridge.SetRequestTimeout(5 * time.Second)

	// Active session is a DIFFERENT member than the one making the request.
	tracker := NewActiveSessionTracker()
	tracker.SetSession("s-other", "member-other")
	bridge.SetActiveSessionTracker(tracker)

	ac := actor.ActorContext{
		SessionID: "s-ask", TeamID: "team-1", MemberID: "member-ask",
		MemberName: "asker", MemberRole: "programmer",
	}
	ctx := ac.WithContext(t.Context())

	resCh := make(chan bool, 1)
	go func() {
		allowed, _ := bridge.Request(ctx, permission.CreatePermissionRequest{
			SessionID: "s-ask", ToolCallID: "tc-always", ToolName: "write",
			Action: "write", Description: "test", Path: "/tmp/x",
		})
		resCh <- allowed
	}()

	// The request must reach requestWithUI: TeamContextFor becomes available.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if _, ok := bridge.TeamContextFor("tc-always"); ok {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	_, visible := bridge.TeamContextFor("tc-always")
	require.True(t, visible, "request should route to requestWithUI despite non-matching active session")

	require.NoError(t, bridge.ResolveRequest("tc-always", true, "call"))
	select {
	case allowed := <-resCh:
		assert.True(t, allowed)
	case <-time.After(2 * time.Second):
		t.Fatal("Request did not return")
	}
}

// TestPermissionBridge_PublishDelegatesToInner verifies that Publish on the
// bridge delegates to inner.Publish, enabling team permission requests to
// reach the UI event stream.
func TestPermissionBridge_PublishDelegatesToInner(t *testing.T) {
	inner := permission.NewPermissionService(t.TempDir(), true, nil)
	bridge := NewPermissionBridge("default", inner)
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
	bridge := NewPermissionBridge("default", inner)
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
	bridge := NewPermissionBridge("default", nil)
	_, ok := bridge.TeamContextFor("nope")
	assert.False(t, ok)
}

// TestPermissionBridge_TimeoutDeniesAndCleansUp verifies that an unresolved
// request denies after the bridge-local timeout and clears its state.
func TestPermissionBridge_TimeoutDeniesAndCleansUp(t *testing.T) {
	inner := permission.NewPermissionService(t.TempDir(), false, nil)
	bridge := NewPermissionBridge("default", inner)
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
	bridge := NewPermissionBridge("default", inner)
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

// TestPermissionBridge_TraceLogging verifies the permission decision trace is
// emitted at slog.Debug level for the full member round-trip.
func TestPermissionBridge_TraceLogging(t *testing.T) {
	// Capture slog output via an in-memory handler at Debug level.
	var mu sync.Mutex
	var lines []string
	h := slog.NewTextHandler(newSyncWriter(&mu, &lines), &slog.HandlerOptions{Level: slog.LevelDebug})
	prev := slog.Default()
	slog.SetDefault(slog.New(h))
	t.Cleanup(func() { slog.SetDefault(prev) })

	inner := permission.NewPermissionService(t.TempDir(), false, nil)
	bridge := NewPermissionBridge("default", inner)
	bridge.SetRequestTimeout(5 * time.Second)

	tracker := NewActiveSessionTracker()
	tracker.SetSession("s-trace", "member-trace")
	bridge.SetActiveSessionTracker(tracker)

	ac := actor.ActorContext{
		SessionID: "s-trace", TeamID: "team-trace", MemberID: "member-trace",
		MemberName: "tracer", MemberRole: "programmer",
	}
	ctx := ac.WithContext(t.Context())

	resCh := make(chan error, 1)
	go func() {
		_, err := bridge.Request(ctx, permission.CreatePermissionRequest{
			SessionID: "s-trace", ToolCallID: "tc-trace", ToolName: "write",
			Action: "write", Description: "test", Path: "/tmp/x",
		})
		resCh <- err
	}()

	// Wait for the context to be stored, then resolve.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if _, ok := bridge.TeamContextFor("tc-trace"); ok {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	require.NoError(t, bridge.ResolveRequest("tc-trace", true, "call"))
	select {
	case <-resCh:
	case <-time.After(2 * time.Second):
		t.Fatal("Request did not return")
	}

	mu.Lock()
	defer mu.Unlock()
	joined := strings.Join(lines, "\n")
	for _, want := range []string{
		"perm_bridge: request",
		"perm_bridge: team → requestWithUI",
		"perm_bridge: requestWithUI enqueued",
		"perm_bridge: pumpDisplay published",
		"perm_bridge: ResolveRequest",
		"perm_bridge: request resolved by UI",
	} {
		require.Contains(t, joined, want, "missing trace line: %s", want)
	}
}

// TestPermissionBridge_SequentialQueue verifies that two concurrent member
// requests are presented one at a time: only the first is Published to the UI
// until resolved, then the second is Published. "Displayed" is observed via the
// pubsub event stream (the TUI only opens a dialog on a Publish event).
func TestPermissionBridge_SequentialQueue(t *testing.T) {
	inner := permission.NewPermissionService(t.TempDir(), false, nil)
	bridge := NewPermissionBridge("default", inner)
	bridge.SetRequestTimeout(5 * time.Second)

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	events := inner.Subscribe(ctx)

	// nextEvent returns the next published request ID, or "" on timeout.
	nextEvent := func(timeout time.Duration) string {
		select {
		case ev := <-events:
			return ev.Payload.ID
		case <-time.After(timeout):
			return ""
		}
	}

	mkReq := func(reqID, member string) <-chan bool {
		ac := actor.ActorContext{
			SessionID: "s-q", TeamID: "team-q", MemberID: member,
			MemberName: member, MemberRole: "programmer",
		}
		rctx := ac.WithContext(t.Context())
		resCh := make(chan bool, 1)
		go func() {
			allowed, _ := bridge.Request(rctx, permission.CreatePermissionRequest{
				SessionID: "s-q", ToolCallID: reqID, ToolName: "write",
				Action: "write", Description: "test", Path: "/tmp/x",
			})
			resCh <- allowed
		}()
		return resCh
	}

	r1 := mkReq("tc-q1", "m1")

	// First should be Published promptly.
	require.Equal(t, "tc-q1", nextEvent(2*time.Second), "first request should be displayed")

	// Now enqueue the second while the first is still displayed.
	r2 := mkReq("tc-q2", "m2")

	// Second must NOT be Published yet (still waiting in FIFO). Give it a moment
	// to (incorrectly) publish, then assert silence.
	assert.Equal(t, "", nextEvent(150*time.Millisecond), "second request should wait, not be displayed yet")

	// Resolve the first — second should be Published next.
	require.NoError(t, bridge.ResolveRequest("tc-q1", true, "call"))
	select {
	case <-r1:
	case <-time.After(2 * time.Second):
		t.Fatal("first request did not return")
	}

	require.Equal(t, "tc-q2", nextEvent(2*time.Second), "second request should be displayed after first resolved")

	require.NoError(t, bridge.ResolveRequest("tc-q2", true, "call"))
	select {
	case <-r2:
	case <-time.After(2 * time.Second):
		t.Fatal("second request did not return")
	}
}

// TestPermissionBridge_FairTimeout verifies the display timer starts at DISPLAY,
// not at enqueue. Two requests queued; the displayed one times out and advances
// the queue; the second (which was waiting, no timer) then becomes displayable
// and can be resolved normally — proving it did not expire while waiting.
func TestPermissionBridge_FairTimeout(t *testing.T) {
	inner := permission.NewPermissionService(t.TempDir(), false, nil)
	bridge := NewPermissionBridge("default", inner)
	bridge.SetRequestTimeout(200 * time.Millisecond) // short display timer

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	events := inner.Subscribe(ctx)
	nextEvent := func(timeout time.Duration) string {
		select {
		case ev := <-events:
			return ev.Payload.ID
		case <-time.After(timeout):
			return ""
		}
	}

	mkReq := func(reqID string) <-chan bool {
		ac := actor.ActorContext{
			SessionID: "s-ft", TeamID: "team-ft", MemberID: "m-ft",
			MemberName: "m", MemberRole: "programmer",
		}
		rctx := ac.WithContext(t.Context())
		resCh := make(chan bool, 1)
		go func() {
			allowed, _ := bridge.Request(rctx, permission.CreatePermissionRequest{
				SessionID: "s-ft", ToolCallID: reqID, ToolName: "write",
				Action: "write", Description: "test", Path: "/tmp/x",
			})
			resCh <- allowed
		}()
		return resCh
	}

	r1 := mkReq("tc-ft1")
	require.Equal(t, "tc-ft1", nextEvent(2*time.Second), "first should be displayed")

	// Enqueue the second while the first is displayed (waiting, no timer).
	r2 := mkReq("tc-ft2")
	// Wait window is well under the 200ms timer, so the first is still displayed
	// and the second must still be waiting (not yet published).
	assert.Equal(t, "", nextEvent(50*time.Millisecond), "second should wait")

	// First times out (denied) after ~80ms. Wait notably longer than the timer
	// so the timeout surely fired and advanced the queue.
	select {
	case allowed := <-r1:
		assert.False(t, allowed, "first (displayed) should time out and deny")
	case <-time.After(2 * time.Second):
		t.Fatal("first request did not time out")
	}

	// After the first times out, the second is promoted to displayed and can be
	// resolved by the UI — proving it did not expire while waiting.
	require.Equal(t, "tc-ft2", nextEvent(2*time.Second), "second should be promoted after first timeout")
	require.NoError(t, bridge.ResolveRequest("tc-ft2", true, "call"))
	select {
	case allowed := <-r2:
		assert.True(t, allowed, "second should resolve (no premature timeout while waiting)")
	case <-time.After(2 * time.Second):
		t.Fatal("second request did not return")
	}
}

// TestPermissionBridge_LateResolveNoOp verifies that resolving an already-
// timed-out request returns an error and does not panic.
func TestPermissionBridge_LateResolveNoOp(t *testing.T) {
	inner := permission.NewPermissionService(t.TempDir(), false, nil)
	bridge := NewPermissionBridge("default", inner)
	bridge.SetRequestTimeout(40 * time.Millisecond)

	ac := actor.ActorContext{
		SessionID: "s-late", TeamID: "team-l", MemberID: "m-l",
		MemberName: "m", MemberRole: "programmer",
	}
	ctx := ac.WithContext(t.Context())

	done := make(chan bool, 1)
	go func() {
		allowed, _ := bridge.Request(ctx, permission.CreatePermissionRequest{
			SessionID: "s-late", ToolCallID: "tc-late", ToolName: "write",
			Action: "write", Description: "test", Path: "/tmp/x",
		})
		done <- allowed
	}()

	// Wait for timeout.
	select {
	case allowed := <-done:
		assert.False(t, allowed)
	case <-time.After(2 * time.Second):
		t.Fatal("request did not time out")
	}

	// Late resolve: error, no panic.
	err := bridge.ResolveRequest("tc-late", true, "call")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not pending")
}

// TestPermissionBridge_CtxCancelAdvancesQueue verifies that cancelling a
// displayed request's context removes it and promotes the next waiting request.
func TestPermissionBridge_CtxCancelAdvancesQueue(t *testing.T) {
	inner := permission.NewPermissionService(t.TempDir(), false, nil)
	bridge := NewPermissionBridge("default", inner)
	bridge.SetRequestTimeout(5 * time.Second)

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	events := inner.Subscribe(ctx)
	nextEvent := func(timeout time.Duration) string {
		select {
		case ev := <-events:
			return ev.Payload.ID
		case <-time.After(timeout):
			return ""
		}
	}

	ctx1, cancel1 := context.WithCancel(t.Context())
	defer cancel1()
	ac1 := actor.ActorContext{SessionID: "s-c", TeamID: "team-c", MemberID: "m1",
		MemberName: "m1", MemberRole: "programmer"}
	ctx1 = ac1.WithContext(ctx1)

	r1 := make(chan error, 1)
	go func() {
		_, err := bridge.Request(ctx1, permission.CreatePermissionRequest{
			SessionID: "s-c", ToolCallID: "tc-c1", ToolName: "write",
			Action: "write", Description: "test", Path: "/tmp/x",
		})
		r1 <- err
	}()

	// Wait for first displayed.
	require.Equal(t, "tc-c1", nextEvent(2*time.Second), "first should be displayed")

	// Enqueue a second (waits in FIFO).
	ac2 := actor.ActorContext{SessionID: "s-c", TeamID: "team-c", MemberID: "m2",
		MemberName: "m2", MemberRole: "programmer"}
	ctx2 := ac2.WithContext(t.Context())
	r2 := make(chan bool, 1)
	go func() {
		allowed, _ := bridge.Request(ctx2, permission.CreatePermissionRequest{
			SessionID: "s-c", ToolCallID: "tc-c2", ToolName: "write",
			Action: "write", Description: "test", Path: "/tmp/x",
		})
		r2 <- allowed
	}()
	// Second should still be waiting.
	assert.Equal(t, "", nextEvent(50*time.Millisecond), "second should wait")

	cancel1()
	select {
	case err := <-r1:
		require.ErrorIs(t, err, context.Canceled)
	case <-time.After(2 * time.Second):
		t.Fatal("first did not return on cancel")
	}

	// Second should now be promoted to displayed.
	require.Equal(t, "tc-c2", nextEvent(2*time.Second), "second should be promoted after cancel")
	require.NoError(t, bridge.ResolveRequest("tc-c2", true, "call"))
	select {
	case <-r2:
	case <-time.After(2 * time.Second):
		t.Fatal("second did not return")
	}
}

func TestPermEventToAuditEvent_FullMapping(t *testing.T) {
	e := PermAuditEvent{
		WorkspaceID: "ws-1",
		SessionID:   "sess-1",
		ToolCallID:  "call-1",
		Action:      PermAuditPermissionAllowed,
		TeamID:      "team-1",
		MemberID:    "member-1",
		TaskID:      "task-1",
		RunID:       "run-1",
		ToolName:    "write",
		Decision:    "allowed",
		Scope:       "task",
		DecidedBy:   "user",
		Timestamp:   time.Date(2026, 7, 11, 12, 0, 0, 0, time.UTC),
	}

	got := PermEventToAuditEvent(e)

	require.Equal(t, "ws-1", got.WorkspaceID)
	require.Equal(t, "team-1", got.TeamID)
	require.Equal(t, "permission.permission_allowed", got.EventType)
	require.NotNil(t, got.Action)
	require.Equal(t, "permission_allowed", *got.Action)
	require.NotNil(t, got.MemberID)
	require.Equal(t, "member-1", *got.MemberID)
	require.NotNil(t, got.SessionID)
	require.Equal(t, "sess-1", *got.SessionID)
	require.NotNil(t, got.ToolCallID)
	require.Equal(t, "call-1", *got.ToolCallID)
	require.NotNil(t, got.ResourceType)
	require.Equal(t, "tool", *got.ResourceType)
	require.NotNil(t, got.ResourceRef)
	require.Equal(t, "write", *got.ResourceRef)
	require.NotNil(t, got.Decision)
	require.Equal(t, "allowed", *got.Decision)
	require.NotNil(t, got.Scope)
	require.Equal(t, "task", *got.Scope)
	require.NotNil(t, got.Summary)
	require.Equal(t, "decided_by:user", *got.Summary)
	require.Equal(t, e.Timestamp, got.CreatedAt)
	require.NotEmpty(t, got.ID)
}

func TestPermEventToAuditEvent_EmptyFieldsBecomeNil(t *testing.T) {
	e := PermAuditEvent{
		WorkspaceID: "ws-1",
		TeamID:      "team-1",
		Action:      PermAuditPermissionExpired,
		Timestamp:   time.Now(),
		// MemberID, SessionID, TaskID, etc. all empty
	}

	got := PermEventToAuditEvent(e)

	require.Nil(t, got.MemberID)
	require.Nil(t, got.SessionID)
	require.Nil(t, got.ToolCallID)
	require.Nil(t, got.TaskID)
	require.Nil(t, got.RunID)
	require.Nil(t, got.Summary)    // DecidedBy empty → Summary nil
	require.Nil(t, got.ResourceRef) // ToolName empty → ResourceRef nil
}
