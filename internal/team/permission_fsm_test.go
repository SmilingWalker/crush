package team

import (
	"context"
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
		ID: "r1", TeamID: "t1", MemberID: "m1", RunID: "run1",
		ToolName: "bash", Action: "execute", ResourceRef: "/tmp/test",
		Status: "pending", RequestedScope: "call", CreatedAt: time.Now(),
	}
	require.NoError(t, ps.CreateRequest(ctx, req))

	err := fsm.Resolve(ctx, ResolveRequest{RequestID: "r1", Decision: "allowed", Scope: "call", DecidedBy: "user"})
	require.NoError(t, err)

	got, _ := ps.GetRequest(ctx, "r1")
	assert.Equal(t, "allowed", got.Status)
	assert.Equal(t, "call", got.DecisionScope)

	grant, ok := gs.FindActiveGrant(ctx, "m1", "bash", "execute")
	assert.True(t, ok)
	assert.Equal(t, "call", grant.Scope)
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
