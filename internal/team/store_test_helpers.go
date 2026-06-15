package team

import (
	"context"
	"database/sql"
	"errors"
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
// from `queries` and pass `sqlDB` to runTx.
func newStoreFixture(t *testing.T) (*sql.DB, *db.Queries) {
	t.Helper()
	sqlDB, err := sql.Open("sqlite", ":memory:")
	require.NoError(t, err)
	t.Cleanup(func() { _ = sqlDB.Close() })

	// :memory: SQLite databases are per-CONNECTION — without serializing all
	// access through a single connection, the migration would run on conn A
	// while a later tx lands on conn B (a fresh empty :memory: DB → "no such
	// table"). This mirrors the production connect.go:142 SetMaxOpenConns(1).
	sqlDB.SetMaxOpenConns(1)

	_, err = sqlDB.Exec("PRAGMA foreign_keys = ON;")
	require.NoError(t, err)

	require.NoError(t, db.InitGooseForTest(), "goose init")
	require.NoError(t, goose.Up(sqlDB, "migrations"), "apply migrations up")

	return sqlDB, db.New(sqlDB)
}

// runTx opens a transaction, runs fn with the tx, and commits if fn returns
// nil (rolling back on error or panic). This is the test analogue of the
// service-layer tx pattern in session.go/file.go: BeginTx + defer Rollback +
// explicit Commit in the same scope. Using a helper (rather than t.Cleanup on
// the raw tx) avoids the modernc awaitDone/conn-pool interaction where a
// t.Cleanup-deferred Rollback on an already-Committed tx lingers and, under
// SetMaxOpenConns(1), deadlocks a subsequent BeginTx.
func runTx(t *testing.T, sqlDB *sql.DB, fn func(tx *sql.Tx) error) {
	t.Helper()
	tx, err := sqlDB.BeginTx(context.Background(), nil)
	require.NoError(t, err)
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback()
		}
	}()
	if err := fn(tx); err != nil {
		_ = tx.Rollback()
		t.Fatalf("tx body failed: %v", err)
	}
	require.NoError(t, tx.Commit())
	committed = true
}

// runTxExpectErr runs fn inside a tx and asserts it returns an error matching
// want (via errors.Is). The tx is rolled back (the failing op is not committed).
func runTxExpectErr(t *testing.T, sqlDB *sql.DB, fn func(tx *sql.Tx) error, want error) {
	t.Helper()
	tx, err := sqlDB.BeginTx(context.Background(), nil)
	require.NoError(t, err)
	defer func() { _ = tx.Rollback() }()
	err = fn(tx)
	require.Error(t, err, "expected an error from tx body")
	if want != nil {
		require.ErrorIs(t, err, want, "expected error to wrap %v", want)
	}
}

var _ = errors.Is // keep errors import if future helpers need it
