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

	"github.com/charmbracelet/crush/internal/permission"
)

// PermAuditAction categorizes permission audit events.
type PermAuditAction string

const (
	PermAuditGrantAuto          PermAuditAction = "grant_auto"
	PermAuditPermissionRequested PermAuditAction = "permission_requested"
	PermAuditPermissionAllowed   PermAuditAction = "permission_allowed"
	PermAuditPermissionDenied    PermAuditAction = "permission_denied"
	PermAuditPermissionExpired   PermAuditAction = "permission_expired"
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

// FindActiveGrant returns an active (non-expired) grant matching the member,
// tool name, and action. Returns nil, false if no active grant is found.
func (g *GrantStore) FindActiveGrant(ctx context.Context, memberID string, toolName string, action string) (*Grant, bool) {
	g.mu.RLock()
	defer g.mu.RUnlock()
	for _, grant := range g.grants {
		if grant.MemberID == memberID && grant.ToolName == toolName &&
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

// PermissionBridge wraps permission.Service for team sessions.
// When a team member calls a tool, the bridge creates a permission request
// and waits for UI approval via a channel-based decision mechanism.
type PermissionBridge struct {
	inner      permission.Service
	store      *PermissionStore
	grantStore *GrantStore
	auditFn    PermAuditFunc
	// pendingRequests tracks requests awaiting UI decision
	pendingRequests map[string]chan bool // requestID → decision channel
	pendingMu       sync.Mutex
}

// NewPermissionBridge creates a PermissionBridge wrapping the given permission.Service.
func NewPermissionBridge(inner permission.Service) *PermissionBridge {
	return &PermissionBridge{
		inner:           inner,
		store:           NewPermissionStore(),
		grantStore:      NewGrantStore(),
		auditFn:         func(ctx context.Context, event PermAuditEvent) {}, // no-op default
		pendingRequests: make(map[string]chan bool),
	}
}

// SetAuditFunc sets the audit callback for permission events.
func (b *PermissionBridge) SetAuditFunc(fn PermAuditFunc) {
	b.auditFn = fn
}

// Request implements the permission check for team sessions.
// It first checks for active grants, then creates a permission request and
// waits for UI decision via a channel with a 5-minute timeout.
func (b *PermissionBridge) Request(ctx context.Context, opts permission.CreatePermissionRequest) (bool, error) {
	// Check existing grants first (call scope).
	if grant, ok := b.grantStore.FindActiveGrant(ctx, opts.SessionID, opts.ToolName, opts.Action); ok {
		b.auditFn(ctx, PermAuditEvent{
			Action: PermAuditGrantAuto, ToolName: opts.ToolName,
			Decision: "allowed", Scope: grant.Scope, Timestamp: time.Now(),
		})
		return true, nil
	}

	// Create a permission request for UI approval.
	reqID := fmt.Sprintf("perm-%d", time.Now().UnixNano())
	req := &PermissionRequest{
		ID: reqID, Status: "pending", RequestedScope: "call",
		ToolName: opts.ToolName, Action: opts.Action,
		CreatedAt: time.Now(), ExpiresAt: time.Now().Add(5 * time.Minute),
	}
	_ = b.store.CreateRequest(ctx, req)

	// Wait for UI decision via channel.
	ch := make(chan bool, 1)
	b.pendingMu.Lock()
	b.pendingRequests[reqID] = ch
	b.pendingMu.Unlock()

	b.auditFn(ctx, PermAuditEvent{Action: PermAuditPermissionRequested, ToolName: opts.ToolName, Timestamp: time.Now()})

	select {
	case allowed := <-ch:
		return allowed, nil
	case <-time.After(5 * time.Minute):
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
	ch <- allowed
	return nil
}
