package team

import (
	"context"
	"fmt"
	"time"
)

// ResolveRequest is the input for resolving a pending permission request.
type ResolveRequest struct {
	RequestID string
	Decision  string // "allowed" or "denied"
	Scope     string // "call", "task", or "session"
	DecidedBy string // "user", "system", or "hook"
}

// PermissionFSM manages the lifecycle of permission requests and grants.
type PermissionFSM struct {
	store      *PermissionStore
	grantStore *GrantStore
	auditFn    PermAuditFunc
}

// NewPermissionFSM creates a new PermissionFSM.
func NewPermissionFSM(store *PermissionStore, grantStore *GrantStore, auditFn PermAuditFunc) *PermissionFSM {
	return &PermissionFSM{
		store:      store,
		grantStore: grantStore,
		auditFn:    auditFn,
	}
}

// Resolve handles a user decision on a pending permission request.
func (fsm *PermissionFSM) Resolve(ctx context.Context, req ResolveRequest) error {
	permReq, err := fsm.store.GetRequest(ctx, req.RequestID)
	if err != nil {
		return fmt.Errorf("resolve: %w", err)
	}

	if permReq.Status != "pending" {
		return fmt.Errorf("resolve: request %s is not pending (status=%s)", req.RequestID, permReq.Status)
	}

	now := time.Now()

	switch req.Decision {
	case "allowed":
		permReq.Status = "allowed"
		permReq.Decision = "allowed"
		permReq.DecisionScope = req.Scope
		permReq.DecidedBy = req.DecidedBy
		permReq.DecidedAt = &now

		// Create grant based on scope.
		scope := req.Scope
		if scope == "" {
			scope = "call"
		}
		grant := &Grant{
			ID:              fmt.Sprintf("grant-%d", time.Now().UnixNano()),
			WorkspaceID:     permReq.WorkspaceID,
			TeamID:          permReq.TeamID,
			MemberID:        permReq.MemberID,
			TaskID:          permReq.TaskID,
			SessionID:       permReq.SessionID,
			ToolName:        permReq.ToolName,
			Action:          permReq.Action,
			ResourceType:    permReq.ResourceType,
			ResourceRef:     permReq.ResourceRef,
			Scope:           scope,
			SourceRequestID: permReq.ID,
			CreatedAt:       now,
		}
		// Set expiry based on scope.
		switch scope {
		case "call":
			grant.ExpiresAt = now.Add(30 * time.Minute)
		case "task":
			grant.ExpiresAt = now.Add(24 * time.Hour)
		case "session":
			grant.ExpiresAt = now.Add(7 * 24 * time.Hour)
		}

		if err := fsm.grantStore.CreateGrant(ctx, grant); err != nil {
			return fmt.Errorf("resolve: create grant: %w", err)
		}

		fsm.auditFn(ctx, PermAuditEvent{
			Action: PermAuditPermissionAllowed, TeamID: permReq.TeamID, MemberID: permReq.MemberID,
			TaskID: permReq.TaskID, RunID: permReq.RunID, ToolName: permReq.ToolName,
			Resource: permReq.ResourceRef, Decision: "allowed", Scope: scope, DecidedBy: req.DecidedBy,
			Timestamp: now,
		})

	case "denied":
		permReq.Status = "denied"
		permReq.Decision = "denied"
		permReq.DecidedBy = req.DecidedBy
		permReq.DecidedAt = &now

		fsm.auditFn(ctx, PermAuditEvent{
			Action: PermAuditPermissionDenied, TeamID: permReq.TeamID, MemberID: permReq.MemberID,
			TaskID: permReq.TaskID, RunID: permReq.RunID, ToolName: permReq.ToolName,
			Resource: permReq.ResourceRef, Decision: "denied", DecidedBy: req.DecidedBy,
			Timestamp: now,
		})

	default:
		return fmt.Errorf("resolve: unknown decision %q", req.Decision)
	}

	return fsm.store.UpdateRequest(ctx, permReq)
}

// Expire marks a pending request as expired (called on timeout).
func (fsm *PermissionFSM) Expire(ctx context.Context, requestID string) error {
	permReq, err := fsm.store.GetRequest(ctx, requestID)
	if err != nil {
		return fmt.Errorf("expire: %w", err)
	}
	if permReq.Status != "pending" {
		return nil // already resolved
	}
	permReq.Status = "expired"
	fsm.auditFn(ctx, PermAuditEvent{
		Action: PermAuditPermissionExpired, TeamID: permReq.TeamID, MemberID: permReq.MemberID,
		TaskID: permReq.TaskID, RunID: permReq.RunID, ToolName: permReq.ToolName,
		Timestamp: time.Now(),
	})
	return fsm.store.UpdateRequest(ctx, permReq)
}

// Cancel marks all pending requests for a run as canceled and emits an audit
// event for each. Returns the count of canceled requests.
func (fsm *PermissionFSM) Cancel(ctx context.Context, runID string) (int, error) {
	requests := fsm.store.ListByRun(ctx, runID)
	for _, req := range requests {
		req.Status = "canceled"
		_ = fsm.store.UpdateRequest(ctx, req)
		fsm.auditFn(ctx, PermAuditEvent{
			Action: PermAuditPermissionCanceled, TeamID: req.TeamID, MemberID: req.MemberID,
			RunID: req.RunID, ToolName: req.ToolName, Timestamp: time.Now(),
		})
	}
	return len(requests), nil
}

// Orphan marks all pending requests for a member as orphaned (used during
// startup recovery). Returns the count of orphaned requests.
func (fsm *PermissionFSM) Orphan(ctx context.Context, memberID string) (int, error) {
	requests := fsm.store.ListPendingByMember(ctx, memberID)
	for _, req := range requests {
		req.Status = "orphaned"
		_ = fsm.store.UpdateRequest(ctx, req)
		fsm.auditFn(ctx, PermAuditEvent{
			Action: PermAuditPermissionOrphaned, TeamID: req.TeamID, MemberID: req.MemberID,
			RunID: req.RunID, ToolName: req.ToolName, Timestamp: time.Now(),
		})
	}
	return len(requests), nil
}

// IsLateResponse checks if a run has terminated before the decision.
func (fsm *PermissionFSM) IsLateResponse(ctx context.Context, runID string, isTerminal func(string) bool) bool {
	if isTerminal != nil && isTerminal(runID) {
		return true
	}
	return false
}
