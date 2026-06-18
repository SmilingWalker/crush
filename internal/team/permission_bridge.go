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
const defaultRequestTimeout = 60 * time.Second

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

// pendingEntry is one member permission request awaiting a UI decision. It is
// either waiting in displayFIFO or currently the displayed slot. Only the
// displayed entry has been Published to the UI and has a live timer.
type pendingEntry struct {
	reqID     string
	ch        chan bool                  // decision from UI (buffered, size 1)
	tctx      *TeamPermissionContext
	opts      permission.CreatePermissionRequest
	ac        actor.ActorContext
	timeoutCh chan struct{}              // closed when the 60s display-timer fires
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
	tracker         *ActiveSessionTracker // M5.2: shared singleton, injected via SetActiveSessionTracker
	// teamContexts holds team display context per pending request, queried by
	// the TUI to decide whether to render a team permission dialog.
	teamContexts map[string]*TeamPermissionContext
	// requestTimeout is how long requestWithUI waits for a UI decision before
	// denying. Defaults to 60s; overridable for tests.
	requestTimeout time.Duration
	// --- display coordination (M5.4) ---
	// queueMu guards pendingRequests, teamContexts, entries, displayFIFO, and
	// displayed (the entire pending-request state machine).
	queueMu     sync.Mutex
	displayFIFO []*pendingEntry             // waiting, not yet shown
	displayed   *pendingEntry               // the one currently shown (≤1 invariant)
	entries     map[string]*pendingEntry    // reqID → entry, O(1) resolve/cancel/timeout
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
		entries:         make(map[string]*pendingEntry),
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
	b.queueMu.Lock()
	defer b.queueMu.Unlock()
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

	// Team session — yolo (SkipRequests) auto-allows, bypassing the UI.
	if b.inner.SkipRequests() {
		slog.Debug("perm_bridge: team SkipRequests=true (auto-allow)", "tool_call_id", opts.ToolCallID)
		return true, nil
	}
	// Otherwise always show a popup regardless of focused session (M5.4).
	slog.Debug("perm_bridge: team → requestWithUI (always show)", "tool_call_id", opts.ToolCallID, "session_id", opts.SessionID, "member_id", ac.MemberID)
	return b.requestWithUI(ctx, opts, ac)
}

// requestWithUI enqueues a permission request into the display queue and blocks
// until the user decides (via ResolveRequest), the 60s display timer fires, or
// the context is cancelled. The timer starts only when the entry becomes the
// displayed slot (fair timeout).
func (b *PermissionBridge) requestWithUI(ctx context.Context, opts permission.CreatePermissionRequest, ac actor.ActorContext) (bool, error) {
	reqID := opts.ToolCallID
	if reqID == "" {
		reqID = uuid.New().String()
	}

	entry := &pendingEntry{
		reqID: reqID,
		ch:    make(chan bool, 1),
		tctx: &TeamPermissionContext{
			TeamName:   ac.TeamID,
			MemberName: ac.MemberName,
			MemberRole: ac.MemberRole,
			ToolName:   opts.ToolName,
			Action:     opts.Action,
			Resource:   opts.Path,
		},
		opts:      opts,
		ac:        ac,
		timeoutCh: make(chan struct{}),
	}

	now := time.Now()
	teamReq := &PermissionRequest{
		ID:         reqID,
		SessionID:  opts.SessionID,
		ToolCallID: reqID,
		ToolName:   opts.ToolName,
		Action:     opts.Action,
		Status:     "pending",
		CreatedAt:  now,
		ExpiresAt:  now.Add(b.requestTimeout),
	}

	b.queueMu.Lock()
	b.displayFIFO = append(b.displayFIFO, entry)
	b.entries[reqID] = entry
	b.pendingRequests[reqID] = entry.ch
	b.teamContexts[reqID] = entry.tctx
	b.queueMu.Unlock()

	_ = b.queue.Enqueue(ctx, teamReq) // FSM queue (5min TTL backstop); ignore full error

	slog.Debug("perm_bridge: requestWithUI enqueued",
		"tool_call_id", reqID, "team", ac.TeamID, "member", ac.MemberName, "role", ac.MemberRole,
	)

	b.pumpDisplay()

	select {
	case <-ctx.Done():
		b.terminateEntry(reqID)
		slog.Debug("perm_bridge: request cancelled", "tool_call_id", reqID, "err", ctx.Err())
		return false, ctx.Err()
	case <-entry.timeoutCh:
		// handleTimeout already dequeued from the FSM queue.
		slog.Debug("perm_bridge: request timed out (deny)", "tool_call_id", reqID)
		return false, nil
	case allowed := <-entry.ch:
		// ResolveRequest already dequeued from the FSM queue.
		slog.Debug("perm_bridge: request resolved by UI", "tool_call_id", reqID, "allowed", allowed)
		return allowed, nil
	}
}

