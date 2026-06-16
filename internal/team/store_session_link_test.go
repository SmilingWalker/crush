package team

import (
	"context"
	"database/sql"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestSessionLinkStore_Create(t *testing.T) {
	sqlDB, q := newStoreFixture(t)
	teams := NewTeamStore(q)
	members := NewMemberStore(q)
	links := NewSessionLinkStore(q)
	ctx := context.Background()

	// Seed parent team + member for FK.
	runTx(t, sqlDB, func(tx *sql.Tx) error {
		_, err := teams.CreateTeam(ctx, tx, Team{ID: "t1-sl", WorkspaceID: "ws", LeaderSessionID: "l", Name: "T", Status: TeamCreated, CreatedAt: time.UnixMilli(1), UpdatedAt: time.UnixMilli(1)})
		if err != nil {
			return err
		}
		_, err = members.CreateMember(ctx, tx, TeamMember{
			ID: "m1-sl", TeamID: "t1-sl", Name: "coder", Role: "programmer", AgentProfile: "{}",
			Status: MemberCreated, CreatedAt: time.UnixMilli(1), UpdatedAt: time.UnixMilli(1),
		})
		return err
	})

	// Create session link.
	var created TeamSessionLink
	runTx(t, sqlDB, func(tx *sql.Tx) error {
		var err error
		created, err = links.CreateSessionLink(ctx, tx, TeamSessionLink{
			ID: "sl-1", TeamID: "t1-sl", MemberID: "m1-sl", SessionID: "sess-1",
			LinkType: "member", LinkedAt: time.UnixMilli(1000),
		})
		return err
	})
	assert.Equal(t, "sl-1", created.ID)
	assert.Equal(t, "t1-sl", created.TeamID)
	assert.Equal(t, "m1-sl", created.MemberID)
	assert.Equal(t, "sess-1", created.SessionID)
	assert.Equal(t, "member", created.LinkType)
}

func TestSessionLinkStore_GetByMember_Found(t *testing.T) {
	sqlDB, q := newStoreFixture(t)
	teams := NewTeamStore(q)
	members := NewMemberStore(q)
	links := NewSessionLinkStore(q)
	ctx := context.Background()

	runTx(t, sqlDB, func(tx *sql.Tx) error {
		_, err := teams.CreateTeam(ctx, tx, Team{ID: "t1-sl2", WorkspaceID: "ws", LeaderSessionID: "l", Name: "T", Status: TeamCreated, CreatedAt: time.UnixMilli(1), UpdatedAt: time.UnixMilli(1)})
		if err != nil {
			return err
		}
		_, err = members.CreateMember(ctx, tx, TeamMember{
			ID: "m1-sl2", TeamID: "t1-sl2", Name: "coder", Role: "programmer", AgentProfile: "{}",
			Status: MemberCreated, CreatedAt: time.UnixMilli(1), UpdatedAt: time.UnixMilli(1),
		})
		return err
	})

	runTx(t, sqlDB, func(tx *sql.Tx) error {
		_, err := links.CreateSessionLink(ctx, tx, TeamSessionLink{
			ID: "sl-2", TeamID: "t1-sl2", MemberID: "m1-sl2", SessionID: "sess-2",
			LinkType: "member", LinkedAt: time.UnixMilli(1000),
		})
		return err
	})

	var got TeamSessionLink
	runTx(t, sqlDB, func(tx *sql.Tx) error {
		var err error
		got, err = links.GetSessionLinkByMember(ctx, tx, "t1-sl2", "m1-sl2")
		return err
	})
	assert.Equal(t, "sl-2", got.ID)
	assert.Equal(t, "sess-2", got.SessionID)
}

func TestSessionLinkStore_GetByMember_NotFound(t *testing.T) {
	sqlDB, q := newStoreFixture(t)
	teams := NewTeamStore(q)
	members := NewMemberStore(q)
	links := NewSessionLinkStore(q)
	ctx := context.Background()

	runTx(t, sqlDB, func(tx *sql.Tx) error {
		_, err := teams.CreateTeam(ctx, tx, Team{ID: "t1-sl3", WorkspaceID: "ws", LeaderSessionID: "l", Name: "T", Status: TeamCreated, CreatedAt: time.UnixMilli(1), UpdatedAt: time.UnixMilli(1)})
		if err != nil {
			return err
		}
		_, err = members.CreateMember(ctx, tx, TeamMember{
			ID: "m1-sl3", TeamID: "t1-sl3", Name: "coder", Role: "programmer", AgentProfile: "{}",
			Status: MemberCreated, CreatedAt: time.UnixMilli(1), UpdatedAt: time.UnixMilli(1),
		})
		return err
	})

	// No link — should return zero-value with no error.
	var got TeamSessionLink
	runTx(t, sqlDB, func(tx *sql.Tx) error {
		var err error
		got, err = links.GetSessionLinkByMember(ctx, tx, "t1-sl3", "m1-sl3")
		return err
	})
	assert.Equal(t, "", got.ID)
	assert.Equal(t, "", got.SessionID)
}

func TestSessionLinkStore_GetByTeam(t *testing.T) {
	sqlDB, q := newStoreFixture(t)
	teams := NewTeamStore(q)
	members := NewMemberStore(q)
	links := NewSessionLinkStore(q)
	ctx := context.Background()

	runTx(t, sqlDB, func(tx *sql.Tx) error {
		_, err := teams.CreateTeam(ctx, tx, Team{ID: "t1-sl4", WorkspaceID: "ws", LeaderSessionID: "l", Name: "T", Status: TeamCreated, CreatedAt: time.UnixMilli(1), UpdatedAt: time.UnixMilli(1)})
		if err != nil {
			return err
		}
		_, err = members.CreateMember(ctx, tx, TeamMember{
			ID: "m1-sl4", TeamID: "t1-sl4", Name: "coder", Role: "programmer", AgentProfile: "{}",
			Status: MemberCreated, CreatedAt: time.UnixMilli(1), UpdatedAt: time.UnixMilli(1),
		})
		return err
	})

	runTx(t, sqlDB, func(tx *sql.Tx) error {
		_, err := links.CreateSessionLink(ctx, tx, TeamSessionLink{
			ID: "sl-4a", TeamID: "t1-sl4", MemberID: "m1-sl4", SessionID: "sess-4a",
			LinkType: "member", LinkedAt: time.UnixMilli(1000),
		})
		if err != nil {
			return err
		}
		_, err = links.CreateSessionLink(ctx, tx, TeamSessionLink{
			ID: "sl-4b", TeamID: "t1-sl4", MemberID: "m1-sl4", SessionID: "sess-4b",
			LinkType: "member", LinkedAt: time.UnixMilli(2000),
		})
		return err
	})

	var got []TeamSessionLink
	runTx(t, sqlDB, func(tx *sql.Tx) error {
		var err error
		got, err = links.GetSessionLinksByTeam(ctx, tx, "t1-sl4")
		return err
	})
	assert.Len(t, got, 2)
	// Most recent first (ORDER BY linked_at DESC)
	assert.Equal(t, "sess-4b", got[0].SessionID)
	assert.Equal(t, "sess-4a", got[1].SessionID)
}

func TestSessionLinkStore_FKCascade(t *testing.T) {
	sqlDB, q := newStoreFixture(t)
	teams := NewTeamStore(q)
	members := NewMemberStore(q)
	links := NewSessionLinkStore(q)
	ctx := context.Background()

	runTx(t, sqlDB, func(tx *sql.Tx) error {
		_, err := teams.CreateTeam(ctx, tx, Team{ID: "t1-fk", WorkspaceID: "ws", LeaderSessionID: "l", Name: "T", Status: TeamCreated, CreatedAt: time.UnixMilli(1), UpdatedAt: time.UnixMilli(1)})
		if err != nil {
			return err
		}
		_, err = members.CreateMember(ctx, tx, TeamMember{
			ID: "m1-fk", TeamID: "t1-fk", Name: "coder", Role: "programmer", AgentProfile: "{}",
			Status: MemberCreated, CreatedAt: time.UnixMilli(1), UpdatedAt: time.UnixMilli(1),
		})
		return err
	})

	runTx(t, sqlDB, func(tx *sql.Tx) error {
		_, err := links.CreateSessionLink(ctx, tx, TeamSessionLink{
			ID: "sl-fk", TeamID: "t1-fk", MemberID: "m1-fk", SessionID: "sess-fk",
			LinkType: "member", LinkedAt: time.UnixMilli(1000),
		})
		return err
	})

	// Delete the member — session link should cascade.
	runTx(t, sqlDB, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, "DELETE FROM team_members WHERE id = ?", "m1-fk")
		return err
	})

	// Verify link is gone.
	var count int
	runTx(t, sqlDB, func(tx *sql.Tx) error {
		return tx.QueryRowContext(ctx, "SELECT COUNT(*) FROM team_session_links WHERE id = ?", "sl-fk").Scan(&count)
	})
	assert.Equal(t, 0, count)
}
