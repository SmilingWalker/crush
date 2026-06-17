// active_session.go — M5.2: tracks which session the user is currently viewing
// in the TUI. PermissionBridge uses this to decide whether to pop up a permission
// dialog for team member tool calls.
package team

import "sync"

// ActiveSessionTracker is a concurrency-safe holder for the currently active
// session ID in the TUI. It is updated on session switch and read by
// PermissionBridge when a team member requests tool permission.
type ActiveSessionTracker struct {
	mu        sync.RWMutex
	sessionID string
}

// NewActiveSessionTracker returns an empty tracker.
func NewActiveSessionTracker() *ActiveSessionTracker {
	return &ActiveSessionTracker{}
}

// Set updates the active session ID.
func (t *ActiveSessionTracker) Set(sid string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.sessionID = sid
}

// Get returns the currently active session ID, or "" if none.
func (t *ActiveSessionTracker) Get() string {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return t.sessionID
}
