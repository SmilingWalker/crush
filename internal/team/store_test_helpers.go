package team

import (
	"context"
	"database/sql"
	"testing"

	"github.com/charmbracelet/crush/internal/db"
	"github.com/pressly/goose/v3"
	"github.com/stretchr/testify/require"

	_ "modernc.org/sqlite"
)

// newStoreFixture returns an in-memory SQLite with FK enforcement on, all
// migrations applied, and a *db.Queries built via db.New() (the production
// construction path — NOT db.Prepare, which would compile the pre-existing
// broken ListNewFiles/is_new query; see plan Seam 6). Callers build stores
// from `queries` and pass `sqlDB` to BeginTx.
func newStoreFixture(t *testing.T) (*sql.DB, *db.Queries) {
	t.Helper()
	sqlDB, err := sql.Open("sqlite", ":memory:")
	require.NoError(t, err)
	t.Cleanup(func() { _ = sqlDB.Close() })

	_, err = sqlDB.Exec("PRAGMA foreign_keys = ON;")
	require.NoError(t, err)

	require.NoError(t, db.InitGooseForTest(), "goose init")
	require.NoError(t, goose.Up(sqlDB, "migrations"), "apply migrations up")

	return sqlDB, db.New(sqlDB)
}

// mustBegin is a convenience for store tests: open a tx, auto-rollback on cleanup.
func mustBegin(t *testing.T, sqlDB *sql.DB) *sql.Tx {
	t.Helper()
	tx, err := sqlDB.BeginTx(context.Background(), nil)
	require.NoError(t, err)
	t.Cleanup(func() { _ = tx.Rollback() })
	return tx
}
