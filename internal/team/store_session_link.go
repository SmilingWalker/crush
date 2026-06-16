package team

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/charmbracelet/crush/internal/db"
)

// SessionLinkStore is the persistence interface for team_session_links rows.
type SessionLinkStore interface {
	CreateSessionLink(ctx context.Context, tx *sql.Tx, link TeamSessionLink) (TeamSessionLink, error)
	GetSessionLinkByMember(ctx context.Context, tx *sql.Tx, teamID, memberID string) (TeamSessionLink, error)
	GetSessionLinksByTeam(ctx context.Context, tx *sql.Tx, teamID string) ([]TeamSessionLink, error)
}

type sqlcSessionLinkStore struct {
	q *db.Queries
}

// NewSessionLinkStore builds a SessionLinkStore backed by the given *db.Queries.
func NewSessionLinkStore(q *db.Queries) SessionLinkStore {
	return &sqlcSessionLinkStore{q: q}
}

func (s *sqlcSessionLinkStore) CreateSessionLink(ctx context.Context, tx *sql.Tx, link TeamSessionLink) (TeamSessionLink, error) {
	qtx := s.q.WithTx(tx)
	row, err := qtx.InsertSessionLink(ctx, db.InsertSessionLinkParams{
		ID:        link.ID,
		TeamID:    link.TeamID,
		MemberID:  link.MemberID,
		SessionID: link.SessionID,
		LinkType:  link.LinkType,
		LinkedAt:  link.LinkedAt.UnixMilli(),
	})
	if err != nil {
		return TeamSessionLink{}, fmt.Errorf("create session link: %w", err)
	}
	return toSessionLink(row), nil
}

func (s *sqlcSessionLinkStore) GetSessionLinkByMember(ctx context.Context, tx *sql.Tx, teamID, memberID string) (TeamSessionLink, error) {
	qtx := s.q.WithTx(tx)
	row, err := qtx.GetSessionLinkByMember(ctx, db.GetSessionLinkByMemberParams{
		TeamID:   teamID,
		MemberID: memberID,
	})
	if err != nil {
		if err == sql.ErrNoRows {
			return TeamSessionLink{}, nil
		}
		return TeamSessionLink{}, fmt.Errorf("get session link by member: %w", err)
	}
	return toSessionLink(row), nil
}

func (s *sqlcSessionLinkStore) GetSessionLinksByTeam(ctx context.Context, tx *sql.Tx, teamID string) ([]TeamSessionLink, error) {
	qtx := s.q.WithTx(tx)
	rows, err := qtx.GetSessionLinksByTeam(ctx, db.GetSessionLinksByTeamParams{TeamID: teamID})
	if err != nil {
		return nil, fmt.Errorf("get session links by team: %w", err)
	}
	out := make([]TeamSessionLink, 0, len(rows))
	for _, r := range rows {
		out = append(out, toSessionLink(r))
	}
	return out, nil
}
