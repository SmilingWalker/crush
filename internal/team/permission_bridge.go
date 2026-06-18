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
	"log/slog"
	"sync"
	"time"

	"github.com/charmbracelet/crush/internal/actor"
	"github.com/charmbracelet/crush/internal/permission"
	"github.com/charmbracelet/crush/internal/pubsub"
	"github.com/google/uuid"
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

// defaultRequestTimeout is how long requestWithUI waits for a UI decision
// before denying. It also sets the PermissionRequest.ExpiresAt window.
const defaultRequestTimeout = 30 * time.Second

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
	tracker         *ActiveSessionTracker // M5.2: shared singleton, injected via SetActiveSessionTracker
	// teamContexts holds team display context per pending request, queried by
	// the TUI to decide whether to render a team permission dialog.
	teamContexts map[string]*TeamPermissionContext
	// requestTimeout is how long requestWithUI waits for a UI decision before
	// denying. Defaults to 30s; overridable for tests.
	requestTimeout time.Duration
}

// NewPermissionBridge creates a PermissionBridge wrapping the given permission.Service.
func NewPermissionBridge(inner permission.Service) *PermissionBridge {
	bridge := &PermissionBridge{
		inner:           inner,
		store:           NewPermissionStore(),
		grantStore:      NewGrantStore(),
		auditFn:         func(ctx context.Context, event PermAuditEvent) {}, // no-op default
		pendingRequests: make(map[string]chan bool),
		teamContexts:    make(map[string]*TeamPermissionContext),
		requestTimeout:  defaultRequestTimeout,
	}
	fsm := NewPermissionFSM(bridge.store, bridge.grantStore, bridge.auditFn)
	bridge.queue = NewPermissionQueue(fsm)
	return bridge
}

// SetAuditFunc sets the audit callback for permission events.
func (b *PermissionBridge) SetAuditFunc(fn PermAuditFunc) {
	b.auditFn = fn
}

// SetActiveSessionTracker injects the shared ActiveSessionTracker from app.go.
// Must be called before any team member tool calls. Caller owns the tracker lifecycle.
func (b *PermissionBridge) SetActiveSessionTracker(t *ActiveSessionTracker) {
	b.tracker = t
}

// SetRequestTimeout overrides how long requestWithUI waits for a UI decision
// before denying. Intended for tests.
func (b *PermissionBridge) SetRequestTimeout(d time.Duration) {
	b.requestTimeout = d
}

// TeamContextFor returns the team display context associated with a pending
// permission request, if any. The TUI uses this to decide whether to render a
// team permission dialog (team-specific buttons) or a plain one.
func (b *PermissionBridge) TeamContextFor(reqID string) (*TeamPermissionContext, bool) {
	b.pendingMu.Lock()
	defer b.pendingMu.Unlock()
	c, ok := b.teamContexts[reqID]
	return c, ok
}

// GetQueue returns the PermissionQueue for external access.
func (b *PermissionBridge) GetQueue() *PermissionQueue {
	return b.queue
}

// Request implements the permission check for team sessions.
// For non-team sessions it delegates directly to inner. For team sessions
// it checks the active session: if the user is viewing this member's session,
// it shows a permission dialog; otherwise it uses the SkipRequests gate.
func (b *PermissionBridge) Request(ctx context.Context, opts permission.CreatePermissionRequest) (bool, error) {
	ac, hasTeam := actor.FromContext(ctx)
	slog.Debug("perm_bridge: request",
		"team_id", ac.TeamID, "member_id", ac.MemberID, "session_id", opts.SessionID,
		"tool", opts.ToolName, "action", opts.Action, "tool_call_id", opts.ToolCallID,
		"has_team", hasTeam, "tracker_set", b.tracker != nil,
	)
	if !hasTeam || ac.TeamID == "" || ac.MemberID == "" {
		// Non-team session — delegate to inner (leader normal flow).
		slog.Debug("perm_bridge: non-team delegate to inner", "tool_call_id", opts.ToolCallID)
		return b.inner.Request(ctx, opts)
	}

	// Check existing grants first (call scope).
	if grant, ok := b.grantStore.FindActiveGrant(ctx, opts.SessionID, opts.ToolName, opts.Action); ok {
		slog.Debug("perm_bridge: active grant found (auto-allow)", "tool_call_id", opts.ToolCallID, "scope", grant.Scope)
		b.auditFn(ctx, PermAuditEvent{
			Action: PermAuditGrantAuto, TeamID: ac.TeamID, MemberID: ac.MemberID,
			ToolName: opts.ToolName, Decision: "allowed", Scope: grant.Scope, Timestamp: time.Now(),
		})
		return true, nil
	}

	// Team session — check if user is viewing this member (by session ID or member ID).
	if b.tracker != nil && b.tracker.IsActiveSession(opts.SessionID, ac.MemberID) {
		slog.Debug("perm_bridge: active session matches → requestWithUI", "tool_call_id", opts.ToolCallID, "session_id", opts.SessionID, "member_id", ac.MemberID)
		return b.requestWithUI(ctx, opts, ac)
	}

	// User is viewing another session — SkipRequests gate.
	if b.inner.SkipRequests() {
		slog.Debug("perm_bridge: not active view, SkipRequests=true (auto-allow)", "tool_call_id", opts.ToolCallID)
		return true, nil
	}
	slog.Debug("perm_bridge: not active view, SkipRequests=false (deny)", "tool_call_id", opts.ToolCallID)
	return false, nil
}

