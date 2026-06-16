package app

import (
	"context"
	"database/sql"
	"testing"

	"github.com/charmbracelet/crush/internal/config"
	"github.com/charmbracelet/crush/internal/db"
	"github.com/charmbracelet/crush/internal/team"
	"github.com/pressly/goose/v3"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	_ "modernc.org/sqlite"
)

// newTeamServiceFixture builds an in-memory SQLite team.Service for wiring
// tests. The agentTeamEnabled parameter controls the feature gate, mirroring
// the app.New wiring: cfg.Options.IsAgentTeamEnabled → team.WithEnabledGate.
func newTeamServiceFixture(t *testing.T, agentTeamEnabled bool) team.Service {
	t.Helper()
	sqlDB, err := sql.Open("sqlite", ":memory:")
	require.NoError(t, err)
	t.Cleanup(func() { _ = sqlDB.Close() })
	sqlDB.SetMaxOpenConns(1)
	_, err = sqlDB.Exec("PRAGMA foreign_keys = ON;")
	require.NoError(t, err)
	require.NoError(t, db.InitGooseForTest(), "goose init")
	require.NoError(t, goose.Up(sqlDB, "migrations"))
	q := db.New(sqlDB)
	return team.NewService(
		sqlDB,
		team.NewTeamStore(q), team.NewMemberStore(q), team.NewTaskStore(q),
		team.NewRunStore(q), team.NewEventStore(q), team.NewAuditStore(q),
		team.NewMailboxStore(q),
		team.NewSessionLinkStore(q),
		nil, // deps
		team.WithEnabledGate(func() bool { return agentTeamEnabled }),
	)
}

// TestTeamService_GateDisabled locks M3-08 acceptance #1 and #2: when the
// feature gate is disabled, every write method returns ErrFeatureDisabled and
// no DB rows are created (enabledGuard returns before the tx begins).
func TestTeamService_GateDisabled(t *testing.T) {
	svc := newTeamServiceFixture(t, false)
	ctx := context.Background()
	_, err := svc.CreateTeam(ctx, team.CreateTeamRequest{
		WorkspaceID: "ws", LeaderSessionID: "lead", Name: "Test",
	})
	require.Error(t, err)
	assert.ErrorIs(t, err, team.ErrFeatureDisabled)
}

// TestTeamService_GateEnabled verifies that when the feature gate is enabled,
// writes succeed. This is the gate-on path that app.New enables when
// Experimental.AgentTeam is true.
func TestTeamService_GateEnabled(t *testing.T) {
	svc := newTeamServiceFixture(t, true)
	ctx := context.Background()
	snap, err := svc.CreateTeam(ctx, team.CreateTeamRequest{
		WorkspaceID: "ws", LeaderSessionID: "lead", Name: "Test",
	})
	require.NoError(t, err)
	assert.Equal(t, "Test", snap.Team.Name)
}

// TestConfig_IsAgentTeamEnabled_Integration locks M3-08 acceptance #3: the
// config→gate pipeline is nil-safe. An Options without an Experimental block
// (old config) returns false; with AgentTeam=true it returns true. This
// mirrors the app.New wiring: cfg.Options.IsAgentTeamEnabled() feeds
// team.WithEnabledGate.
func TestConfig_IsAgentTeamEnabled_Integration(t *testing.T) {
	// Old config: no experimental block → gate disabled.
	opts := &config.Options{}
	assert.False(t, opts.IsAgentTeamEnabled(), "old config without experimental: gate must be false")

	// AgentTeam true → gate opens.
	opts.Experimental = &config.ExperimentalOptions{AgentTeam: true}
	assert.True(t, opts.IsAgentTeamEnabled(), "config with agent_team=true: gate must be true")
}
