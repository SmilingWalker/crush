package team

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestM5_PermissionFlow_RequestResolve verifies the full permission flow:
// create request -> resolve allowed -> grant created.
func TestM5_PermissionFlow_RequestResolve(t *testing.T) {
	ps := NewPermissionStore()
	gs := NewGrantStore()
	var auditEvents []PermAuditEvent
	auditFn := func(ctx context.Context, e PermAuditEvent) {
		auditEvents = append(auditEvents, e)
	}
	fsm := NewPermissionFSM(ps, gs, auditFn)
	ctx := context.Background()

	// Create a pending request.
	req := &PermissionRequest{
		ID: "e2e-r1", TeamID: "t1", MemberID: "m1", RunID: "run1",
		ToolName: "bash", Action: "execute", ResourceRef: "go test",
		Status: "pending", RequestedScope: "call",
		CreatedAt: time.Now(),
	}
	require.NoError(t, ps.CreateRequest(ctx, req))

	// Resolve: allowed.
	err := fsm.Resolve(ctx, ResolveRequest{
		RequestID: "e2e-r1", Decision: "allowed", Scope: "task", DecidedBy: "user",
	})
	require.NoError(t, err)

	// Verify request updated.
	got, _ := ps.GetRequest(ctx, "e2e-r1")
	assert.Equal(t, "allowed", got.Status)
	assert.Equal(t, "task", got.DecisionScope)

	// Verify grant created.
	grant, ok := gs.FindActiveGrant(ctx, "m1", "bash", "execute")
	assert.True(t, ok)
	assert.Equal(t, "task", grant.Scope)

	// Verify audit event recorded.
	assert.Equal(t, 1, len(auditEvents))
	assert.Equal(t, PermAuditPermissionAllowed, auditEvents[0].Action)
}

// TestM5_PermissionFlow_Deny verifies the deny path: no grant created.
func TestM5_PermissionFlow_Deny(t *testing.T) {
	ps := NewPermissionStore()
	gs := NewGrantStore()
	var auditEvents []PermAuditEvent
	auditFn := func(ctx context.Context, e PermAuditEvent) {
		auditEvents = append(auditEvents, e)
	}
	fsm := NewPermissionFSM(ps, gs, auditFn)
	ctx := context.Background()

	req := &PermissionRequest{
		ID: "e2e-r2", TeamID: "t1", MemberID: "m1", RunID: "run1",
		ToolName: "write", Action: "write", Status: "pending",
		CreatedAt: time.Now(),
	}
	ps.CreateRequest(ctx, req)

	err := fsm.Resolve(ctx, ResolveRequest{
		RequestID: "e2e-r2", Decision: "denied", DecidedBy: "user",
	})
	require.NoError(t, err)

	got, _ := ps.GetRequest(ctx, "e2e-r2")
	assert.Equal(t, "denied", got.Status)

	// No grant created for deny.
	_, ok := gs.FindActiveGrant(ctx, "m1", "write", "write")
	assert.False(t, ok)

	assert.Equal(t, 1, len(auditEvents))
	assert.Equal(t, PermAuditPermissionDenied, auditEvents[0].Action)
}

// TestM5_QueueExpiry_E2E verifies that queued requests auto-expire via the
// queue's expiry timer.
func TestM5_QueueExpiry_E2E(t *testing.T) {
	ps := NewPermissionStore()
	gs := NewGrantStore()
	auditFn := func(ctx context.Context, e PermAuditEvent) {}
	fsm := NewPermissionFSM(ps, gs, auditFn)
	q := NewPermissionQueue(fsm).WithLimits(3, 50*time.Millisecond)
	ctx := context.Background()

	req := &PermissionRequest{
		ID: "e2e-expire", TeamID: "t1", MemberID: "m1",
		Status: "pending", CreatedAt: time.Now(),
	}
	ps.CreateRequest(ctx, req)
	require.NoError(t, q.Enqueue(ctx, req))

	time.Sleep(150 * time.Millisecond)

	got, _ := ps.GetRequest(ctx, "e2e-expire")
	assert.Equal(t, "expired", got.Status)
	assert.Equal(t, 0, q.PendingCount())
}

// TestM5_FullFlow_RequestQueueResolve verifies the full lifecycle:
// create request -> enqueue -> resolve -> dequeue -> grant persists.
func TestM5_FullFlow_RequestQueueResolve(t *testing.T) {
	ps := NewPermissionStore()
	gs := NewGrantStore()
	var auditEvents []PermAuditEvent
	auditFn := func(ctx context.Context, e PermAuditEvent) {
		auditEvents = append(auditEvents, e)
	}
	fsm := NewPermissionFSM(ps, gs, auditFn)
	q := NewPermissionQueue(fsm)
	ctx := context.Background()

	req := &PermissionRequest{
		ID: "e2e-full", TeamID: "t1", MemberID: "m2", RunID: "run2",
		ToolName: "edit", Action: "write", ResourceRef: "main.go",
		Status: "pending", RequestedScope: "call",
		CreatedAt: time.Now(),
	}
	require.NoError(t, ps.CreateRequest(ctx, req))
	require.NoError(t, q.Enqueue(ctx, req))
	assert.Equal(t, 1, q.PendingCount())

	// Resolve.
	err := fsm.Resolve(ctx, ResolveRequest{
		RequestID: "e2e-full", Decision: "allowed", Scope: "session", DecidedBy: "user",
	})
	require.NoError(t, err)

	// Dequeue after resolution.
	q.Dequeue("e2e-full")
	assert.Equal(t, 0, q.PendingCount())

	// Grant exists with session scope.
	grant, ok := gs.FindActiveGrant(ctx, "m2", "edit", "write")
	assert.True(t, ok)
	assert.Equal(t, "session", grant.Scope)

	// Audit: permission_requested (not recorded by enqueue alone in current impl),
	// permission_allowed (from fsm.Resolve).
	assert.Equal(t, 1, len(auditEvents))
	assert.Equal(t, PermAuditPermissionAllowed, auditEvents[0].Action)
}
