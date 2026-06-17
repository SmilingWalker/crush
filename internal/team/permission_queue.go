package team

import (
	"context"
	"fmt"
	"sync"
	"time"
)

// PermissionQueue manages pending permission requests with limits and timeouts.
type PermissionQueue struct {
	maxPending int
	defaultTTL time.Duration
	fsm        *PermissionFSM
	pending    map[string]*time.Timer // requestID -> expiry timer
	mu         sync.RWMutex
}

// NewPermissionQueue creates a queue with defaults (max 3 pending, 5 min TTL).
func NewPermissionQueue(fsm *PermissionFSM) *PermissionQueue {
	return &PermissionQueue{
		maxPending: 3,
		defaultTTL: 5 * time.Minute,
		fsm:        fsm,
		pending:    make(map[string]*time.Timer),
	}
}

// WithLimits overrides the default limits.
func (q *PermissionQueue) WithLimits(maxPending int, ttl time.Duration) *PermissionQueue {
	q.maxPending = maxPending
	q.defaultTTL = ttl
	return q
}

// Enqueue adds a request to the queue and starts its expiry timer.
// Returns an error if the queue is full.
func (q *PermissionQueue) Enqueue(ctx context.Context, req *PermissionRequest) error {
	q.mu.Lock()
	defer q.mu.Unlock()

	if len(q.pending) >= q.maxPending {
		return fmt.Errorf("permission queue full: %d pending (max %d)", len(q.pending), q.maxPending)
	}

	// Start auto-expiry timer.
	timer := time.AfterFunc(q.defaultTTL, func() {
		q.fsm.Expire(ctx, req.ID)
		q.mu.Lock()
		delete(q.pending, req.ID)
		q.mu.Unlock()
	})

	q.pending[req.ID] = timer
	return nil
}

// Dequeue removes a request from the queue (called when resolved).
func (q *PermissionQueue) Dequeue(reqID string) {
	q.mu.Lock()
	defer q.mu.Unlock()
	if timer, ok := q.pending[reqID]; ok {
		if !timer.Stop() {
			// Timer already fired — its AfterFunc goroutine is either
			// running or about to run Expire. Since Expire is idempotent
			// (it checks Status != "pending"), we just skip the drain
			// and let it finish naturally.
		}
		delete(q.pending, reqID)
	}
}

// PendingCount returns the number of pending requests.
func (q *PermissionQueue) PendingCount() int {
	q.mu.RLock()
	defer q.mu.RUnlock()
	return len(q.pending)
}

// IsFull returns true if the queue is at capacity.
func (q *PermissionQueue) IsFull() bool {
	return q.PendingCount() >= q.maxPending
}
