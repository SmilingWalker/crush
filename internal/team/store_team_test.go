package team

import (
	"context"
	"database/sql"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestTeamStore_CreateGetListArchive(t *testing.T) {
	sqlDB, q := newStoreFixture(t)
	store := NewTeamStore(q)
	ctx := context.Background()

	var created Team
	runTx(t, sqlDB, func(tx *sql.Tx) error {
		var err error
		created, err = store.CreateTeam(ctx, tx, Team{
			ID: "team-A", WorkspaceID: "ws-1", LeaderSessionID: "lead-1", Name: "Alpha",
			Status: TeamCreated, CreatedAt: time.UnixMilli(1000), UpdatedAt: time.UnixMilli(1000),
		})
		return err
	})
	assert.Equal(t, TeamCreated, created.Status)
	assert.Equal(t, 1, created.Version)

	var got Team
	runTx(t, sqlDB, func(tx *sql.Tx) error {
		var err error
		got, err = store.GetTeam(ctx, tx, "team-A")
		return err
	})
	assert.Equal(t, "Alpha", got.Name)

	// Second team in same workspace.
	runTx(t, sqlDB, func(tx *sql.Tx) error {
		_, err := store.CreateTeam(ctx, tx, Team{
			ID: "team-B", WorkspaceID: "ws-1", LeaderSessionID: "lead-2", Name: "Bravo",
			Status: TeamCreated, CreatedAt: time.UnixMilli(2000), UpdatedAt: time.UnixMilli(2000),
		})
		return err
	})

	// Third team, then archive it (excluded by ListTeams).
	runTx(t, sqlDB, func(tx *sql.Tx) error {
		if _, err := store.CreateTeam(ctx, tx, Team{
			ID: "team-C", WorkspaceID: "ws-1", LeaderSessionID: "lead-3", Name: "Charlie",
			Status: TeamCreated, CreatedAt: time.UnixMilli(3000), UpdatedAt: time.UnixMilli(3000),
		}); err != nil {
			return err
		}
		return store.ArchiveTeam(ctx, tx, "team-C", time.UnixMilli(4000))
	})

	var teams []Team
	runTx(t, sqlDB, func(tx *sql.Tx) error {
		var err error
		teams, err = store.ListTeams(ctx, tx, "ws-1")
		return err
	})
	assert.Len(t, teams, 2, "ListTeams excludes archived")
	for _, tm := range teams {
		assert.NotEqual(t, "team-C", tm.ID)
	}
}

func TestTeamStore_GetTeamMissingReturnsErrNoRows(t *testing.T) {
	sqlDB, q := newStoreFixture(t)
	store := NewTeamStore(q)
	runTxExpectErr(t, sqlDB, func(tx *sql.Tx) error {
		_, err := store.GetTeam(context.Background(), tx, "nope")
		return err
	}, sql.ErrNoRows)
}
