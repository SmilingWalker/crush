package team

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/charmbracelet/crush/internal/db"
)

// ErrMemberVersionConflict is returned by UpdateMemberCAS when the expected
// version does not match the row's current version (concurrent modification).
var ErrMemberVersionConflict = errors.New("version conflict: member was modified concurrently")

type MemberStore interface {
	CreateMember(ctx context.Context, tx *sql.Tx, member TeamMember) (TeamMember, error)
	GetMember(ctx context.Context, tx *sql.Tx, id string) (TeamMember, error)
	ListMembers(ctx context.Context, tx *sql.Tx, teamID string) ([]TeamMember, error)
	UpdateMemberCAS(ctx context.Context, tx *sql.Tx, member TeamMember, expectedVersion int) (TeamMember, error)
}

type sqlcMemberStore struct {
	q *db.Queries
}

func NewMemberStore(q *db.Queries) MemberStore {
	return &sqlcMemberStore{q: q}
}

func (s *sqlcMemberStore) CreateMember(ctx context.Context, tx *sql.Tx, m TeamMember) (TeamMember, error) {
	qtx := s.q.WithTx(tx)
	row, err := qtx.InsertMember(ctx, db.InsertMemberParams{
		ID: m.ID, TeamID: m.TeamID, SessionID: strPtrToNullStr(m.SessionID),
		Name: m.Name, Role: m.Role, AgentProfile: m.AgentProfile,
		ModelProvider: strPtrToNullStr(m.ModelProvider), ModelName: strPtrToNullStr(m.ModelName),
		CurrentTaskID: strPtrToNullStr(m.CurrentTaskID), CurrentRunID: strPtrToNullStr(m.CurrentRunID),
		CurrentToolName: strPtrToNullStr(m.CurrentToolName),
		MaxCost: int64PtrToNullInt64(m.MaxCost), MaxTokens: int64PtrToNullInt64(m.MaxTokens),
		CreatedAt: m.CreatedAt.UnixMilli(), UpdatedAt: m.UpdatedAt.UnixMilli(),
	})
	if err != nil {
		return TeamMember{}, fmt.Errorf("create member: %w", err)
	}
	return toTeamMember(row), nil
}

func (s *sqlcMemberStore) GetMember(ctx context.Context, tx *sql.Tx, id string) (TeamMember, error) {
	qtx := s.q.WithTx(tx)
	row, err := qtx.GetMember(ctx, id)
	if err != nil {
		return TeamMember{}, fmt.Errorf("get member: %w", err)
	}
	return toTeamMember(row), nil
}

func (s *sqlcMemberStore) ListMembers(ctx context.Context, tx *sql.Tx, teamID string) ([]TeamMember, error) {
	qtx := s.q.WithTx(tx)
	rows, err := qtx.ListMembers(ctx, teamID)
	if err != nil {
		return nil, fmt.Errorf("list members: %w", err)
	}
	out := make([]TeamMember, 0, len(rows))
	for _, r := range rows {
		out = append(out, toTeamMember(r))
	}
	return out, nil
}

func (s *sqlcMemberStore) UpdateMemberCAS(ctx context.Context, tx *sql.Tx, m TeamMember, expectedVersion int) (TeamMember, error) {
	qtx := s.q.WithTx(tx)
	row, err := qtx.UpdateMemberCAS(ctx, db.UpdateMemberCASParams{
		Status: string(m.Status), CurrentTaskID: strPtrToNullStr(m.CurrentTaskID),
		CurrentRunID: strPtrToNullStr(m.CurrentRunID), CurrentToolName: strPtrToNullStr(m.CurrentToolName),
		LastEventSeq: m.LastEventSeq, UpdatedAt: m.UpdatedAt.UnixMilli(),
		ID: m.ID, TeamID: m.TeamID, Version: int64(expectedVersion),
	})
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return TeamMember{}, ErrMemberVersionConflict
		}
		return TeamMember{}, fmt.Errorf("update member: %w", err)
	}
	return toTeamMember(row), nil
}
