// store_dependency.go implements the DependencyStore — the persistence layer for
// the team_task_dependencies table (M4-11). It follows the same sqlcTx wrapper
// pattern as the other M4 stores (TaskStore, RunStore, MailboxStore).

package team

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/charmbracelet/crush/internal/db"
)

// DependencyStore persists task dependency edges and supports lookup in both
// directions: "what does task X depend on?" (GetDependencies) and "what tasks
// depend on task X?" (GetDependents). Both are used for cycle detection and
// cascade wake. GetTeamDependencies loads the full graph for a team (used in
// DFS cycle detection).
type DependencyStore interface {
	AddDependency(ctx context.Context, tx *sql.Tx, dep TeamTaskDependency) error
	RemoveDependency(ctx context.Context, tx *sql.Tx, taskID, dependsOnTaskID string) error
	GetDependencies(ctx context.Context, tx *sql.Tx, taskID string) ([]TeamTaskDependency, error)
	GetDependents(ctx context.Context, tx *sql.Tx, taskID string) ([]TeamTaskDependency, error)
	GetTeamDependencies(ctx context.Context, tx *sql.Tx, teamID string) ([]TeamTaskDependency, error)
}

type sqlcDependencyStore struct {
	q *db.Queries
}

func NewDependencyStore(q *db.Queries) DependencyStore {
	return &sqlcDependencyStore{q: q}
}

func (s *sqlcDependencyStore) AddDependency(ctx context.Context, tx *sql.Tx, dep TeamTaskDependency) error {
	qtx := s.q.WithTx(tx)
	return qtx.AddDependency(ctx, db.AddDependencyParams{
		TaskID:          dep.TaskID,
		DependsOnTaskID: dep.DependsOnTaskID,
		TeamID:          dep.TeamID,
		CreatedAt:       dep.CreatedAt.UnixMilli(),
	})
}

func (s *sqlcDependencyStore) RemoveDependency(ctx context.Context, tx *sql.Tx, taskID, dependsOnTaskID string) error {
	qtx := s.q.WithTx(tx)
	return qtx.RemoveDependency(ctx, db.RemoveDependencyParams{
		TaskID:          taskID,
		DependsOnTaskID: dependsOnTaskID,
	})
}

func (s *sqlcDependencyStore) GetDependencies(ctx context.Context, tx *sql.Tx, taskID string) ([]TeamTaskDependency, error) {
	qtx := s.q.WithTx(tx)
	rows, err := qtx.GetDependencies(ctx, taskID)
	if err != nil {
		return nil, fmt.Errorf("get dependencies: %w", err)
	}
	out := make([]TeamTaskDependency, 0, len(rows))
	for _, r := range rows {
		out = append(out, toTeamTaskDependency(r))
	}
	return out, nil
}

func (s *sqlcDependencyStore) GetDependents(ctx context.Context, tx *sql.Tx, taskID string) ([]TeamTaskDependency, error) {
	qtx := s.q.WithTx(tx)
	rows, err := qtx.GetDependents(ctx, taskID)
	if err != nil {
		return nil, fmt.Errorf("get dependents: %w", err)
	}
	out := make([]TeamTaskDependency, 0, len(rows))
	for _, r := range rows {
		out = append(out, toTeamTaskDependency(r))
	}
	return out, nil
}

func (s *sqlcDependencyStore) GetTeamDependencies(ctx context.Context, tx *sql.Tx, teamID string) ([]TeamTaskDependency, error) {
	qtx := s.q.WithTx(tx)
	rows, err := qtx.GetTeamDependencies(ctx, teamID)
	if err != nil {
		return nil, fmt.Errorf("get team dependencies: %w", err)
	}
	out := make([]TeamTaskDependency, 0, len(rows))
	for _, r := range rows {
		out = append(out, toTeamTaskDependency(r))
	}
	return out, nil
}
