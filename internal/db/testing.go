package db

// InitGooseForTest initializes goose (SetBaseFS + sqlite3 dialect) for use by
// out-of-package test fixtures that bypass Connect and call goose.Up directly
// on their own *sql.DB. It is safe to call multiple times (idempotent via the
// gooseInitOnce inside initGoose). Production code uses Connect, which calls
// initGoose internally; this is the exported entry point for cross-package
// tests (e.g. internal/team store tests in M3-04).
func InitGooseForTest() error {
	return initGoose()
}
