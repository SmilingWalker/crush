package app

import (
	"context"
	"database/sql"
	"testing"
	"time"

	"github.com/charmbracelet/crush/internal/actor"
	"github.com/charmbracelet/crush/internal/config"
	"github.com/charmbracelet/crush/internal/permission"
	"github.com/charmbracelet/crush/internal/team"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestPermissionAudit_PersistsToDB is the M5-08a Task 5 E2E test: it proves
// that when the PermissionBridge fires a permission audit event, the callback
// the app registered in New() (SetAuditFunc → AppendAudit) lands a real row in
// the team_audit_events table.
//
// Path chosen: Path A (full bridge.Request flow). The bridge fires auditFn on
// the grant_auto path (an active grant matches the incoming request), which is
// the only Request path that fires auditFn synchronously without UI interaction.
// We seed a matching grant via the exported GrantStore() accessor, drive a real
// Request with a team actor context, then read the row back through ListAudit.
func TestPermissionAudit_PersistsToDB(t *testing.T) {
	// Build a real *App over a :memory: SQLite DB with goose migrations applied
	// (newTestDB sets SetMaxOpenConns(1) — required for :memory: fixtures).
	cfg := newTestConfig(t)
	cfg.Config().Options.Experimental = &config.ExperimentalOptions{AgentTeam: true}

	app, err := New(t.Context(), newTestDB(t), cfg, nil)
	require.NoError(t, err)
	require.NotNil(t, app.permBridge, "PermissionBridge should be wired in New()")
	require.NotNil(t, app.auditStore, "AuditStore should be wired in New()")

	// team_audit_events.team_id REFERENCES teams(id) with PRAGMA foreign_keys =
	// ON, so the audit row can only land if a team row exists. Seed one directly.
	const (
		teamID      = "team-audit-1"
		workspaceID = "ws"
		sessionID   = "sess-1"
		memberID    = "member-1"
		toolName    = "bash"
		action      = "execute"
	)
	seedTeam(t, app.db, teamID, workspaceID)

	bridge := app.PermBridge()
	require.NotNil(t, bridge)

	// Seed an active grant matching the request below. FindActiveGrant matches
	// on SessionID + ToolName + Action + non-expired ExpiresAt, so the Request
	// hits the grant_auto branch and fires auditFn synchronously.
	grant := &team.Grant{
		ID:        "grant-1",
		TeamID:    teamID,
		MemberID:  memberID,
		SessionID: sessionID,
		ToolName:  toolName,
		Action:    action,
		Scope:     "session",
		ExpiresAt: time.Now().Add(time.Hour),
		CreatedAt: time.Now(),
	}
	require.NoError(t, bridge.GrantStore().CreateGrant(t.Context(), grant))

	// Sanity: ListAudit is empty before any event.
	readAudit(t, app.db, app.auditStore, teamID, func(rows []team.AuditEvent) {
		assert.Empty(t, rows, "no audit rows before any event")
	})

	// Drive a real permission Request through the bridge with a team actor
	// context. The bridge finds the active grant and fires auditFn with
	// PermAuditGrantAuto, which the app's SetAuditFunc callback persists.
	ctx := actor.ActorContext{
		SessionID:  sessionID,
		TeamID:     teamID,
		MemberID:   memberID,
		MemberName: "test-member",
	}.WithContext(t.Context())

	allowed, err := bridge.Request(ctx, permission.CreatePermissionRequest{
		SessionID:  sessionID,
		ToolCallID: "call-1",
		ToolName:   toolName,
		Action:     action,
	})
	require.NoError(t, err)
	assert.True(t, allowed, "active grant should auto-allow the request")

	// Verify the audit row landed in team_audit_events.
	readAudit(t, app.db, app.auditStore, teamID, func(rows []team.AuditEvent) {
		require.Len(t, rows, 1, "exactly one audit row should be persisted")
		row := rows[0]
		assert.Equal(t, "permission.grant_auto", row.EventType)
		assert.Equal(t, teamID, row.TeamID)
		// app.New constructs the bridge with workspace "default"; the grant_auto
		// audit path stamps WorkspaceID from the bridge, so the row carries it.
		assert.Equal(t, "default", row.WorkspaceID)
		require.NotNil(t, row.SessionID)
		assert.Equal(t, sessionID, *row.SessionID)
		require.NotNil(t, row.ToolCallID)
		assert.Equal(t, "call-1", *row.ToolCallID)
		require.NotNil(t, row.Action)
		assert.Equal(t, "grant_auto", *row.Action)
		require.NotNil(t, row.Decision)
		assert.Equal(t, "allowed", *row.Decision)
	})
}

// seedTeam inserts a minimal teams row so team_audit_events' FK on team_id is
// satisfied. Uses its own tx (AppendAudit/ListAudit each open their own).
func seedTeam(t *testing.T, sqlDB *sql.DB, teamID, workspaceID string) {
	t.Helper()
	tx, err := sqlDB.BeginTx(context.Background(), nil)
	require.NoError(t, err)
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback()
		}
	}()
	now := time.Now().UnixMilli()
	_, err = tx.Exec(
		`INSERT INTO teams (id, workspace_id, leader_session_id, name, status, version, created_at, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		teamID, workspaceID, "leader-1", "Audit Test Team", string(team.TeamCreated), 1, now, now,
	)
	require.NoError(t, err)
	require.NoError(t, tx.Commit())
	committed = true
}

// readAudit runs ListAudit for teamID inside its own tx and calls assertFn
// with the rows. The AuditStore API requires a *sql.Tx; under
// SetMaxOpenConns(1) each tx must be fully closed before the next BeginTx, so
// the helper keeps Begin/Commit scoped to the read.
func readAudit(t *testing.T, sqlDB *sql.DB, store team.AuditStore, teamID string, assertFn func([]team.AuditEvent)) {
	t.Helper()
	tx, err := sqlDB.BeginTx(context.Background(), nil)
	require.NoError(t, err)
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback()
		}
	}()
	rows, err := store.ListAudit(context.Background(), tx, teamID, 10)
	require.NoError(t, err)
	require.NoError(t, tx.Commit())
	committed = true
	assertFn(rows)
}
