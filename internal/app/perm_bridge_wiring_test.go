package app

import (
	"context"
	"database/sql"
	"testing"
	"time"

	"github.com/charmbracelet/crush/internal/actor"
	"github.com/charmbracelet/crush/internal/config"
	"github.com/charmbracelet/crush/internal/csync"
	"github.com/charmbracelet/crush/internal/db"
	"github.com/charmbracelet/crush/internal/permission"
	"github.com/charmbracelet/crush/internal/team"
	"github.com/pressly/goose/v3"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	_ "modernc.org/sqlite"
)

// newTestConfig returns a ConfigStore with no providers so that IsConfigured()
// returns false -- this skips InitCoderAgent in app.New, letting us test wiring
// without needing a full agent configuration.
func newTestConfig(t *testing.T) *config.ConfigStore {
	t.Helper()
	return config.NewTestStore(&config.Config{
		Options: &config.Options{
			DataDirectory: t.TempDir(),
		},
		Providers: csync.NewMap[string, config.ProviderConfig](),
	})
}

// newTestDB creates an in-memory SQLite database with goose migrations applied.
func newTestDB(t *testing.T) *sql.DB {
	t.Helper()
	mdb, err := sql.Open("sqlite", ":memory:")
	require.NoError(t, err)
	t.Cleanup(func() { _ = mdb.Close() })
	mdb.SetMaxOpenConns(1)
	_, err = mdb.Exec("PRAGMA foreign_keys = ON;")
	require.NoError(t, err)
	require.NoError(t, db.InitGooseForTest(), "goose init")
	require.NoError(t, goose.Up(mdb, "migrations"))
	return mdb
}

// TestPermissionBridge_WiredIntoApp verifies the M5 PermissionBridge is
// created and accessible from the App struct after New().
func TestPermissionBridge_WiredIntoApp(t *testing.T) {
	cfg := newTestConfig(t)
	cfg.Config().Options.Experimental = &config.ExperimentalOptions{AgentTeam: true}

	app, err := New(t.Context(), newTestDB(t), cfg, nil)
	require.NoError(t, err)
	require.NotNil(t, app.permBridge, "PermissionBridge should be wired in New()")
}

// TestPermissionBridge_DelegatesToInner verifies the PermissionBridge
// delegates non-team permission operations to the inner permission.Service.
// We test via SetSkipRequests/SkipRequests which are transparently delegated.
func TestPermissionBridge_DelegatesToInner(t *testing.T) {
	cfg := newTestConfig(t)
	cfg.Config().Options.Experimental = &config.ExperimentalOptions{AgentTeam: true}

	app, err := New(t.Context(), newTestDB(t), cfg, nil)
	require.NoError(t, err)
	require.NotNil(t, app.permBridge)

	// Toggle skip via the bridge -- delegates to inner service.
	assert.False(t, app.permBridge.SkipRequests())

	app.permBridge.SetSkipRequests(true)
	assert.True(t, app.permBridge.SkipRequests())

	app.permBridge.SetSkipRequests(false)
	assert.False(t, app.permBridge.SkipRequests())
}

// TestPermissionBridge_ImplementsPermissionService is a compile-time
// verification that the PermissionBridge satisfies the permission.Service
// interface. This confirms the bridge can be passed wherever the raw
// permission.Service is expected (notably agent.NewCoordinator).
func TestPermissionBridge_ImplementsPermissionService(t *testing.T) {
	cfg := newTestConfig(t)
	app, err := New(t.Context(), newTestDB(t), cfg, nil)
	require.NoError(t, err)
	require.NotNil(t, app.permBridge)

	// Interface satisfaction check: if this compiles, the bridge
	// implements permission.Service.
	var svc permission.Service = app.permBridge
	assert.NotNil(t, svc)
}

// TestPermissionBridge_TeamSessionIntercepted verifies that when a team member
// calls a tool, the PermissionBridge intercepts the request and routes it
// through the team permission flow (grant check → enqueue → wait for UI)
// instead of delegating to the inner permission.Service.
//
// We detect interception via the audit callback: the bridge fires
// PermAuditPermissionRequested only when it enters the team-aware path.
func TestPermissionBridge_TeamSessionIntercepted(t *testing.T) {
	cfg := newTestConfig(t)
	cfg.Config().Options.Experimental = &config.ExperimentalOptions{AgentTeam: true}

	app, err := New(t.Context(), newTestDB(t), cfg, nil)
	require.NoError(t, err)
	require.NotNil(t, app.permBridge)

	// Replace the audit function with one that signals on a channel
	// when the bridge enters the team permission path.
	auditCh := make(chan team.PermAuditEvent, 1)
	app.permBridge.SetAuditFunc(func(_ context.Context, e team.PermAuditEvent) {
		auditCh <- e
	})

	// Inject actor context with team info -- this triggers the bridge's
	// team path (grant check → enqueue → wait for UI).
	ac := actor.ActorContext{
		SessionID:  "session-1",
		TeamID:     "team-1",
		MemberID:   "member-1",
		MemberName: "test-member",
	}
	teamCtx := ac.WithContext(t.Context())

	// Call Request in a goroutine -- it will block waiting for UI because
	// there are no active grants and nobody calls ResolveRequest. The bridge's
	// select only checks <-ch and <-time.After(5min), so this goroutine
	// will stay blocked for up to 5 minutes (cleaned up when test exits).
	go func() {
		_, _ = app.permBridge.Request(teamCtx, permission.CreatePermissionRequest{
			SessionID:   "session-1",
			ToolCallID:  "call-team-1",
			ToolName:    "bash",
			Action:      "run",
			Description: "team command",
			Path:        ".",
		})
	}()

	// Wait for the audit event proving the bridge intercepted the request.
	select {
	case ev := <-auditCh:
		assert.Equal(t, team.PermAuditPermissionRequested, ev.Action,
			"bridge should fire permission_requested for team sessions")
		assert.Equal(t, "team-1", ev.TeamID)
		assert.Equal(t, "member-1", ev.MemberID)
		assert.Equal(t, "bash", ev.ToolName)
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for bridge to intercept team request -- " +
			"request may have been delegated to inner or audit not fired")
	}
}
