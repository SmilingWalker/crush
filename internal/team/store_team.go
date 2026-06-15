// store_team.go wraps the M3-02 team queries behind a domain-typed TeamStore
// interface. Each method takes a caller-supplied *sql.Tx (Seam 2), builds a
// tx-bound *db.Queries, runs the query, and converts the row to domain (Seam 3).

package team

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/charmbracelet/crush/internal/db"
)

// TeamStore is the persistence interface for teams rows. M3-05 TeamService
// consumes it; the sqlc-backed implementation is *sqlcTeamStore.
type TeamStore interface {
	CreateTeam(ctx context.Context, tx *sql.Tx, team Team) (Team, error)
	GetTeam(ctx context.Context, tx *sql.Tx, id string) (Team, error)
	ListTeams(ctx context.Context, tx *sql.Tx, workspaceID string) ([]Team, error)
	ArchiveTeam(ctx context.Context, tx *sql.Tx, id string, archivedAt time.Time) error
}

type sqlcTeamStore struct {
	q *db.Queries
}

// NewTeamStore builds a TeamStore backed by the given *db.Queries. The caller
// (M3-05) owns the *sql.DB and per-call *sql.Tx.
func NewTeamStore(q *db.Queries) TeamStore {
	return &sqlcTeamStore{q: q}
}

func (s *sqlcTeamStore) CreateTeam(ctx context.Context, tx *sql.Tx, team Team) (Team, error) {
	// InsertTeam forces status='created' and version=1 in the SQL; the caller's
	// team.Status/team.Version are ignored on insert (documented, not validated).
	qtx := s.q.WithTx(tx)
	row, err := qtx.InsertTeam(ctx, db.InsertTeamParams{
		ID:              team.ID,
		WorkspaceID:     team.WorkspaceID,
		LeaderSessionID: team.LeaderSessionID,
		Name:            team.Name,
		Description:     strPtrToNullStr(strPtrOrNil(team.Description)),
		MaxCost:         int64PtrToNullInt64(team.MaxCost),
		MaxTokens:       int64PtrToNullInt64(team.MaxTokens),
		CreatedAt:       team.CreatedAt.UnixMilli(),
		UpdatedAt:       team.UpdatedAt.UnixMilli(),
	})
	if err != nil {
		return Team{}, fmt.Errorf("create team: %w", err)
	}
	return toTeam(row), nil
}

func (s *sqlcTeamStore) GetTeam(ctx context.Context, tx *sql.Tx, id string) (Team, error) {
	qtx := s.q.WithTx(tx)
	row, err := qtx.GetTeam(ctx, id)
	if err != nil {
		return Team{}, fmt.Errorf("get team: %w", err)
	}
	return toTeam(row), nil
}

func (s *sqlcTeamStore) ListTeams(ctx context.Context, tx *sql.Tx, workspaceID string) ([]Team, error) {
	qtx := s.q.WithTx(tx)
	rows, err := qtx.ListTeams(ctx, workspaceID)
	if err != nil {
		return nil, fmt.Errorf("list teams: %w", err)
	}
	out := make([]Team, 0, len(rows))
	for _, r := range rows {
		out = append(out, toTeam(r))
	}
	return out, nil
}

func (s *sqlcTeamStore) ArchiveTeam(ctx context.Context, tx *sql.Tx, id string, archivedAt time.Time) error {
	qtx := s.q.WithTx(tx)
	if err := qtx.ArchiveTeam(ctx, db.ArchiveTeamParams{
		ArchivedAt: sql.NullInt64{Int64: archivedAt.UnixMilli(), Valid: true},
		UpdatedAt:  archivedAt.UnixMilli(),
		ID:         id,
	}); err != nil {
		return fmt.Errorf("archive team: %w", err)
	}
	return nil
}
