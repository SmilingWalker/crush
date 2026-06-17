// permission_bridge.go is the M5 PermissionBridge — a team-aware wrapper around
// the existing permission.Service. When a member (running in a team session) calls
// a tool like bash/write/edit, the PermissionBridge intercepts the call and routes
// it through the team permission store for UI approval instead of auto-denying.
//
// In-memory stores (GrantStore, PermissionStore) for now — DB-backed in Task 1b.

package team

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/charmbracelet/crush/internal/actor"
	"github.com/charmbracelet/crush/internal/permission"
	"github.com/charmbracelet/crush/internal/pubsub"
)

// PermAuditAction categorizes permission audit events.
type PermAuditAction string

const (
	PermAuditGrantAuto          PermAuditAction = "grant_auto"
	PermAuditPermissionRequested PermAuditAction = "permission_requested"
	PermAuditPermissionAllowed   PermAuditAction = "permission_allowed"
	PermAuditPermissionDenied    PermAuditAction = "permission_denied"
	PermAuditPermissionExpired   PermAuditAction = "permission_expired"
	PermAuditPermissionCanceled  PermAuditAction = "permission_canceled"
	PermAuditPermissionOrphaned  PermAuditAction = "permission_orphaned"
	PermAuditHookAllow           PermAuditAction = "hook_allow"
	PermAuditHookDeny            PermAuditAction = "hook_deny"
	PermAuditLateResponse        PermAuditAction = "late_response"
)

// PermAuditEvent records a permission-related event for the audit trail.
type PermAuditEvent struct {
	Action    PermAuditAction
	TeamID    string
	MemberID  string
	TaskID    string
	RunID     string
	ToolName  string
	Resource  string
	Decision  string
	Scope     string
	DecidedBy string
	Timestamp time.Time
}

// PermAuditFunc is called for every permission audit event.
type PermAuditFunc func(ctx context.Context, event PermAuditEvent)

// Grant represents an active permission grant for a team member.
type Grant struct {
	ID              string
	WorkspaceID     string
	TeamID          string
	MemberID        string
	TaskID          string
	SessionID       string
	ToolName        string
	Action          string
	ResourceType    string
	ResourceRef     string
	Scope           string // "call", "task", "session"
	SourceRequestID string
	ExpiresAt       time.Time
	CreatedAt       time.Time
}

// GrantStore provides in-memory grant lookup and creation (DB-backed in Task 1b).
type GrantStore struct {
	mu     sync.RWMutex
	grants map[string]*Grant
}

// NewGrantStore returns an empty in-memory GrantStore.
func NewGrantStore() *GrantStore {
	return &GrantStore{grants: make(map[string]*Grant)}
}

// FindActiveGrant returns an active (non-expired) grant matching the session,
// tool name, and action. Returns nil, false if no active grant is found.
func (g *GrantStore) FindActiveGrant(ctx context.Context, sessionID string, toolName string, action string) (*Grant, bool) {
	g.mu.RLock()
	defer g.mu.RUnlock()
	for _, grant := range g.grants {
		if grant.SessionID == sessionID && grant.ToolName == toolName &&
			grant.Action == action && time.Now().Before(grant.ExpiresAt) {
			return grant, true
		}
	}
	return nil, false
}

// CreateGrant inserts a grant into the store.
func (g *GrantStore) CreateGrant(ctx context.Context, grant *Grant) error {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.grants[grant.ID] = grant
	return nil
}

// PermissionRequest represents a pending permission request awaiting UI decision.
type PermissionRequest struct {
	ID             string
	WorkspaceID    string
	TeamID         string
	MemberID       string
	TaskID         string
	RunID          string
	SessionID      string
	ToolCallID     string
	ToolName       string
	Action         string
	ResourceType   string
	ResourceRef    string
	ReasonSummary  string
	Status         string // "pending", "allowed", "denied", "expired", "canceled", "orphaned"
	RequestedScope string // "call"
	DecisionScope  string
	Decision       string
	DecidedBy      string // "user", "system", "hook"
	CreatedAt      time.Time
	ExpiresAt      time.Time
	DecidedAt      *time.Time
}

// PermissionStore manages in-memory permission requests.
type PermissionStore struct {
	mu       sync.RWMutex
	requests map[string]*PermissionRequest
}

// NewPermissionStore returns an empty in-memory PermissionStore.
func NewPermissionStore() *PermissionStore {
	return &PermissionStore{requests: make(map[string]*PermissionRequest)}
}

// CreateRequest inserts a permission request into the store.
func (s *PermissionStore) CreateRequest(ctx context.Context, req *PermissionRequest) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.requests[req.ID] = req
	return nil
}

// GetRequest retrieves a permission request by ID.
func (s *PermissionStore) GetRequest(ctx context.Context, id string) (*PermissionRequest, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	req, ok := s.requests[id]
	if !ok {
		return nil, fmt.Errorf("permission request not found: %s", id)
	}
	return req, nil
}

// UpdateRequest updates an existing permission request.
func (s *PermissionStore) UpdateRequest(ctx context.Context, req *PermissionRequest) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.requests[req.ID] = req
	return nil
}

// ListByRun returns all pending requests for a given run ID.
func (s *PermissionStore) ListByRun(ctx context.Context, runID string) []*PermissionRequest {
	s.mu.RLock()
	defer s.mu.RUnlock()
	var result []*PermissionRequest
	for _, req := range s.requests {
		if req.RunID == runID && req.Status == "pending" {
			result = append(result, req)
		}
	}
	return result
}

