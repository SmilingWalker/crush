// active_session.go — M5.2: tracks which session the user is currently viewing
// in the TUI. PermissionBridge uses this to decide whether to pop up a permission
// dialog for team member tool calls.
//
// Lives in internal/team (not internal/ui/team) to avoid an import cycle:
// internal/ui/team already imports internal/team (for domain types).
package team

import "sync"

// ActiveSessionTracker is a concurrency-safe holder for the currently active
// session and member in the TUI. PermissionBridge uses this to decide whether
// to pop up a permission dialog for team member tool calls.
type ActiveSessionTracker struct {
	mu        sync.RWMutex
	sessionID string
	memberID  string // set when switching to a member whose session may not exist yet
}

// NewActiveSessionTracker returns an empty tracker.
func NewActiveSessionTracker() *ActiveSessionTracker {
	return &ActiveSessionTracker{}
}

// SetSession updates the active session ID.
func (t *ActiveSessionTracker) SetSession(sid, mid string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.sessionID = sid
	t.memberID = mid
}

// IsActiveSession returns true if the given session or member matches the
// currently active view. Member ID matching handles the case where the user
// switched to a member whose session hasn't been created yet (first wake).
func (t *ActiveSessionTracker) IsActiveSession(sessionID, memberID string) bool {
	t.mu.RLock()
	defer t.mu.RUnlock()
	if t.sessionID != "" && t.sessionID == sessionID {
		return true
	}
	if t.memberID != "" && t.memberID == memberID {
		return true
	}
	return false
}
