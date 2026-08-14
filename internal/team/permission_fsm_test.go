package team

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func setupFSM(t *testing.T) (*PermissionFSM, *PermissionStore, *GrantStore) {
	t.Helper()
	ps := NewPermissionStore()
	gs := NewGrantStore()
	var auditEvents []PermAuditEvent
	auditFn := func(ctx context.Context, e PermAuditEvent) {
		auditEvents = append(auditEvents, e)
	}
	_ = auditEvents
	return NewPermissionFSM(ps, gs, auditFn), ps, gs
}

func TestFSM_Resolve_Allowed_CallScope(t *testing.T) {
	fsm, ps, gs := setupFSM(t)
	ctx := context.Background()
	req := &PermissionRequest{
		ID: "r1", TeamID: "t1", MemberID: "m1", SessionID: "s1", RunID: "run1",
		ToolName: "bash", Action: "execute", ResourceRef: "/tmp/test",
		Status: "pending", RequestedScope: "call", CreatedAt: time.Now(),
	}
	require.NoError(t, ps.CreateRequest(ctx, req))

	err := fsm.Resolve(ctx, ResolveRequest{RequestID: "r1", Decision: "allowed", Scope: "call", DecidedBy: "user"})
	require.NoError(t, err)

	got, _ := ps.GetRequest(ctx, "r1")
	assert.Equal(t, "allowed", got.Status)
	assert.Equal(t, "call", got.DecisionScope)

	_, ok := gs.FindActiveGrant(ctx, "s1", "", "bash", "execute")
	assert.False(t, ok, "allow-once must not create a grant")
}

func TestFSM_Resolve_Denied(t *testing.T) {
	fsm, ps, _ := setupFSM(t)
	ctx := context.Background()
	req := &PermissionRequest{
		ID: "r2", TeamID: "t1", MemberID: "m1", RunID: "run1",
		ToolName: "write", Action: "write", Status: "pending",
		CreatedAt: time.Now(),
	}
	require.NoError(t, ps.CreateRequest(ctx, req))

	err := fsm.Resolve(ctx, ResolveRequest{RequestID: "r2", Decision: "denied", DecidedBy: "user"})
	require.NoError(t, err)

	got, _ := ps.GetRequest(ctx, "r2")
	assert.Equal(t, "denied", got.Status)
}

func TestFSM_Resolve_AlreadyResolved(t *testing.T) {
	fsm, ps, _ := setupFSM(t)
	ctx := context.Background()
	req := &PermissionRequest{
		ID: "r3", TeamID: "t1", MemberID: "m1", Status: "allowed",
		CreatedAt: time.Now(),
	}
	ps.CreateRequest(ctx, req)

	err := fsm.Resolve(ctx, ResolveRequest{RequestID: "r3", Decision: "allowed", Scope: "call"})
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "not pending")
}

func TestFSM_Expire(t *testing.T) {
	fsm, ps, _ := setupFSM(t)
	ctx := context.Background()
	req := &PermissionRequest{
		ID: "r4", TeamID: "t1", MemberID: "m1", Status: "pending", CreatedAt: time.Now(),
	}
	ps.CreateRequest(ctx, req)

	err := fsm.Expire(ctx, "r4")
	require.NoError(t, err)

	got, _ := ps.GetRequest(ctx, "r4")
	assert.Equal(t, "expired", got.Status)
}

func TestFSM_SetAuditFunc_Propagates(t *testing.T) {
	ps := NewPermissionStore()
	gs := NewGrantStore()
	fsm := NewPermissionFSM(ps, gs, func(ctx context.Context, e PermAuditEvent) {})

	var mu sync.Mutex
	var events []PermAuditEvent
	fsm.SetAuditFunc(func(ctx context.Context, e PermAuditEvent) {
		mu.Lock()
		defer mu.Unlock()
		events = append(events, e)
	})

	ctx := context.Background()
	require.NoError(t, ps.CreateRequest(ctx, &PermissionRequest{
		ID: "saf1", TeamID: "t1", MemberID: "m1", SessionID: "s1",
		ToolName: "bash", Action: "execute", Status: "pending", CreatedAt: time.Now(),
	}))
	require.NoError(t, fsm.Expire(ctx, "saf1"))

	mu.Lock()
	defer mu.Unlock()
	require.Len(t, events, 1)
	assert.Equal(t, PermAuditPermissionExpired, events[0].Action)
}

func TestFSM_CancelRequest_Pending(t *testing.T) {
	fsm, ps, _ := setupFSM(t)
	ctx := context.Background()
	require.NoError(t, ps.CreateRequest(ctx, &PermissionRequest{
		ID: "cr1", TeamID: "t1", MemberID: "m1", Status: "pending", CreatedAt: time.Now(),
	}))
	require.NoError(t, fsm.CancelRequest(ctx, "cr1"))
	got, _ := ps.GetRequest(ctx, "cr1")
	assert.Equal(t, "canceled", got.Status)
}

func TestFSM_CancelRequest_IdempotentAndNotFound(t *testing.T) {
	fsm, ps, _ := setupFSM(t)
	ctx := context.Background()
	require.NoError(t, ps.CreateRequest(ctx, &PermissionRequest{
		ID: "cr2", Status: "allowed", CreatedAt: time.Now(),
	}))
	require.NoError(t, fsm.CancelRequest(ctx, "cr2"), "non-pending must be an idempotent no-op")
	require.Error(t, fsm.CancelRequest(ctx, "nope"), "unknown ID must error")
}

func TestPermissionFSM_Resolve_Concurrent(t *testing.T) {
	ps := NewPermissionStore()
	gs := NewGrantStore()

	var mu sync.Mutex
	var events []PermAuditEvent
	auditFn := func(ctx context.Context, e PermAuditEvent) {
		mu.Lock()
		defer mu.Unlock()
		events = append(events, e)
	}
	fsm := NewPermissionFSM(ps, gs, auditFn)

	ctx := context.Background()
	require.NoError(t, ps.CreateRequest(ctx, &PermissionRequest{
		ID: "cc1", TeamID: "t1", MemberID: "m1", SessionID: "s1", RunID: "run1",
		ToolName: "bash", Action: "execute", Status: "pending", CreatedAt: time.Now(),
	}))

	const n = 16
	var wg sync.WaitGroup
	start := make(chan struct{})
	for i := range n {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			decision := "allowed"
			if i%2 == 1 {
				decision = "denied"
			}
			_ = fsm.Resolve(ctx, ResolveRequest{
				RequestID: "cc1", Decision: decision, Scope: "call", DecidedBy: "user",
			})
		}(i)
	}
	close(start)
	wg.Wait()

	mu.Lock()
	defer mu.Unlock()
	assert.Len(t, events, 1, "exactly one decision must win and be audited")

	got, err := ps.GetRequest(ctx, "cc1")
	require.NoError(t, err)
	assert.Contains(t, []string{"allowed", "denied"}, got.Status)

	gs.mu.RLock()
	grantCount := len(gs.grants)
	gs.mu.RUnlock()
	assert.Equal(t, 0, grantCount, "allow-once must not create a grant")
}
