// mailbox.go is the M4-04 domain-level SendMessage implementation: resolve
// recipients (direct/broadcast/role) → insert 1 message + N receipts in a single
// tx. The method is added to the Service interface (service.go) and implemented
// on teamService. It returns the resolved recipient member IDs so the caller
// (MemberRunner / Scheduler) can Wake each one.
//
// Acceptance (master doc M4-04 :523-530):
//  1. Direct: 1 message + 1 receipt + recipient woken
//  2. Broadcast: 1 message + N receipts + all woken
//  3. Role: only matching-role members get receipts
//  4. Delivered/read receipts trackable
//  5. Shutdown_request is stored as a normal mailbox message (special handling
//     in the prompt builder / shutdown sequence, not here)

package team

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/google/uuid"
)

// SendMessageRequest carries the inputs for SendMessage.
type SendMessageRequest struct {
	TeamID        string        `json:"team_id"`
	FromMemberID  string        `json:"from_member_id"`
	RecipientType RecipientType `json:"recipient_type"`
	ToMemberID    string        `json:"to_member_id,omitempty"` // direct
	ToRole        string        `json:"to_role,omitempty"`      // role
	Kind          MessageKind   `json:"kind"`
	Summary       string        `json:"summary"`
	Payload       string        `json:"payload"`
}

// SendMessage resolves recipients, inserts 1 message + N receipts in one tx,
// and returns the resolved recipient member IDs so the caller can Wake each one.
// M4-04 master doc :509-520.
func (s *teamService) SendMessage(ctx context.Context, req SendMessageRequest) ([]string, error) {
	if err := s.enabledGuard(); err != nil {
		return nil, err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("begin tx: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	ts := now()

	// 1. Insert the message
	msg, err := s.mailbox.InsertMessage(ctx, tx, MailboxMessage{
		ID: uuid.New().String(), TeamID: req.TeamID, FromMemberID: req.FromMemberID,
		Kind: req.Kind, Summary: req.Summary, Payload: req.Payload, CreatedAt: ts,
	})
	if err != nil {
		return nil, err
	}

	// 2. Resolve recipients
	recipientIDs, err := s.resolveRecipients(ctx, tx, req)
	if err != nil {
		return nil, err
	}

	// 3. Insert one receipt per recipient
	for _, memberID := range recipientIDs {
		r := MessageReceipt{
			ID: uuid.New().String(), MessageID: msg.ID, ToMemberID: memberID,
		}
		if err := s.mailbox.InsertReceipt(ctx, tx, r); err != nil {
			return nil, err
		}
	}

	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("commit: %w", err)
	}
	return recipientIDs, nil
}

// GetUnreadMessages returns unread mailbox messages for a member.
func (s *teamService) GetUnreadMessages(ctx context.Context, memberID string, limit int) ([]MailboxMessage, error) {
	if err := s.enabledGuard(); err != nil {
		return nil, err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("begin tx: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	msgs, err := s.mailbox.GetUnreadMessages(ctx, tx, memberID, limit)
	if err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("commit: %w", err)
	}
	return msgs, nil
}

// resolveRecipients returns the member IDs that should receive this message,
// based on the RecipientType and member data from DB.
func (s *teamService) resolveRecipients(ctx context.Context, tx *sql.Tx, req SendMessageRequest) ([]string, error) {
	switch req.RecipientType {
	case RecipientDirect:
		if req.ToMemberID == "" {
			return nil, fmt.Errorf("direct recipient requires ToMemberID")
		}
		return []string{req.ToMemberID}, nil

	case RecipientBroadcast:
		members, err := s.members.ListMembers(ctx, tx, req.TeamID)
		if err != nil {
			return nil, fmt.Errorf("list members for broadcast: %w", err)
		}
		ids := make([]string, 0, len(members))
		for _, m := range members {
			if m.ID != req.FromMemberID {
				ids = append(ids, m.ID)
			}
		}
		return ids, nil

	case RecipientRole:
		members, err := s.members.ListMembers(ctx, tx, req.TeamID)
		if err != nil {
			return nil, fmt.Errorf("list members for role: %w", err)
		}
		ids := make([]string, 0)
		for _, m := range members {
			if m.ID != req.FromMemberID && m.Role == req.ToRole {
				ids = append(ids, m.ID)
			}
		}
		return ids, nil

	default:
		return nil, fmt.Errorf("unknown recipient type: %s", req.RecipientType)
	}
}
