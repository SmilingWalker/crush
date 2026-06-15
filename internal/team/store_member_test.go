package team

import (
	"context"
	"database/sql"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestMemberStore_CreateGetListUpdateCAS(t *testing.T) {
	sqlDB, q := newStoreFixture(t)
	teams := NewTeamStore(q)
	members := NewMemberStore(q)
	ctx := context.Background()

	// Seed parent team (FK parent for team_members.team_id).
	runTx(t, sqlDB, func(tx *sql.Tx) error {
		_, err := teams.CreateTeam(ctx, tx, Team{ID: "t1", WorkspaceID: "ws", LeaderSessionID: "l", Name: "T", Status: TeamCreated, CreatedAt: time.UnixMilli(1), UpdatedAt: time.UnixMilli(1)})
		return err
	})

	var m TeamMember
	runTx(t, sqlDB, func(tx *sql.Tx) error {
		var err error
		m, err = members.CreateMember(ctx, tx, TeamMember{
			ID: "m1", TeamID: "t1", Name: "coder", Role: "programmer", AgentProfile: "{}",
			Status: MemberCreated, CreatedAt: time.UnixMilli(1), UpdatedAt: time.UnixMilli(1),
		})
		return err
	})
	assert.Equal(t, MemberCreated, m.Status)
	assert.Equal(t, 1, m.Version)

	var got TeamMember
	runTx(t, sqlDB, func(tx *sql.Tx) error {
		var err error
		got, err = members.GetMember(ctx, tx, "m1")
		return err
	})
	assert.Equal(t, "coder", got.Name)

	var listed []TeamMember
	runTx(t, sqlDB, func(tx *sql.Tx) error {
		var err error
		listed, err = members.ListMembers(ctx, tx, "t1")
		return err
	})
	assert.Len(t, listed, 1)

	// CAS with correct version (1) → bumps to 2.
	m.Status = MemberIdle
	var updated TeamMember
	runTx(t, sqlDB, func(tx *sql.Tx) error {
		var err error
		updated, err = members.UpdateMemberCAS(ctx, tx, m, 1)
		return err
	})
	assert.Equal(t, 2, updated.Version)
	assert.Equal(t, MemberIdle, updated.Status)

	// CAS with stale version (1 again, but row is now 2) → ErrMemberVersionConflict.
	runTxExpectErr(t, sqlDB, func(tx *sql.Tx) error {
		_, err := members.UpdateMemberCAS(ctx, tx, m, 1)
		return err
	}, ErrMemberVersionConflict)
}