// terminateEntry is the ctx.Done driver: removes the entry from the queue and,
// if it was the displayed slot, advances the queue. Idempotent. Callers must
// NOT hold queueMu.
func (b *PermissionBridge) terminateEntry(reqID string) {
	b.queueMu.Lock()
	entry, ok := b.entries[reqID]
	if !ok {
		b.queueMu.Unlock()
		return
	}
	wasDisplayed := b.displayed == entry
	if wasDisplayed {
		b.displayed = nil
	}
	delete(b.entries, reqID)
	delete(b.pendingRequests, reqID)
	delete(b.teamContexts, reqID)
	removeFromFIFO(b, reqID)
	b.queueMu.Unlock()

	if wasDisplayed {
		b.pumpDisplay()
	}
	b.queue.Dequeue(reqID)
}

// ResolveRequest is called by the UI when the user decides a pending request.
// It terminates the entry, advances the queue if it was displayed, and feeds
// the decision to the blocking goroutine. Returns an error if the entry was
// already terminated (resolved/timed out/cancelled).
func (b *PermissionBridge) ResolveRequest(reqID string, allowed bool, scope string) error {
	b.queueMu.Lock()
	entry, ok := b.entries[reqID]
	if !ok {
		b.queueMu.Unlock()
		slog.Debug("perm_bridge: ResolveRequest not pending", "tool_call_id", reqID)
		return fmt.Errorf("request not pending: %s", reqID)
	}
	wasDisplayed := b.displayed == entry
	if wasDisplayed {
		b.displayed = nil
	}
	delete(b.entries, reqID)
	delete(b.pendingRequests, reqID)
	delete(b.teamContexts, reqID)
	removeFromFIFO(b, reqID)
	b.queueMu.Unlock()

	if wasDisplayed {
		b.pumpDisplay()
	}
	b.queue.Dequeue(reqID)

	slog.Debug("perm_bridge: ResolveRequest",
		"tool_call_id", reqID, "allowed", allowed, "scope", scope, "was_displayed", wasDisplayed,
	)

	entry.ch <- allowed // buffered, size 1 — never blocks
	return nil
}

// pumpDisplay promotes the front of displayFIFO to the displayed slot, arms its
// 60s timer, and publishes it to the UI. If something is already displayed, it
// does nothing. Callers must NOT hold queueMu.
func (b *PermissionBridge) pumpDisplay() {
	b.queueMu.Lock()
	if b.displayed != nil {
		b.queueMu.Unlock()
		return
	}
	if len(b.displayFIFO) == 0 {
		b.queueMu.Unlock()
		return
	}
	entry := b.displayFIFO[0]
	b.displayFIFO = b.displayFIFO[1:]
	b.displayed = entry
	reqID := entry.reqID
	// Arm the display timer (60s by default). handleTimeout clears the slot and
	// wakes the blocking goroutine via timeoutCh. Fire-and-forget: a late fire
	// after the entry is already terminated is a no-op (handleTimeout's
	// entries[reqID] membership guard).
	time.AfterFunc(b.requestTimeout, func() {
		b.handleTimeout(reqID)
	})
	// Publish to the inner broker so the TUI opens the dialog. Done UNDER the
	// lock so a concurrent terminal handler cannot terminate this entry between
	// the displayed-slot assignment and the Publish — that would Publish a stale
	// entry and violate the ≤1-displayed invariant. inner.Publish is a
	// non-blocking channel send, safe to call while holding queueMu.
	b.inner.Publish(pubsub.CreatedEvent, permission.PermissionRequest{
		ID:         reqID,
		ToolCallID: reqID,
		ToolName:   entry.opts.ToolName,
		SessionID:  entry.opts.SessionID,
		Action:     entry.opts.Action,
		Path:       entry.opts.Path,
	})
	fifoRemaining := len(b.displayFIFO)
	b.queueMu.Unlock()

	slog.Debug("perm_bridge: pumpDisplay published",
		"tool_call_id", reqID, "fifo_remaining", fifoRemaining,
	)
}

// handleTimeout is the AfterFunc callback for a displayed entry's timer. It
// terminates the entry (deny) and advances the queue. Idempotent: if the entry
// was already resolved/cancelled, it is a no-op.
func (b *PermissionBridge) handleTimeout(reqID string) {
	b.queueMu.Lock()
	entry, ok := b.entries[reqID]
	if !ok {
		b.queueMu.Unlock()
		return
	}
	wasDisplayed := b.displayed == entry
	if wasDisplayed {
		b.displayed = nil
	}
	delete(b.entries, reqID)
	delete(b.pendingRequests, reqID)
	delete(b.teamContexts, reqID)
	removeFromFIFO(b, reqID)
	b.queueMu.Unlock()

	if wasDisplayed {
		b.pumpDisplay()
	}
	b.queue.Dequeue(reqID)
	close(entry.timeoutCh)
}

// removeFromFIFO removes reqID from displayFIFO. Caller must hold queueMu.
func removeFromFIFO(b *PermissionBridge, reqID string) {
	for i, e := range b.displayFIFO {
		if e.reqID == reqID {
			b.displayFIFO = append(b.displayFIFO[:i], b.displayFIFO[i+1:]...)
			return
		}
	}
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