// ListPendingByMember returns all pending requests for a given member ID.
func (s *PermissionStore) ListPendingByMember(ctx context.Context, memberID string) []*PermissionRequest {
	s.mu.RLock()
	defer s.mu.RUnlock()
	var result []*PermissionRequest
	for _, req := range s.requests {
		if req.MemberID == memberID && req.Status == "pending" {
			result = append(result, req)
		}
	}
	return result
}

// PermissionBridge wraps permission.Service for team sessions.
// When a team member calls a tool, the bridge creates a permission request
// and waits for UI approval via a channel-based decision mechanism.
type PermissionBridge struct {
	inner      permission.Service
	store      *PermissionStore
	grantStore *GrantStore
	queue      *PermissionQueue
	auditFn    PermAuditFunc
	// pendingRequests tracks requests awaiting UI decision
	pendingRequests map[string]chan bool // requestID → decision channel
	pendingMu       sync.Mutex
}

// NewPermissionBridge creates a PermissionBridge wrapping the given permission.Service.
func NewPermissionBridge(inner permission.Service) *PermissionBridge {
	bridge := &PermissionBridge{
		inner:           inner,
		store:           NewPermissionStore(),
		grantStore:      NewGrantStore(),
		auditFn:         func(ctx context.Context, event PermAuditEvent) {}, // no-op default
		pendingRequests: make(map[string]chan bool),
	}
	fsm := NewPermissionFSM(bridge.store, bridge.grantStore, bridge.auditFn)
	bridge.queue = NewPermissionQueue(fsm)
	return bridge
}

// SetAuditFunc sets the audit callback for permission events.
func (b *PermissionBridge) SetAuditFunc(fn PermAuditFunc) {
	b.auditFn = fn
}

// GetQueue returns the PermissionQueue for external access.
func (b *PermissionBridge) GetQueue() *PermissionQueue {
	return b.queue
}

// Request implements the permission check for team sessions.
// For non-team sessions it delegates directly to inner. For team sessions
// it checks grants, enqueues a permission request, and waits for UI decision.
func (b *PermissionBridge) Request(ctx context.Context, opts permission.CreatePermissionRequest) (bool, error) {
	// Check for team context — delegate non-team sessions to inner.
	ac, hasTeam := actor.FromContext(ctx)
	if !hasTeam || ac.TeamID == "" || ac.MemberID == "" {
		return b.inner.Request(ctx, opts)
	}

	// Check existing grants first (call scope).
	if grant, ok := b.grantStore.FindActiveGrant(ctx, opts.SessionID, opts.ToolName, opts.Action); ok {
		b.auditFn(ctx, PermAuditEvent{
			Action: PermAuditGrantAuto, TeamID: ac.TeamID, MemberID: ac.MemberID,
			ToolName: opts.ToolName, Decision: "allowed", Scope: grant.Scope, Timestamp: time.Now(),
		})
		return true, nil
	}

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
}

// ResolveRequest is called by the UI when the user makes a decision on a
// pending permission request.
func (b *PermissionBridge) ResolveRequest(reqID string, allowed bool, scope string) error {
	b.pendingMu.Lock()
	ch, ok := b.pendingRequests[reqID]
	if ok {
		delete(b.pendingRequests, reqID)
	}
	b.pendingMu.Unlock()

	if !ok {
		return fmt.Errorf("request not pending: %s", reqID)
	}

	// Dequeue from the permission queue to stop the expiry timer.
	b.queue.Dequeue(reqID)

	ch <- allowed
	return nil
}

// --- delegation methods implementing the full permission.Service interface ---

// GrantPersistent delegates to the inner permission.Service.
func (b *PermissionBridge) GrantPersistent(perm permission.PermissionRequest) bool {
	return b.inner.GrantPersistent(perm)
}

// Grant delegates to the inner permission.Service.
func (b *PermissionBridge) Grant(perm permission.PermissionRequest) bool {
	return b.inner.Grant(perm)
}

// Deny delegates to the inner permission.Service.
func (b *PermissionBridge) Deny(perm permission.PermissionRequest) bool {
	return b.inner.Deny(perm)
}

// AutoApproveSession delegates to the inner permission.Service.
func (b *PermissionBridge) AutoApproveSession(sessionID string) {
	b.inner.AutoApproveSession(sessionID)
}

// SetSkipRequests delegates to the inner permission.Service.
func (b *PermissionBridge) SetSkipRequests(skip bool) {
	b.inner.SetSkipRequests(skip)
}

// SkipRequests delegates to the inner permission.Service.
func (b *PermissionBridge) SkipRequests() bool {
	return b.inner.SkipRequests()
}

// SubscribeNotifications delegates to the inner permission.Service.
func (b *PermissionBridge) SubscribeNotifications(ctx context.Context) <-chan pubsub.Event[permission.PermissionNotification] {
	return b.inner.SubscribeNotifications(ctx)
}

// Subscribe delegates to the inner permission.Service (pubsub.Subscriber[permission.PermissionRequest]).
func (b *PermissionBridge) Subscribe(ctx context.Context) <-chan pubsub.Event[permission.PermissionRequest] {
	return b.inner.Subscribe(ctx)
}