// requestWithUI publishes a permission request to the inner broker so the TUI
// opens a dialog, then blocks on a channel until the user decides or the context
// is cancelled.
func (b *PermissionBridge) requestWithUI(ctx context.Context, opts permission.CreatePermissionRequest, ac actor.ActorContext) (bool, error) {
	reqID := opts.ToolCallID
	if reqID == "" {
		reqID = uuid.New().String()
	}

	ch := make(chan bool, 1)
	// ActorContext carries MemberName/MemberRole (filled by the team runtime);
	// TaskTitle is left empty here — the dialog omits the Task line when blank.
	tctx := &TeamPermissionContext{
		TeamName:   ac.TeamID,
		MemberName: ac.MemberName,
		MemberRole: ac.MemberRole,
		ToolName:   opts.ToolName,
		Action:     opts.Action,
		Resource:   opts.Path,
	}
	b.pendingMu.Lock()
	b.pendingRequests[reqID] = ch
	b.teamContexts[reqID] = tctx
	b.pendingMu.Unlock()

	slog.Debug("perm_bridge: requestWithUI stored team context",
		"tool_call_id", reqID, "team", ac.TeamID, "member", ac.MemberName, "role", ac.MemberRole,
	)

	now := time.Now()
	teamReq := &PermissionRequest{
		ID:         reqID,
		SessionID:  opts.SessionID,
		ToolCallID: reqID,
		ToolName:   opts.ToolName,
		Action:     opts.Action,
		Status:     "pending",
		CreatedAt:  now,
		ExpiresAt:  now.Add(defaultRequestTimeout),
	}
	b.queue.Enqueue(ctx, teamReq)

	// Publish to inner's broker so the TUI dialog opens.
	b.inner.Publish(pubsub.CreatedEvent, permission.PermissionRequest{
		ID:         reqID,
		ToolCallID: reqID,
		ToolName:   opts.ToolName,
		SessionID:  opts.SessionID,
		Action:     opts.Action,
		Path:       opts.Path,
	})
	slog.Debug("perm_bridge: published permission request to UI", "tool_call_id", reqID)

	// cleanup removes this request's channel and team context. Idempotent.
	cleanup := func() {
		b.pendingMu.Lock()
		delete(b.pendingRequests, reqID)
		delete(b.teamContexts, reqID)
		b.pendingMu.Unlock()
	}

	timer := time.NewTimer(b.requestTimeout)
	defer timer.Stop()

	select {
	case <-ctx.Done():
		slog.Debug("perm_bridge: request cancelled", "tool_call_id", reqID, "err", ctx.Err())
		cleanup()
		b.queue.Dequeue(reqID)
		return false, ctx.Err()
	case <-timer.C:
		// Timeout = denial. ResolveRequest (if it races in later) finds the
		// entry gone and returns an error without sending; the buffered
		// channel is discarded.
		slog.Debug("perm_bridge: request timed out (deny)", "tool_call_id", reqID)
		cleanup()
		b.queue.Dequeue(reqID)
		return false, nil
	case allowed := <-ch:
		// ResolveRequest already deleted the entry before feeding; dequeue only.
		slog.Debug("perm_bridge: request resolved by UI", "tool_call_id", reqID, "allowed", allowed)
		b.queue.Dequeue(reqID)
		return allowed, nil
	}
}

// ResolveRequest is called by the UI when the user makes a decision on a
// pending permission request.
func (b *PermissionBridge) ResolveRequest(reqID string, allowed bool, scope string) error {
	b.pendingMu.Lock()
	ch, ok := b.pendingRequests[reqID]
	if ok {
		delete(b.pendingRequests, reqID)
		delete(b.teamContexts, reqID)
	}
	b.pendingMu.Unlock()

	slog.Debug("perm_bridge: ResolveRequest",
		"tool_call_id", reqID, "allowed", allowed, "scope", scope, "found_pending", ok,
	)

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

// Publish delegates to the inner permission.Service so the bridge can
// publish team permission requests to the UI event stream.
func (b *PermissionBridge) Publish(et pubsub.EventType, payload permission.PermissionRequest) {
	b.inner.Publish(et, payload)
}
