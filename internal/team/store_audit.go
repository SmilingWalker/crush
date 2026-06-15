package team

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/charmbracelet/crush/internal/db"
)

// AuditStore wraps the team_audit_events queries. Audit is append-only (no
// Update/Delete). AppendAudit writes one row; ListAudit reads recent rows.
type AuditStore interface {
	AppendAudit(ctx context.Context, tx *sql.Tx, event AuditEvent) error
	ListAudit(ctx context.Context, tx *sql.Tx, teamID string, limit int) ([]AuditEvent, error)
}

type sqlcAuditStore struct {
	q *db.Queries
}

func NewAuditStore(q *db.Queries) AuditStore {
	return &sqlcAuditStore{q: q}
}

func (s *sqlcAuditStore) AppendAudit(ctx context.Context, tx *sql.Tx, e AuditEvent) error {
	qtx := s.q.WithTx(tx)
	return qtx.InsertAudit(ctx, db.InsertAuditParams{
		ID: e.ID, WorkspaceID: e.WorkspaceID, TeamID: e.TeamID,
		MemberID: strPtrToNullStr(e.MemberID), TaskID: strPtrToNullStr(e.TaskID),
		RunID: strPtrToNullStr(e.RunID), SessionID: strPtrToNullStr(e.SessionID),
		ToolCallID: strPtrToNullStr(e.ToolCallID), EventType: e.EventType,
		Action: strPtrToNullStr(e.Action), ResourceType: strPtrToNullStr(e.ResourceType),
		ResourceRef: strPtrToNullStr(e.ResourceRef), InputHash: strPtrToNullStr(e.InputHash),
		Summary: strPtrToNullStr(e.Summary), Decision: strPtrToNullStr(e.Decision),
		Scope: strPtrToNullStr(e.Scope), CreatedAt: e.CreatedAt.UnixMilli(),
	})
}

func (s *sqlcAuditStore) ListAudit(ctx context.Context, tx *sql.Tx, teamID string, limit int) ([]AuditEvent, error) {
	qtx := s.q.WithTx(tx)
	rows, err := qtx.ListAudit(ctx, db.ListAuditParams{TeamID: teamID, Limit: int64(limit)})
	if err != nil {
		return nil, fmt.Errorf("list audit: %w", err)
	}
	out := make([]AuditEvent, 0, len(rows))
	for _, r := range rows {
		out = append(out, toAuditEvent(r))
	}
	return out, nil
}
