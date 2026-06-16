package team

import (
	"context"
	"database/sql"
	"testing"
	"time"

	"github.com/charmbracelet/crush/internal/db"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestRecipientType_Valid covers the 3 RecipientType enum values + invalid.
func TestRecipientType_Valid(t *testing.T) {
	for _, valid := range allRecipientTypes {
		assert.True(t, valid.Valid(), "expected %q to be valid", valid)
	}
	assert.False(t, RecipientType("").Valid(), "empty string should be invalid")
	assert.False(t, RecipientType("unknown").Valid(), "unknown should be invalid")
}

// TestMessageKind_Valid covers the 5 MessageKind enum values + invalid.
func TestMessageKind_Valid(t *testing.T) {
	for _, valid := range allMessageKinds {
		assert.True(t, valid.Valid(), "expected %q to be valid", valid)
	}
	assert.False(t, MessageKind("").Valid(), "empty string should be invalid")
	assert.False(t, MessageKind("unknown").Valid(), "unknown should be invalid")
}

// seedTeamAndMembers creates a team + 3 members in one tx. Returns teamID and
// member IDs ([leader, programmer, reviewer]). FK parents required for mailbox.
func seedTeamAndMembers(t *testing.T, sqlDB *sql.DB, q *db.Queries) (string, string, string, string) {
	t.Helper()
	teamID := "t-" + uuid.New().String()[:8]
	m1 := "m-" + uuid.New().String()[:8]
	m2 := "m-" + uuid.New().String()[:8]
	m3 := "m-" + uuid.New().String()[:8]

	teams := NewTeamStore(q)
	members := NewMemberStore(q)

	runTx(t, sqlDB, func(tx *sql.Tx) error {
		_, err := teams.CreateTeam(context.Background(), tx, Team{
			ID: teamID, WorkspaceID: "ws", LeaderSessionID: "ls", Name: "TestTeam",
			Status: TeamCreated, CreatedAt: time.UnixMilli(1), UpdatedAt: time.UnixMilli(1),
		})
		return err
	})
	for _, spec := range []struct {
		id, name, role string
	}{
		{m1, "leader", "leader"},
		{m2, "coder", "programmer"},
		{m3, "reviewer", "reviewer"},
	} {
		runTx(t, sqlDB, func(tx *sql.Tx) error {
			_, err := members.CreateMember(context.Background(), tx, TeamMember{
				ID: spec.id, TeamID: teamID, Name: spec.name, Role: spec.role,
				AgentProfile: "{}", Status: MemberIdle,
				CreatedAt: time.UnixMilli(1), UpdatedAt: time.UnixMilli(1),
			})
			return err
		})
	}
	return teamID, m1, m2, m3
}

// TestMailboxStore_InsertAndGetUnread covers InsertMessage + GetUnreadMessages.
func TestMailboxStore_InsertAndGetUnread(t *testing.T) {
	sqlDB, q := newStoreFixture(t)
	store := NewMailboxStore(q)
	teamID, fromID, toID, _ := seedTeamAndMembers(t, sqlDB, q)
	ctx := context.Background()
	ts := time.Now()

	// Insert message
	var msg MailboxMessage
	runTx(t, sqlDB, func(tx *sql.Tx) error {
		var err error
		msg, err = store.InsertMessage(ctx, tx, MailboxMessage{
			ID: "msg-1", TeamID: teamID, FromMemberID: fromID,
			Kind: KindMessage, Summary: "hello", Payload: "{}",
			CreatedAt: ts,
		})
		return err
	})
	assert.Equal(t, "msg-1", msg.ID)
	assert.Equal(t, KindMessage, msg.Kind)

	// Insert receipt (not yet delivered/read)
	runTx(t, sqlDB, func(tx *sql.Tx) error {
		return store.InsertReceipt(ctx, tx, MessageReceipt{
			ID: "rec-1", MessageID: "msg-1", ToMemberID: toID,
		})
	})

	// Message should show as unread for toID
	var unread []MailboxMessage
	runTx(t, sqlDB, func(tx *sql.Tx) error {
		var err error
		unread, err = store.GetUnreadMessages(ctx, tx, toID, 10)
		return err
	})
	assert.Len(t, unread, 1)
	assert.Equal(t, "msg-1", unread[0].ID)

	// Mark read → unread should return empty
	runTx(t, sqlDB, func(tx *sql.Tx) error {
		return store.MarkRead(ctx, tx, "msg-1", toID, ts)
	})
	runTx(t, sqlDB, func(tx *sql.Tx) error {
		var err error
		unread, err = store.GetUnreadMessages(ctx, tx, toID, 10)
		return err
	})
	assert.Len(t, unread, 0)
}

// TestMailboxStore_MarkDelivered covers delivered/read tracking.
func TestMailboxStore_MarkDelivered(t *testing.T) {
	sqlDB, q := newStoreFixture(t)
	store := NewMailboxStore(q)
	teamID, fromID, toID, _ := seedTeamAndMembers(t, sqlDB, q)
	ctx := context.Background()
	ts := time.Now()

	// Insert message + receipt
	runTx(t, sqlDB, func(tx *sql.Tx) error {
		_, err := store.InsertMessage(ctx, tx, MailboxMessage{
			ID: "msg-2", TeamID: teamID, FromMemberID: fromID,
			Kind: KindTaskAssignment, Summary: "task", Payload: `{"task":"abc"}`,
			CreatedAt: ts,
		})
		if err != nil {
			return err
		}
		return store.InsertReceipt(ctx, tx, MessageReceipt{
			ID: "rec-2", MessageID: "msg-2", ToMemberID: toID,
		})
	})

	// Mark delivered
	deliverTS := ts.Add(time.Second)
	runTx(t, sqlDB, func(tx *sql.Tx) error {
		return store.MarkDelivered(ctx, tx, "msg-2", toID, deliverTS)
	})

	// Verify delivered_at was set (via direct query)
	var deliveredAt sql.NullInt64
	err := sqlDB.QueryRow(`SELECT delivered_at FROM team_message_receipts WHERE id = 'rec-2'`).Scan(&deliveredAt)
	require.NoError(t, err)
	assert.True(t, deliveredAt.Valid)
	assert.Equal(t, deliverTS.UnixMilli(), deliveredAt.Int64)

	// Mark read
	readTS := ts.Add(2 * time.Second)
	runTx(t, sqlDB, func(tx *sql.Tx) error {
		return store.MarkRead(ctx, tx, "msg-2", toID, readTS)
	})

	var readAt sql.NullInt64
	err = sqlDB.QueryRow(`SELECT read_at FROM team_message_receipts WHERE id = 'rec-2'`).Scan(&readAt)
	require.NoError(t, err)
	assert.True(t, readAt.Valid)
	assert.Equal(t, readTS.UnixMilli(), readAt.Int64)
}

// TestSendMessage_Direct sends a direct message and verifies 1 receipt.
func TestSendMessage_Direct(t *testing.T) {
	sqlDB, q := newStoreFixture(t)
	teamID, fromID, toID, _ := seedTeamAndMembers(t, sqlDB, q)
	svc := NewService(
		sqlDB,
		NewTeamStore(q), NewMemberStore(q), NewTaskStore(q),
		NewRunStore(q), NewEventStore(q), NewAuditStore(q),
		NewMailboxStore(q), NewDependencyStore(q),
		WithEnabledGate(func() bool { return true }),
	)
	ctx := context.Background()

	recipientIDs, err := svc.SendMessage(ctx, SendMessageRequest{
		TeamID: teamID, FromMemberID: fromID, RecipientType: RecipientDirect,
		ToMemberID: toID, Kind: KindMessage, Summary: "direct msg", Payload: "{}",
	})
	require.NoError(t, err)
	assert.Equal(t, []string{toID}, recipientIDs)

	// Verify unread for toID
	unread, err := svc.GetUnreadMessages(ctx, toID, 10)
	require.NoError(t, err)
	assert.Len(t, unread, 1)
	assert.Equal(t, KindMessage, unread[0].Kind)
}

// TestSendMessage_Broadcast sends a broadcast and verifies all other members get receipts.
func TestSendMessage_Broadcast(t *testing.T) {
	sqlDB, q := newStoreFixture(t)
	teamID, fromID, m2, m3 := seedTeamAndMembers(t, sqlDB, q)
	svc := NewService(
		sqlDB,
		NewTeamStore(q), NewMemberStore(q), NewTaskStore(q),
		NewRunStore(q), NewEventStore(q), NewAuditStore(q),
		NewMailboxStore(q), NewDependencyStore(q),
		WithEnabledGate(func() bool { return true }),
	)
	ctx := context.Background()

	recipientIDs, err := svc.SendMessage(ctx, SendMessageRequest{
		TeamID: teamID, FromMemberID: fromID, RecipientType: RecipientBroadcast,
		Kind: KindTaskAssignment, Summary: "broadcast", Payload: "{}",
	})
	require.NoError(t, err)
	// Should include m2 and m3 (both non-sender members)
	assert.Len(t, recipientIDs, 2)
	assert.Contains(t, recipientIDs, m2)
	assert.Contains(t, recipientIDs, m3)
	assert.NotContains(t, recipientIDs, fromID)

	// Both recipients have unread
	for _, mid := range []string{m2, m3} {
		unread, err := svc.GetUnreadMessages(ctx, mid, 10)
		require.NoError(t, err)
		assert.Len(t, unread, 1, "member %s should have 1 unread", mid)
	}

	// Sender has no receipt → no unread (direct table read test)
	unread, err := svc.GetUnreadMessages(ctx, fromID, 10)
	require.NoError(t, err)
	assert.Len(t, unread, 0, "sender should have no unread from own broadcast")
}

// TestSendMessage_Role sends to a specific role and verifies only matching members get receipts.
func TestSendMessage_Role(t *testing.T) {
	sqlDB, q := newStoreFixture(t)
	teamID, fromID, m2, m3 := seedTeamAndMembers(t, sqlDB, q)
	svc := NewService(
		sqlDB,
		NewTeamStore(q), NewMemberStore(q), NewTaskStore(q),
		NewRunStore(q), NewEventStore(q), NewAuditStore(q),
		NewMailboxStore(q), NewDependencyStore(q),
		WithEnabledGate(func() bool { return true }),
	)
	ctx := context.Background()

	// Send to role="programmer" (only m2 is a programmer)
	recipientIDs, err := svc.SendMessage(ctx, SendMessageRequest{
		TeamID: teamID, FromMemberID: fromID, RecipientType: RecipientRole,
		ToRole: "programmer", Kind: KindMessage, Summary: "role msg", Payload: "{}",
	})
	require.NoError(t, err)
	assert.Len(t, recipientIDs, 1)
	assert.Equal(t, m2, recipientIDs[0])
	assert.NotContains(t, recipientIDs, m3) // reviewer, not programmer
	assert.NotContains(t, recipientIDs, fromID)

	// m2 has unread, m3 does not
	unread, err := svc.GetUnreadMessages(ctx, m2, 10)
	require.NoError(t, err)
	assert.Len(t, unread, 1)
	unread, err = svc.GetUnreadMessages(ctx, m3, 10)
	require.NoError(t, err)
	assert.Len(t, unread, 0)
}

// TestSendMessage_GateOff tests that SendMessage returns ErrFeatureDisabled.
func TestSendMessage_GateOff(t *testing.T) {
	sqlDB, q := newStoreFixture(t)
	svc := NewService(
		sqlDB,
		NewTeamStore(q), NewMemberStore(q), NewTaskStore(q),
		NewRunStore(q), NewEventStore(q), NewAuditStore(q),
		NewMailboxStore(q), NewDependencyStore(q),
		// NO WithEnabledGate
	)
	ctx := context.Background()
	_, err := svc.SendMessage(ctx, SendMessageRequest{
		TeamID: "t1", FromMemberID: "m1",
	})
	require.ErrorIs(t, err, ErrFeatureDisabled)
}

// TestMailbox_ConvertRoundTrip ensures domain ↔ db conversion is lossless.
func TestMailbox_ConvertRoundTrip(t *testing.T) {
	ts := time.UnixMilli(1000)
	dbMsg := db.TeamMailboxMessage{
		ID: "m1", TeamID: "t1", FromMemberID: "f1",
		Kind: "message", Summary: "s1", Payload: "{}", CreatedAt: 1000,
	}
	dm := toMailboxMessage(dbMsg)
	assert.Equal(t, "m1", dm.ID)
	assert.Equal(t, KindMessage, dm.Kind)
	assert.Equal(t, ts, dm.CreatedAt)

	dbReceipt := db.TeamMessageReceipt{
		ID: "r1", MessageID: "m1", ToMemberID: "t1",
		DeliveredAt: sql.NullInt64{Int64: 2000, Valid: true},
		ReadAt:      sql.NullInt64{},
	}
	dr := toMessageReceipt(dbReceipt)
	assert.Equal(t, "r1", dr.ID)
	assert.NotNil(t, dr.DeliveredAt)
	assert.Equal(t, time.UnixMilli(2000), *dr.DeliveredAt)
	assert.Nil(t, dr.ReadAt)
}
