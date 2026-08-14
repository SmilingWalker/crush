package team

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"
)

// errNotPending is returned by store Update callbacks when the request has
// already left the pending state; callers treat it as an idempotent no-op.
var errNotPending = errors.New("request is not pending")

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
	// mu guards auditFn, replaceable after construction via SetAuditFunc.
	mu      sync.Mutex
	auditFn PermAuditFunc
}

// NewPermissionFSM creates a new PermissionFSM.
func NewPermissionFSM(store *PermissionStore, grantStore *GrantStore, auditFn PermAuditFunc) *PermissionFSM {
	return &PermissionFSM{
		store:      store,
		grantStore: grantStore,
		auditFn:    auditFn,
	}
}

// SetAuditFunc replaces the audit callback (propagated from the bridge).
func (fsm *PermissionFSM) SetAuditFunc(fn PermAuditFunc) {
	fsm.mu.Lock()
	defer fsm.mu.Unlock()
	fsm.auditFn = fn
}

// audit emits an event through the current audit callback. The callback runs
// outside fsm.mu so it may persist to storage without holding the lock.
func (fsm *PermissionFSM) audit(ctx context.Context, event PermAuditEvent) {
	fsm.mu.Lock()
	fn := fsm.auditFn
	fsm.mu.Unlock()
	if fn != nil {
		fn(ctx, event)
	}
}

// Resolve handles a user decision on a pending permission request.
func (fsm *PermissionFSM) Resolve(ctx context.Context, req ResolveRequest) error {
	switch req.Decision {
	case "allowed", "denied":
	default:
		return fmt.Errorf("resolve: unknown decision %q", req.Decision)
	}

	now := time.Now()
	updated, err := fsm.store.Update(ctx, req.RequestID, func(r *PermissionRequest) error {
		if r.Status != "pending" {
			return fmt.Errorf("request %s is not pending (status=%s)", req.RequestID, r.Status)
		}
		r.Status = req.Decision
		r.Decision = req.Decision
		r.DecidedBy = req.DecidedBy
		r.DecidedAt = &now
		if req.Decision == "allowed" {
			r.DecisionScope = req.Scope
		}
		return nil
	})
	if err != nil {
		return fmt.Errorf("resolve: %w", err)
	}

	if req.Decision == "denied" {
		fsm.audit(ctx, PermAuditEvent{
			WorkspaceID: updated.WorkspaceID, SessionID: updated.SessionID, ToolCallID: updated.ToolCallID,
			Action: PermAuditPermissionDenied, TeamID: updated.TeamID, MemberID: updated.MemberID,
			TaskID: updated.TaskID, RunID: updated.RunID, ToolName: updated.ToolName,
			Resource: updated.ResourceRef, Decision: "denied", DecidedBy: req.DecidedBy,
			Timestamp: now,
		})
		return nil
	}

	scope := req.Scope
	if scope == "" {
		scope = "call"
	}
	if scope != "call" {
		grant := &Grant{
			ID:              fmt.Sprintf("grant-%d", time.Now().UnixNano()),
			WorkspaceID:     updated.WorkspaceID,
			TeamID:          updated.TeamID,
			MemberID:        updated.MemberID,
			TaskID:          updated.TaskID,
			SessionID:       updated.SessionID,
			ToolName:        updated.ToolName,
			Action:          updated.Action,
			ResourceType:    updated.ResourceType,
			ResourceRef:     updated.ResourceRef,
			Scope:           scope,
			SourceRequestID: updated.ID,
			CreatedAt:       now,
		}
		switch scope {
		case "task":
			grant.ExpiresAt = now.Add(24 * time.Hour)
		case "session":
			grant.ExpiresAt = now.Add(7 * 24 * time.Hour)
		}
		if err := fsm.grantStore.CreateGrant(ctx, grant); err != nil {
			return fmt.Errorf("resolve: create grant: %w", err)
		}
	}

	fsm.audit(ctx, PermAuditEvent{
		WorkspaceID: updated.WorkspaceID, SessionID: updated.SessionID, ToolCallID: updated.ToolCallID,
		Action: PermAuditPermissionAllowed, TeamID: updated.TeamID, MemberID: updated.MemberID,
		TaskID: updated.TaskID, RunID: updated.RunID, ToolName: updated.ToolName,
		Resource: updated.ResourceRef, Decision: "allowed", Scope: scope, DecidedBy: req.DecidedBy,
		Timestamp: now,
	})
	return nil
}

