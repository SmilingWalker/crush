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
	q, _, ps := setupQueue(t)
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
