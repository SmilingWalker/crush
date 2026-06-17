# Permission Queue + Timeout Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Implement PermissionQueue -- a bounded in-memory queue for pending permission requests with max-pending limit and auto-expiry timeout.

**Architecture:** PermissionQueue wraps PermissionFSM, holding a map of requestID to expiry timers. On Enqueue, a `time.AfterFunc` fires Expire on the FSM when the TTL elapses. On Dequeue (manual resolution), the timer is stopped. Queue capacity is checked before enqueuing.

**Tech Stack:** Go stdlib (sync.Mutex, time.Timer, context), testify for tests.

---

### Task 1: PermissionQueue implementation + tests

**Files:**
- Create: `internal/team/permission_queue.go`
- Create: `internal/team/permission_queue_test.go`

- [ ] **Step 1: Write permission_queue.go**

```go
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
	mu         sync.Mutex
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
		timer.Stop()
		delete(q.pending, reqID)
	}
}

// PendingCount returns the number of pending requests.
func (q *PermissionQueue) PendingCount() int {
	q.mu.Lock()
	defer q.mu.Unlock()
	return len(q.pending)
}

// IsFull returns true if the queue is at capacity.
func (q *PermissionQueue) IsFull() bool {
	return q.PendingCount() >= q.maxPending
}
```

- [ ] **Step 2: Write permission_queue_test.go**

```go
package team

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func setupQueue(t *testing.T) (*PermissionQueue, *PermissionFSM, *PermissionStore) {
	t.Helper()
	ps := NewPermissionStore()
	gs := NewGrantStore()
	var events []PermAuditEvent
	auditFn := func(ctx context.Context, e PermAuditEvent) { events = append(events, e) }
	fsm := NewPermissionFSM(ps, gs, auditFn)
	q := NewPermissionQueue(fsm)
	return q, fsm, ps
}

func TestQueue_EnqueueDequeue(t *testing.T) {
	q, _, ps := setupQueue(t)
	ctx := context.Background()

	req := &PermissionRequest{ID: "r1", Status: "pending", CreatedAt: time.Now()}
	ps.CreateRequest(ctx, req)

	err := q.Enqueue(ctx, req)
	require.NoError(t, err)
	assert.Equal(t, 1, q.PendingCount())

	q.Dequeue("r1")
	assert.Equal(t, 0, q.PendingCount())
}

func TestQueue_MaxPending(t *testing.T) {
	q, _, ps := setupQueue(t)
	ctx := context.Background()

	for i := 0; i < 3; i++ {
		id := fmt.Sprintf("r%d", i)
		req := &PermissionRequest{ID: id, Status: "pending", CreatedAt: time.Now()}
		ps.CreateRequest(ctx, req)
		err := q.Enqueue(ctx, req)
		require.NoError(t, err, "enqueue %d", i)
	}
	assert.True(t, q.IsFull())

	req4 := &PermissionRequest{ID: "r4", Status: "pending", CreatedAt: time.Now()}
	ps.CreateRequest(ctx, req4)
	err := q.Enqueue(ctx, req4)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "full")
}

func TestQueue_AutoExpire(t *testing.T) {
	q, fsm, ps := setupQueue(t)
	q.WithLimits(3, 50*time.Millisecond) // short TTL for test
	ctx := context.Background()

	req := &PermissionRequest{ID: "r-expire", Status: "pending", CreatedAt: time.Now()}
	ps.CreateRequest(ctx, req)
	require.NoError(t, q.Enqueue(ctx, req))

	time.Sleep(150 * time.Millisecond) // wait for expiry

	got, _ := ps.GetRequest(ctx, "r-expire")
	assert.Equal(t, "expired", got.Status)
	assert.Equal(t, 0, q.PendingCount())
}
```

- [ ] **Step 3: Run tests to verify they pass**

```bash
cd G:/ai-project/remote-github/crush-worktrees/m5-03-permission-queue && go build ./... && go test -count=1 -timeout 5s ./internal/team/... -run "TestQueue"
```

Expected: build passes, all 3 queue tests pass.

- [ ] **Step 4: Commit**

```bash
git add internal/team/permission_queue.go internal/team/permission_queue_test.go
git commit -m "feat(team): add PermissionQueue with max pending and auto-expiry for M5"
```

---

### Self-Review

**1. Spec coverage:**
- PermissionQueue struct with maxPending, defaultTTL, fsm, pending map, mu -- covered by Task 1 Step 1
- NewPermissionQueue constructor with defaults (3, 5min) -- covered
- WithLimits fluent setter -- covered
- Enqueue with capacity check + AfterFunc expiry timer -- covered
- Dequeue with timer.Stop -- covered
- PendingCount -- covered
- IsFull -- covered
- EnqueueDequeue test -- covered
- MaxPending test -- covered
- AutoExpire test -- covered

**2. Placeholder scan:** No TBD, TODO, or vague instructions. All code is concrete.

**3. Type consistency:**
- PermissionRequest.ID used consistently as string key
- PermissionFSM.Expire signature matches: `Expire(ctx context.Context, requestID string) error`
- PermissionStore.GetRequest returns `(*PermissionRequest, error)` -- used correctly
- Test imports include `"fmt"` (needed for `fmt.Sprintf` in MaxPending test) -- confirmed present
