package team

import (
	"context"
	"testing"
	"time"

	"github.com/charmbracelet/crush/internal/permission"
	"github.com/charmbracelet/crush/internal/pubsub"
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
		ID: "e2e-r1", TeamID: "t1", MemberID: "m1", SessionID: "s1", RunID: "run1",
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

	// Verify grant created (FindActiveGrant now matches on SessionID).
	grant, ok := gs.FindActiveGrant(ctx, "s1", "bash", "execute")
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
		ID: "e2e-r2", TeamID: "t1", MemberID: "m1", SessionID: "s2", RunID: "run1",
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

	// No grant created for deny (match on SessionID).
	_, ok := gs.FindActiveGrant(ctx, "s2", "write", "write")
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
		ID: "e2e-full", TeamID: "t1", MemberID: "m2", SessionID: "s3", RunID: "run2",
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

	// Grant exists with session scope (match on SessionID).
	grant, ok := gs.FindActiveGrant(ctx, "s3", "edit", "write")
	assert.True(t, ok)
	assert.Equal(t, "session", grant.Scope)

	// Audit: permission_requested (not recorded by enqueue alone in current impl),
	// permission_allowed (from fsm.Resolve).
	assert.Equal(t, 1, len(auditEvents))
	assert.Equal(t, PermAuditPermissionAllowed, auditEvents[0].Action)
}

// TestM5_Cancel_AllPendingForRun verifies that Cancel marks all pending
// requests for a run as canceled.
func TestM5_Cancel_AllPendingForRun(t *testing.T) {
	ps := NewPermissionStore()
	gs := NewGrantStore()
	var auditEvents []PermAuditEvent
	auditFn := func(ctx context.Context, e PermAuditEvent) {
		auditEvents = append(auditEvents, e)
	}
	fsm := NewPermissionFSM(ps, gs, auditFn)
	ctx := context.Background()

	// Create two pending requests for run1.
	r1 := &PermissionRequest{
		ID: "c1", RunID: "run1", TeamID: "t1", MemberID: "m1",
		ToolName: "bash", Status: "pending", CreatedAt: time.Now(),
	}
	r2 := &PermissionRequest{
		ID: "c2", RunID: "run1", TeamID: "t1", MemberID: "m1",
		ToolName: "write", Status: "pending", CreatedAt: time.Now(),
	}
	ps.CreateRequest(ctx, r1)
	ps.CreateRequest(ctx, r2)

	count, err := fsm.Cancel(ctx, "run1")
	require.NoError(t, err)
	assert.Equal(t, 2, count)

	// Both should be canceled.
	got1, _ := ps.GetRequest(ctx, "c1")
	assert.Equal(t, "canceled", got1.Status)
	got2, _ := ps.GetRequest(ctx, "c2")
	assert.Equal(t, "canceled", got2.Status)

	// Two audit events emitted.
	assert.Equal(t, 2, len(auditEvents))
}

// TestM5_Orphan_MarksMemberPendingAsOrphaned verifies that Orphan marks all
// pending requests for a member as orphaned.
func TestM5_Orphan_MarksMemberPendingAsOrphaned(t *testing.T) {
	ps := NewPermissionStore()
	gs := NewGrantStore()
	var auditEvents []PermAuditEvent
	auditFn := func(ctx context.Context, e PermAuditEvent) {
		auditEvents = append(auditEvents, e)
	}
	fsm := NewPermissionFSM(ps, gs, auditFn)
	ctx := context.Background()

	r1 := &PermissionRequest{
		ID: "o1", MemberID: "m1", TeamID: "t1", RunID: "run1",
		ToolName: "bash", Status: "pending", CreatedAt: time.Now(),
	}
	ps.CreateRequest(ctx, r1)

	count, err := fsm.Orphan(ctx, "m1")
	require.NoError(t, err)
	assert.Equal(t, 1, count)

	got, _ := ps.GetRequest(ctx, "o1")
	assert.Equal(t, "orphaned", got.Status)

	assert.Equal(t, 1, len(auditEvents))
	assert.Equal(t, PermAuditPermissionOrphaned, auditEvents[0].Action)
}

// TestM5_NonTeamSessionUnchanged verifies that non-team sessions pass through
// to inner unchanged. This tests the bridge stores the inner reference and
// delegates correctly when the actor context has no team.
func TestM5_NonTeamSessionUnchanged(t *testing.T) {
	inner := &stubPermissionService{}
	bridge := NewPermissionBridge(inner)
	assert.NotNil(t, bridge.inner)

	// When actor context has no team, Request delegates to inner.
	ctx := context.Background()
	opts := permission.CreatePermissionRequest{
		SessionID: "s1", ToolName: "bash", Action: "execute",
	}
	allowed, err := bridge.Request(ctx, opts)
	require.NoError(t, err)
	assert.True(t, allowed)
	assert.True(t, inner.requestCalled)

	// Delegation methods pass through to inner.
	perm := permission.PermissionRequest{ID: "p1", SessionID: "s1"}
	assert.True(t, bridge.GrantPersistent(perm))
	assert.True(t, inner.grantPersistentCalled)

	assert.True(t, bridge.Grant(perm))
	assert.True(t, inner.grantCalled)

	assert.True(t, bridge.Deny(perm))
	assert.True(t, inner.denyCalled)

	bridge.AutoApproveSession("s1")
	assert.True(t, inner.autoApproveCalled)

	bridge.SetSkipRequests(true)
	assert.True(t, inner.skipSet)

	assert.True(t, bridge.SkipRequests())
	assert.True(t, inner.skipCalled)

	// SubscribeNotifications returns a non-nil channel.
	ch := bridge.SubscribeNotifications(ctx)
	assert.NotNil(t, ch)

	// Subscribe returns a non-nil channel.
	subCh := bridge.Subscribe(ctx)
	assert.NotNil(t, subCh)
}

// stubPermissionService implements permission.Service for testing.
type stubPermissionService struct {
	requestCalled         bool
	grantPersistentCalled bool
	grantCalled           bool
	denyCalled            bool
	autoApproveCalled     bool
	skipSet               bool
	skipCalled            bool
	publishCalled         bool
	lastPublished         permission.PermissionRequest
}

func (s *stubPermissionService) Request(ctx context.Context, opts permission.CreatePermissionRequest) (bool, error) {
	s.requestCalled = true
	return true, nil
}

func (s *stubPermissionService) GrantPersistent(perm permission.PermissionRequest) bool {
	s.grantPersistentCalled = true
	return true
}

func (s *stubPermissionService) Grant(perm permission.PermissionRequest) bool {
	s.grantCalled = true
	return true
}

func (s *stubPermissionService) Deny(perm permission.PermissionRequest) bool {
	s.denyCalled = true
	return true
}

func (s *stubPermissionService) AutoApproveSession(sessionID string) {
	s.autoApproveCalled = true
}

func (s *stubPermissionService) SetSkipRequests(skip bool) {
	s.skipSet = true
}

func (s *stubPermissionService) SkipRequests() bool {
	s.skipCalled = true
	return true
}

func (s *stubPermissionService) SubscribeNotifications(ctx context.Context) <-chan pubsub.Event[permission.PermissionNotification] {
	ch := make(chan pubsub.Event[permission.PermissionNotification], 1)
	return ch
}

func (s *stubPermissionService) Subscribe(ctx context.Context) <-chan pubsub.Event[permission.PermissionRequest] {
	ch := make(chan pubsub.Event[permission.PermissionRequest], 1)
	return ch
}

func (s *stubPermissionService) Publish(et pubsub.EventType, payload permission.PermissionRequest) {
	s.publishCalled = true
	s.lastPublished = payload
}
