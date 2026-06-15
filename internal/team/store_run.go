package team

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/charmbracelet/crush/internal/db"
)

type RunStore interface {
	StartRun(ctx context.Context, tx *sql.Tx, run TeamRun) (TeamRun, error)
	GetRun(ctx context.Context, tx *sql.Tx, teamID, runID string) (TeamRun, error)
	UpdateRunHeartbeat(ctx context.Context, tx *sql.Tx, runID string, at time.Time) error
	FinishRun(ctx context.Context, tx *sql.Tx, teamID, runID string, prompt, completion, cost int64, at time.Time) error
	MarkRunTerminal(ctx context.Context, tx *sql.Tx, teamID, runID string, status RunStatus, runErr string, usageStatus string, at time.Time) error
	FindStaleRuns(ctx context.Context, tx *sql.Tx, cutoff time.Time) ([]TeamRun, error)
}

type sqlcRunStore struct {
	q *db.Queries
}

func NewRunStore(q *db.Queries) RunStore {
	return &sqlcRunStore{q: q}
}

func (s *sqlcRunStore) StartRun(ctx context.Context, tx *sql.Tx, run TeamRun) (TeamRun, error) {
	qtx := s.q.WithTx(tx)
	row, err := qtx.InsertRun(ctx, db.InsertRunParams{
		ID: run.ID, TeamID: run.TeamID, MemberID: run.MemberID,
		TaskID: strPtrToNullStr(run.TaskID), SessionID: run.SessionID,
		HeartbeatAt: timePtrToNullInt64(run.HeartbeatAt), StartedAt: timePtrToNullInt64(run.StartedAt),
	})
	if err != nil {
		return TeamRun{}, fmt.Errorf("start run: %w", err)
	}
	return toTeamRun(row), nil
}

func (s *sqlcRunStore) GetRun(ctx context.Context, tx *sql.Tx, teamID, runID string) (TeamRun, error) {
	qtx := s.q.WithTx(tx)
	row, err := qtx.GetRun(ctx, db.GetRunParams{ID: runID, TeamID: teamID})
	if err != nil {
		return TeamRun{}, fmt.Errorf("get run: %w", err)
	}
	return toTeamRun(row), nil
}

func (s *sqlcRunStore) UpdateRunHeartbeat(ctx context.Context, tx *sql.Tx, runID string, at time.Time) error {
	qtx := s.q.WithTx(tx)
	return qtx.UpdateRunHeartbeat(ctx, db.UpdateRunHeartbeatParams{
		HeartbeatAt: sql.NullInt64{Int64: at.UnixMilli(), Valid: true}, ID: runID,
	})
}

func (s *sqlcRunStore) FinishRun(ctx context.Context, tx *sql.Tx, teamID, runID string, prompt, completion, cost int64, at time.Time) error {
	qtx := s.q.WithTx(tx)
	return qtx.FinishRun(ctx, db.FinishRunParams{
		FinishedAt:       sql.NullInt64{Int64: at.UnixMilli(), Valid: true},
		PromptTokens:     sql.NullInt64{Int64: prompt, Valid: true},
		CompletionTokens: sql.NullInt64{Int64: completion, Valid: true},
		CostMicros:       sql.NullInt64{Int64: cost, Valid: true},
		ID:               runID, TeamID: teamID,
	})
}

// MarkRunTerminal transitions a run to a terminal status. expectedStatus is the
// status the row must currently be in for the UPDATE to match (the M3-02
// MarkRunTerminal query guards on status = ?); the store hard-codes
// expectedStatus = RunRunning since terminal transitions are driven off a
// running run. (If M3-05 needs transitions from other source statuses, the
// signature widens to accept expectedStatus.)
func (s *sqlcRunStore) MarkRunTerminal(ctx context.Context, tx *sql.Tx, teamID, runID string, status RunStatus, runErr string, usageStatus string, at time.Time) error {
	qtx := s.q.WithTx(tx)
	return qtx.MarkRunTerminal(ctx, db.MarkRunTerminalParams{
		Status:      string(status),
		FinishedAt:  sql.NullInt64{Int64: at.UnixMilli(), Valid: true},
		Error:       sql.NullString{String: runErr, Valid: runErr != ""},
		UsageStatus: sql.NullString{String: usageStatus, Valid: usageStatus != ""},
		ID:          runID, TeamID: teamID,
		Status_2: string(RunRunning),
	})
}

func (s *sqlcRunStore) FindStaleRuns(ctx context.Context, tx *sql.Tx, cutoff time.Time) ([]TeamRun, error) {
	qtx := s.q.WithTx(tx)
	rows, err := qtx.FindStaleRuns(ctx, sql.NullInt64{Int64: cutoff.UnixMilli(), Valid: true})
	if err != nil {
		return nil, fmt.Errorf("find stale runs: %w", err)
	}
	out := make([]TeamRun, 0, len(rows))
	for _, r := range rows {
		out = append(out, toTeamRun(r))
	}
	return out, nil
}
