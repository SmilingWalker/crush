package team

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/charmbracelet/crush/internal/db"
)

// ErrVersionConflict is returned by UpdateTaskCAS when the expected version
// does not match the row's current version (master doc :409).
var ErrVersionConflict = errors.New("version conflict: task was modified concurrently")

// ErrNoTaskAvailable is returned by ClaimNextTask when no queued task matches
// (master doc :410).
var ErrNoTaskAvailable = errors.New("no task available to claim")

// UpdateTaskCASRequest carries the inputs to UpdateTaskCAS. ExpectedVersion is
// the version the caller believes the row currently has; a mismatch yields
// ErrVersionConflict.
type UpdateTaskCASRequest struct {
	ID               string
	TeamID           string
	Status           TaskStatus
	AssigneeMemberID *string
	ResultSummary    *string
	UpdatedAt        time.Time
	ExpectedVersion  int
}

// ClaimNextTaskRequest carries the inputs to ClaimNextTask.
type ClaimNextTaskRequest struct {
	TeamID           string
	AssigneeMemberID string
	UpdatedAt        time.Time
}

type TaskStore interface {
	CreateTask(ctx context.Context, tx *sql.Tx, task TeamTask) (TeamTask, error)
	GetTask(ctx context.Context, tx *sql.Tx, teamID, taskID string) (TeamTask, error)
	UpdateTaskCAS(ctx context.Context, tx *sql.Tx, req UpdateTaskCASRequest) (TeamTask, error)
	ClaimNextTask(ctx context.Context, tx *sql.Tx, req ClaimNextTaskRequest) (TeamTask, error)
	ListTasks(ctx context.Context, tx *sql.Tx, teamID string) ([]TeamTask, error)
}

type sqlcTaskStore struct {
	q *db.Queries
}

func NewTaskStore(q *db.Queries) TaskStore {
	return &sqlcTaskStore{q: q}
}

func (s *sqlcTaskStore) CreateTask(ctx context.Context, tx *sql.Tx, task TeamTask) (TeamTask, error) {
	qtx := s.q.WithTx(tx)
	row, err := qtx.InsertTask(ctx, db.InsertTaskParams{
		ID: task.ID, TeamID: task.TeamID, Title: task.Title,
		Description:      strPtrToNullStr(strPtrOrNil(task.Description)),
		AssigneeMemberID: strPtrToNullStr(task.AssigneeMemberID),
		CreatedByMemberID: task.CreatedByMemberID, Priority: int64(task.Priority),
		CreatedAt: task.CreatedAt.UnixMilli(), UpdatedAt: task.UpdatedAt.UnixMilli(),
	})
	if err != nil {
		return TeamTask{}, fmt.Errorf("create task: %w", err)
	}
	return toTeamTask(row), nil
}

func (s *sqlcTaskStore) GetTask(ctx context.Context, tx *sql.Tx, teamID, taskID string) (TeamTask, error) {
	qtx := s.q.WithTx(tx)
	row, err := qtx.GetTask(ctx, db.GetTaskParams{ID: taskID, TeamID: teamID})
	if err != nil {
		return TeamTask{}, fmt.Errorf("get task: %w", err)
	}
	return toTeamTask(row), nil
}

func (s *sqlcTaskStore) UpdateTaskCAS(ctx context.Context, tx *sql.Tx, req UpdateTaskCASRequest) (TeamTask, error) {
	qtx := s.q.WithTx(tx)
	row, err := qtx.UpdateTaskCAS(ctx, db.UpdateTaskCASParams{
		Status: string(req.Status), AssigneeMemberID: strPtrToNullStr(req.AssigneeMemberID),
		ResultSummary: strPtrToNullStr(req.ResultSummary), UpdatedAt: req.UpdatedAt.UnixMilli(),
		ID: req.ID, TeamID: req.TeamID, Version: int64(req.ExpectedVersion),
	})
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return TeamTask{}, ErrVersionConflict
		}
		return TeamTask{}, fmt.Errorf("update task: %w", err)
	}
	return toTeamTask(row), nil
}

func (s *sqlcTaskStore) ClaimNextTask(ctx context.Context, tx *sql.Tx, req ClaimNextTaskRequest) (TeamTask, error) {
	qtx := s.q.WithTx(tx)
	assignee := req.AssigneeMemberID
	row, err := qtx.ClaimNextTask(ctx, db.ClaimNextTaskParams{
		TeamID: req.TeamID, AssigneeMemberID: sql.NullString{String: assignee, Valid: assignee != ""},
		UpdatedAt: req.UpdatedAt.UnixMilli(), TeamID_2: req.TeamID,
	})
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return TeamTask{}, ErrNoTaskAvailable
		}
		return TeamTask{}, fmt.Errorf("claim task: %w", err)
	}
	return toTeamTask(row), nil
}

func (s *sqlcTaskStore) ListTasks(ctx context.Context, tx *sql.Tx, teamID string) ([]TeamTask, error) {
	qtx := s.q.WithTx(tx)
	rows, err := qtx.ListTasks(ctx, teamID)
	if err != nil {
		return nil, fmt.Errorf("list tasks: %w", err)
	}
	out := make([]TeamTask, 0, len(rows))
	for _, r := range rows {
		out = append(out, toTeamTask(r))
	}
	return out, nil
}