// Expire marks a pending request as expired (called on timeout).
func (fsm *PermissionFSM) Expire(ctx context.Context, requestID string) error {
	updated, err := fsm.store.Update(ctx, requestID, func(r *PermissionRequest) error {
		if r.Status != "pending" {
			return errNotPending
		}
		r.Status = "expired"
		return nil
	})
	if errors.Is(err, errNotPending) {
		return nil // already resolved — idempotent
	}
	if err != nil {
		return fmt.Errorf("expire: %w", err)
	}
	fsm.audit(ctx, PermAuditEvent{
		WorkspaceID: updated.WorkspaceID, SessionID: updated.SessionID, ToolCallID: updated.ToolCallID,
		Action: PermAuditPermissionExpired, TeamID: updated.TeamID, MemberID: updated.MemberID,
		TaskID: updated.TaskID, RunID: updated.RunID, ToolName: updated.ToolName,
		Timestamp: time.Now(),
	})
	return nil
}

// CancelRequest marks a single pending request as canceled (e.g. the member's
// context went away). Idempotent: already-resolved requests return nil.
func (fsm *PermissionFSM) CancelRequest(ctx context.Context, requestID string) error {
	updated, err := fsm.store.Update(ctx, requestID, func(r *PermissionRequest) error {
		if r.Status != "pending" {
			return errNotPending
		}
		r.Status = "canceled"
		return nil
	})
	if errors.Is(err, errNotPending) {
		return nil // already resolved — idempotent
	}
	if err != nil {
		return fmt.Errorf("cancel request: %w", err)
	}
	fsm.audit(ctx, PermAuditEvent{
		WorkspaceID: updated.WorkspaceID, SessionID: updated.SessionID, ToolCallID: updated.ToolCallID,
		Action: PermAuditPermissionCanceled, TeamID: updated.TeamID, MemberID: updated.MemberID,
		TaskID: updated.TaskID, RunID: updated.RunID, ToolName: updated.ToolName,
		Timestamp: time.Now(),
	})
	return nil
}

// Cancel marks all pending requests for a run as canceled and emits an audit
// event for each. Returns the count of canceled requests.
func (fsm *PermissionFSM) Cancel(ctx context.Context, runID string) (int, error) {
	requests := fsm.store.ListByRun(ctx, runID)
	count := 0
	for _, req := range requests {
		updated, err := fsm.store.Update(ctx, req.ID, func(r *PermissionRequest) error {
			if r.Status != "pending" {
				return errNotPending
			}
			r.Status = "canceled"
			return nil
		})
		if errors.Is(err, errNotPending) {
			continue
		}
		if err != nil {
			return count, fmt.Errorf("cancel: %w", err)
		}
		count++
		fsm.audit(ctx, PermAuditEvent{
			WorkspaceID: updated.WorkspaceID, SessionID: updated.SessionID, ToolCallID: updated.ToolCallID,
			Action: PermAuditPermissionCanceled, TeamID: updated.TeamID, MemberID: updated.MemberID,
			RunID: updated.RunID, ToolName: updated.ToolName, Timestamp: time.Now(),
		})
	}
	return count, nil
}

// Orphan marks all pending requests for a member as orphaned (used during
// startup recovery). Returns the count of orphaned requests.
func (fsm *PermissionFSM) Orphan(ctx context.Context, memberID string) (int, error) {
	requests := fsm.store.ListPendingByMember(ctx, memberID)
	count := 0
	for _, req := range requests {
		updated, err := fsm.store.Update(ctx, req.ID, func(r *PermissionRequest) error {
			if r.Status != "pending" {
				return errNotPending
			}
			r.Status = "orphaned"
			return nil
		})
		if errors.Is(err, errNotPending) {
			continue
		}
		if err != nil {
			return count, fmt.Errorf("orphan: %w", err)
		}
		count++
		fsm.audit(ctx, PermAuditEvent{
			WorkspaceID: updated.WorkspaceID, SessionID: updated.SessionID, ToolCallID: updated.ToolCallID,
			Action: PermAuditPermissionOrphaned, TeamID: updated.TeamID, MemberID: updated.MemberID,
			RunID: updated.RunID, ToolName: updated.ToolName, Timestamp: time.Now(),
		})
	}
	return count, nil
}

// IsLateResponse checks if a run has terminated before the decision.
func (fsm *PermissionFSM) IsLateResponse(ctx context.Context, runID string, isTerminal func(string) bool) bool {
	if isTerminal != nil && isTerminal(runID) {
		return true
	}
	return false
}
