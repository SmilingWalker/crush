package team

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/charmbracelet/crush/internal/db"
)

// EventStore wraps the M3-02 team_events + team_event_counters queries.
// NextEventSeq allocates a per-team monotonic seq; AppendEvent writes the row
// at that seq; ListEventsAfter reads from a cursor. M3-05 typically calls
// NextEventSeq then AppendEvent within one tx to keep the seq gap-free.
type EventStore interface {
	NextEventSeq(ctx context.Context, tx *sql.Tx, teamID string, at time.Time) (int64, error)
	AppendEvent(ctx context.Context, tx *sql.Tx, event TeamEvent) error
	ListEventsAfter(ctx context.Context, tx *sql.Tx, teamID string, afterSeq int64, limit int) ([]TeamEvent, error)
}

type sqlcEventStore struct {
	q *db.Queries
}

func NewEventStore(q *db.Queries) EventStore {
	return &sqlcEventStore{q: q}
}

func (s *sqlcEventStore) NextEventSeq(ctx context.Context, tx *sql.Tx, teamID string, at time.Time) (int64, error) {
	qtx := s.q.WithTx(tx)
	return qtx.NextEventSeq(ctx, db.NextEventSeqParams{
		TeamID: teamID, UpdatedAt: at.UnixMilli(), UpdatedAt_2: at.UnixMilli(),
	})
}

func (s *sqlcEventStore) AppendEvent(ctx context.Context, tx *sql.Tx, e TeamEvent) error {
	qtx := s.q.WithTx(tx)
	return qtx.InsertEvent(ctx, db.InsertEventParams{
		Seq: e.Seq, ID: e.ID, WorkspaceID: e.WorkspaceID, TeamID: e.TeamID,
		EventType: e.EventType, EntityType: e.EntityType, EntityID: e.EntityID,
		ActorMemberID: strPtrToNullStr(e.ActorMemberID), TaskID: strPtrToNullStr(e.TaskID),
		RunID: strPtrToNullStr(e.RunID), MessageID: strPtrToNullStr(e.MessageID),
		PayloadJSON: strPtrToNullStr(e.PayloadJSON),
		PublishedAt: timePtrToNullInt64(e.PublishedAt), CreatedAt: e.CreatedAt.UnixMilli(),
	})
}

func (s *sqlcEventStore) ListEventsAfter(ctx context.Context, tx *sql.Tx, teamID string, afterSeq int64, limit int) ([]TeamEvent, error) {
	qtx := s.q.WithTx(tx)
	rows, err := qtx.ListEventsAfter(ctx, db.ListEventsAfterParams{TeamID: teamID, Seq: afterSeq, Limit: int64(limit)})
	if err != nil {
		return nil, fmt.Errorf("list events: %w", err)
	}
	out := make([]TeamEvent, 0, len(rows))
	for _, r := range rows {
		out = append(out, toTeamEvent(r))
	}
	return out, nil
}
