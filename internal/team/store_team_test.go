package team

import (
	"context"
	"database/sql"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestTeamStore_CreateGetListArchive(t *testing.T) {
	sqlDB, q := newStoreFixture(t)
	store := NewTeamStore(q)
	ctx := context.Background()

	tx := mustBegin(t, sqlDB)
	created, err := store.CreateTeam(ctx, tx, Team{
		ID: "team-A", WorkspaceID: "ws-1", LeaderSessionID: "lead-1", Name: "Alpha",
		Status: TeamCreated, CreatedAt: time.UnixMilli(1000), UpdatedAt: time.UnixMilli(1000),
	})
	require.NoError(t, err)
	require.NoError(t, tx.Commit())

	assert.Equal(t, TeamCreated, created.Status)
	assert.Equal(t, 1, created.Version)

	tx2 := mustBegin(t, sqlDB)
	got, err := store.GetTeam(ctx, tx2, "team-A")
	require.NoError(t, err)
	assert.Equal(t, "Alpha", got.Name)

	// Second team in same workspace + an archived one (excluded by ListTeams).
	_, err = store.CreateTeam(ctx, tx2, Team{
		ID: "team-B", WorkspaceID: "ws-1", LeaderSessionID: "lead-2", Name: "Bravo",
		Status: TeamCreated, CreatedAt: time.UnixMilli(2000), UpdatedAt: time.UnixMilli(2000),
	})
	require.NoError(t, err)
	require.NoError(t, tx2.Commit())

	tx3 := mustBegin(t, sqlDB)
	_, err = store.CreateTeam(ctx, tx3, Team{
		ID: "team-C", WorkspaceID: "ws-1", LeaderSessionID: "lead-3", Name: "Charlie",
		Status: TeamCreated, CreatedAt: time.UnixMilli(3000), UpdatedAt: time.UnixMilli(3000),
	})
	require.NoError(t, err)
	require.NoError(t, store.ArchiveTeam(ctx, tx3, "team-C", time.UnixMilli(4000)))
	require.NoError(t, tx3.Commit())

	tx4 := mustBegin(t, sqlDB)
	teams, err := store.ListTeams(ctx, tx4, "ws-1")
	require.NoError(t, err)
	assert.Len(t, teams, 2, "ListTeams excludes archived")
	for _, tm := range teams {
		assert.NotEqual(t, "team-C", tm.ID)
	}
}

func TestTeamStore_GetTeamMissingReturnsErrNoRows(t *testing.T) {
	sqlDB, q := newStoreFixture(t)
	store := NewTeamStore(q)
	tx := mustBegin(t, sqlDB)
	_, err := store.GetTeam(context.Background(), tx, "nope")
	require.Error(t, err)
	assert.ErrorIs(t, err, sql.ErrNoRows)
}
