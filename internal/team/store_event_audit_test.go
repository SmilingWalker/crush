package team

import (
	"context"
	"database/sql"
	"strconv"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func newEventAuditFixture(t *testing.T) (*sql.DB, EventStore, AuditStore) {
	sqlDB, q := newStoreFixture(t)
	teams := NewTeamStore(q)
	ctx := context.Background()
	runTx(t, sqlDB, func(tx *sql.Tx) error {
		_, err := teams.CreateTeam(ctx, tx, Team{ID: "t1", WorkspaceID: "ws", LeaderSessionID: "l", Name: "T", Status: TeamCreated, CreatedAt: time.UnixMilli(1), UpdatedAt: time.UnixMilli(1)})
		return err
	})
	return sqlDB, NewEventStore(q), NewAuditStore(q)
}

func TestEventStore_NextEventSeq_Monotonic(t *testing.T) {
	sqlDB, events, _ := newEventAuditFixture(t)
	ctx := context.Background()

	// NextEventSeq first INSERTs (next_seq=2, returns 2), then each subsequent
	// call conflicts+increments: 3, 4, 5. (acceptance #4 analog: monotonic, no gap.)
	want := []int64{2, 3, 4, 5}
	for _, w := range want {
		var got int64
		runTx(t, sqlDB, func(tx *sql.Tx) error {
			var err error
			got, err = events.NextEventSeq(ctx, tx, "t1", time.UnixMilli(100))
			return err
		})
		assert.Equal(t, w, got, "NextEventSeq must be monotonic with no gaps")
	}
}

func TestEventStore_AppendAndList(t *testing.T) {
	sqlDB, events, _ := newEventAuditFixture(t)
	ctx := context.Background()

	// Allocate seqs 2..5 and append events at each.
	for _, seq := range []int64{2, 3, 4, 5} {
		runTx(t, sqlDB, func(tx *sql.Tx) error {
			return events.AppendEvent(ctx, tx, TeamEvent{
				Seq: seq, ID: "evt-" + strconv.FormatInt(seq, 10), WorkspaceID: "ws", TeamID: "t1",
				EventType: "team.updated", EntityType: "team", EntityID: "t1", CreatedAt: time.UnixMilli(seq * 10),
			})
		})
	}

	// ListEventsAfter(team-A, after=3) → seqs 4,5 in ASC order, LIMIT 10.
	var got []TeamEvent
	runTx(t, sqlDB, func(tx *sql.Tx) error {
		var err error
		got, err = events.ListEventsAfter(ctx, tx, "t1", 3, 10)
		return err
	})
	assert.Len(t, got, 2)
	assert.Equal(t, int64(4), got[0].Seq)
	assert.Equal(t, int64(5), got[1].Seq)

	// LIMIT caps the result.
	var capped []TeamEvent
	runTx(t, sqlDB, func(tx *sql.Tx) error {
		var err error
		capped, err = events.ListEventsAfter(ctx, tx, "t1", 1, 2)
		return err
	})
	assert.Len(t, capped, 2, "LIMIT 2 caps the result")
}

func TestAuditStore_AppendAndListDESC(t *testing.T) {
	sqlDB, _, audits := newEventAuditFixture(t)
	ctx := context.Background()

	// Append 3 audit rows at increasing created_at.
	for i, idx := range []int64{100, 200, 300} {
		_ = i
		runTx(t, sqlDB, func(tx *sql.Tx) error {
			return audits.AppendAudit(ctx, tx, AuditEvent{
				ID: "a" + strconv.FormatInt(idx, 10), WorkspaceID: "ws", TeamID: "t1",
				EventType: "tool.call", CreatedAt: time.UnixMilli(idx),
			})
		})
	}

	// ListAudit orders DESC by created_at → most recent first.
	var got []AuditEvent
	runTx(t, sqlDB, func(tx *sql.Tx) error {
		var err error
		got, err = audits.ListAudit(ctx, tx, "t1", 10)
		return err
	})
	assert.Len(t, got, 3)
	assert.Equal(t, "a300", got[0].ID, "most recent audit first (DESC)")
	assert.Equal(t, "a200", got[1].ID)
	assert.Equal(t, "a100", got[2].ID)

	// LIMIT caps.
	var capped []AuditEvent
	runTx(t, sqlDB, func(tx *sql.Tx) error {
		var err error
		capped, err = audits.ListAudit(ctx, tx, "t1", 2)
		return err
	})
	assert.Len(t, capped, 2)
	assert.Equal(t, "a300", capped[0].ID)
}
