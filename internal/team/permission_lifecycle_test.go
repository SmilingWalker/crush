package team

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/charmbracelet/crush/internal/actor"
	"github.com/charmbracelet/crush/internal/permission"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// auditCapture collects PermAuditEvents for lifecycle assertions.
type auditCapture struct {
	mu     sync.Mutex
	events []PermAuditEvent
}

func (c *auditCapture) record(ctx context.Context, e PermAuditEvent) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.events = append(c.events, e)
}

func (c *auditCapture) actions() []PermAuditAction {
	c.mu.Lock()
	defer c.mu.Unlock()
	actions := make([]PermAuditAction, len(c.events))
	for i, e := range c.events {
		actions[i] = e.Action
	}
	return actions
}

// startTeamRequest launches bridge.Request on a team actor and returns the
// decision channel. The display timer must be long (or the ctx cancelled) to
// end it.
func startTeamRequest(bridge *PermissionBridge, ctx context.Context, reqID string) <-chan bool {
	ac := actor.ActorContext{
		SessionID: "s-lc", TeamID: "team-lc", MemberID: "m-lc", TaskID: "task-lc", RunID: "run-lc",
		MemberName: "m", MemberRole: "programmer",
	}
	res := make(chan bool, 1)
	go func() {
		allowed, _ := bridge.Request(ac.WithContext(ctx), permission.CreatePermissionRequest{
			SessionID: "s-lc", ToolCallID: reqID, ToolName: "write",
			Action: "write", Description: "test", Path: "/tmp/lc",
		})
		res <- allowed
	}()
	return res
}

// waitRegistered blocks until the request's entry is registered.
func waitRegistered(t *testing.T, bridge *PermissionBridge, reqID string) {
	t.Helper()
	require.Eventually(t, func() bool {
		_, ok := bridge.TeamContextFor(reqID)
		return ok
	}, 2*time.Second, 10*time.Millisecond, "entry must register")
}

func newLifecycleBridge(t *testing.T) (*PermissionBridge, *auditCapture) {
	t.Helper()
	inner := permission.NewPermissionService(t.TempDir(), false, nil)
	bridge := NewPermissionBridge("default", inner)
	bridge.SetRequestTimeout(5 * time.Second)
	capt := &auditCapture{}
	bridge.SetAuditFunc(capt.record)
	return bridge, capt
}

func grantCount(bridge *PermissionBridge) int {
	bridge.grantStore.mu.RLock()
	defer bridge.grantStore.mu.RUnlock()
	return len(bridge.grantStore.grants)
}

func TestM5_UserAllows_Call_NoGrant(t *testing.T) {
	bridge, capt := newLifecycleBridge(t)
	res := startTeamRequest(bridge, t.Context(), "lc-allow")
	waitRegistered(t, bridge, "lc-allow")

	require.NoError(t, bridge.ResolveRequest("lc-allow", true, "call"))

	select {
	case allowed := <-res:
		assert.True(t, allowed)
	case <-time.After(2 * time.Second):
		t.Fatal("request did not return")
	}

	got, err := bridge.store.GetRequest(context.Background(), "lc-allow")
	require.NoError(t, err)
	assert.Equal(t, "allowed", got.Status)
	assert.Equal(t, "call", got.DecisionScope)
	assert.Equal(t, "user", got.DecidedBy)
	assert.Equal(t, 0, grantCount(bridge), "allow-once must not create a grant")
	assert.Contains(t, capt.actions(), PermAuditPermissionAllowed)
}

func TestM5_UserDenies_PersistsDenied(t *testing.T) {
	bridge, capt := newLifecycleBridge(t)
	res := startTeamRequest(bridge, t.Context(), "lc-deny")
	waitRegistered(t, bridge, "lc-deny")

	require.NoError(t, bridge.ResolveRequest("lc-deny", false, "call"))

	select {
	case allowed := <-res:
		assert.False(t, allowed)
	case <-time.After(2 * time.Second):
		t.Fatal("request did not return")
	}

	got, _ := bridge.store.GetRequest(context.Background(), "lc-deny")
	assert.Equal(t, "denied", got.Status)
	assert.Contains(t, capt.actions(), PermAuditPermissionDenied)
}

func TestM5_AllowTaskScope_CreatesGrant(t *testing.T) {
	bridge, _ := newLifecycleBridge(t)
	res := startTeamRequest(bridge, t.Context(), "lc-task")
	waitRegistered(t, bridge, "lc-task")

	require.NoError(t, bridge.ResolveRequest("lc-task", true, "task"))
	<-res

	bridge.grantStore.mu.RLock()
	var taskGrant *Grant
	for _, g := range bridge.grantStore.grants {
		if g.SourceRequestID == "lc-task" {
			taskGrant = g
		}
	}
	bridge.grantStore.mu.RUnlock()
	require.NotNil(t, taskGrant, "task-scope allow must create a grant")
	assert.Equal(t, "task", taskGrant.Scope)
	assert.Equal(t, "task-lc", taskGrant.TaskID)
}

func TestM5_TimeoutExpires(t *testing.T) {
	bridge, capt := newLifecycleBridge(t)
	bridge.SetRequestTimeout(50 * time.Millisecond)
	res := startTeamRequest(bridge, t.Context(), "lc-exp")
	waitRegistered(t, bridge, "lc-exp")

	select {
	case allowed := <-res:
		assert.False(t, allowed)
	case <-time.After(2 * time.Second):
		t.Fatal("request did not time out")
	}

	got, err := bridge.store.GetRequest(context.Background(), "lc-exp")
	require.NoError(t, err)
	assert.Equal(t, "expired", got.Status)
	assert.Contains(t, capt.actions(), PermAuditPermissionExpired)
}

func TestM5_CtxCancelMarksCanceled(t *testing.T) {
	bridge, capt := newLifecycleBridge(t)
	ctx, cancel := context.WithCancel(t.Context())
	res := startTeamRequest(bridge, ctx, "lc-cancel")
	waitRegistered(t, bridge, "lc-cancel")

	cancel()

	select {
	case allowed := <-res:
		assert.False(t, allowed)
	case <-time.After(2 * time.Second):
		t.Fatal("request did not return after cancel")
	}

	got, err := bridge.store.GetRequest(context.Background(), "lc-cancel")
	require.NoError(t, err)
	assert.Equal(t, "canceled", got.Status)
	assert.Contains(t, capt.actions(), PermAuditPermissionCanceled)
}

func TestM5_LateAllowAfterTTLExpiry(t *testing.T) {
	bridge, capt := newLifecycleBridge(t)
	bridge.GetQueue().WithLimits(3, 80*time.Millisecond) // FSM TTL well under the 5s display timer
	res := startTeamRequest(bridge, t.Context(), "lc-late")
	waitRegistered(t, bridge, "lc-late")

	// TTL fires first: the store row leaves pending while the entry survives.
	require.Eventually(t, func() bool {
		got, err := bridge.store.GetRequest(context.Background(), "lc-late")
		return err == nil && got.Status == "expired"
	}, 2*time.Second, 10*time.Millisecond, "TTL must expire the store row")

	require.NoError(t, bridge.ResolveRequest("lc-late", true, "call"))

	select {
	case allowed := <-res:
		assert.False(t, allowed, "late decision must deny")
	case <-time.After(2 * time.Second):
		t.Fatal("request did not return")
	}
	assert.Contains(t, capt.actions(), PermAuditLateResponse)
	assert.Equal(t, 0, grantCount(bridge), "late decision must not create a grant")
}
