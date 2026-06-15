// store_mailbox.go is the M4-04 MailboxStore: per-message persistence for
// team_mailbox_messages + team_message_receipts. The store is message-centric:
// InsertMessage creates the message row; InsertReceipt creates delivery-tracking
// rows per recipient; GetUnreadMessages returns messages whose receipts are not
// yet marked read; MarkDelivered/MarkRead update receipt status.
//
// Follows the same sqlc-backed store pattern as M3-04 TaskStore/MemberStore:
// NewMailboxStore(q *db.Queries) constructs the store; each method acquires its
// tx view via q.WithTx(tx); db↔domain conversion lives in convert.go; row-level
// errors are wrapped with fmt.Errorf.

package team

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/charmbracelet/crush/internal/db"
)

// MailboxStore is the persistence interface for team_mailbox_messages and
// team_message_receipts. M4-04 master doc :502-507.
type MailboxStore interface {
	InsertMessage(ctx context.Context, tx *sql.Tx, msg MailboxMessage) (MailboxMessage, error)
	GetUnreadMessages(ctx context.Context, tx *sql.Tx, memberID string, limit int) ([]MailboxMessage, error)
	InsertReceipt(ctx context.Context, tx *sql.Tx, receipt MessageReceipt) error
	MarkDelivered(ctx context.Context, tx *sql.Tx, messageID, memberID string, at time.Time) error
	MarkRead(ctx context.Context, tx *sql.Tx, messageID, memberID string, at time.Time) error
}

type sqlcMailboxStore struct {
	q *db.Queries
}

func NewMailboxStore(q *db.Queries) MailboxStore {
	return &sqlcMailboxStore{q: q}
}

func (s *sqlcMailboxStore) InsertMessage(ctx context.Context, tx *sql.Tx, msg MailboxMessage) (MailboxMessage, error) {
	qtx := s.q.WithTx(tx)
	row, err := qtx.InsertMessage(ctx, db.InsertMessageParams{
		ID: msg.ID, TeamID: msg.TeamID, FromMemberID: msg.FromMemberID,
		Kind: string(msg.Kind), Summary: msg.Summary, Payload: msg.Payload,
		CreatedAt: msg.CreatedAt.UnixMilli(),
	})
	if err != nil {
		return MailboxMessage{}, fmt.Errorf("insert message: %w", err)
	}
	return toMailboxMessage(row), nil
}

func (s *sqlcMailboxStore) GetUnreadMessages(ctx context.Context, tx *sql.Tx, memberID string, limit int) ([]MailboxMessage, error) {
	qtx := s.q.WithTx(tx)
	rows, err := qtx.GetUnreadMessages(ctx, db.GetUnreadMessagesParams{
		ToMemberID: memberID, Limit: int64(limit),
	})
	if err != nil {
		return nil, fmt.Errorf("get unread messages: %w", err)
	}
	out := make([]MailboxMessage, 0, len(rows))
	for _, r := range rows {
		out = append(out, toMailboxMessage(r))
	}
	return out, nil
}

func (s *sqlcMailboxStore) InsertReceipt(ctx context.Context, tx *sql.Tx, receipt MessageReceipt) error {
	qtx := s.q.WithTx(tx)
	err := qtx.InsertReceipt(ctx, db.InsertReceiptParams{
		ID: receipt.ID, MessageID: receipt.MessageID, ToMemberID: receipt.ToMemberID,
		DeliveredAt: timePtrToNullInt64(receipt.DeliveredAt),
		ReadAt:      timePtrToNullInt64(receipt.ReadAt),
	})
	if err != nil {
		return fmt.Errorf("insert receipt: %w", err)
	}
	return nil
}

func (s *sqlcMailboxStore) MarkDelivered(ctx context.Context, tx *sql.Tx, messageID, memberID string, at time.Time) error {
	qtx := s.q.WithTx(tx)
	err := qtx.MarkDelivered(ctx, db.MarkDeliveredParams{
		DeliveredAt: at.UnixMilli(), MessageID: messageID, ToMemberID: memberID,
	})
	if err != nil {
		return fmt.Errorf("mark delivered: %w", err)
	}
	return nil
}

func (s *sqlcMailboxStore) MarkRead(ctx context.Context, tx *sql.Tx, messageID, memberID string, at time.Time) error {
	qtx := s.q.WithTx(tx)
	err := qtx.MarkRead(ctx, db.MarkReadParams{
		ReadAt: at.UnixMilli(), MessageID: messageID, ToMemberID: memberID,
	})
	if err != nil {
		return fmt.Errorf("mark read: %w", err)
	}
	return nil
}
