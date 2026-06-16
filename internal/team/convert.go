// convert.go owns ALL persistence (package db) ↔ domain (package team)
// translation. Every store method calls these helpers; this file is the
// single audit point for the sql.Null*↔pointer, int64-epoch↔time.Time, and
// int64↔int mappings. See plan Seam 3 for the conversion table.

package team

import (
	"database/sql"
	"time"

	"github.com/charmbracelet/crush/internal/db"
)

// --- null/pointer/time helpers ---

func nullStrToStrPtr(n sql.NullString) *string {
	if !n.Valid {
		return nil
	}
	s := n.String
	return &s
}

func strPtrToNullStr(p *string) sql.NullString {
	if p == nil {
		return sql.NullString{}
	}
	return sql.NullString{String: *p, Valid: true}
}

func nullInt64ToInt64Ptr(n sql.NullInt64) *int64 {
	if !n.Valid {
		return nil
	}
	v := n.Int64
	return &v
}

func int64PtrToNullInt64(p *int64) sql.NullInt64 {
	if p == nil {
		return sql.NullInt64{}
	}
	return sql.NullInt64{Int64: *p, Valid: true}
}

func nullInt64ToTimePtr(n sql.NullInt64) *time.Time {
	if !n.Valid {
		return nil
	}
	t := time.UnixMilli(n.Int64)
	return &t
}

func timePtrToNullInt64(p *time.Time) sql.NullInt64 {
	if p == nil {
		return sql.NullInt64{}
	}
	return sql.NullInt64{Int64: p.UnixMilli(), Valid: true}
}

// strPtrWithDefault dereferences a *string to "" when nil. Used for domain
// fields declared as plain string (Team.Description, TeamRun.UsageStatus,
// TeamRun.Error) that carry an omitempty JSON tag — a NULL column maps to the
// zero-value string so omitempty drops it on marshal, matching the M3-03
// domain contract. (Replaces the earlier strPtrDeref-withDefault method-on-
// builtin pattern, which does not compile in Go: a named type's method
// cannot be called on a builtin *string receiver.)
func strPtrWithDefault(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

// strPtrOrNil returns *string for a non-empty string, nil for "" — lets
// callers pass plain-string domain fields (Description) into strPtrToNullStr.
func strPtrOrNil(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

// --- row → domain converters ---

func toTeam(r db.Team) Team {
	return Team{
		ID:              r.ID,
		WorkspaceID:     r.WorkspaceID,
		LeaderSessionID: r.LeaderSessionID,
		Name:            r.Name,
		Description:     strPtrWithDefault(nullStrToStrPtr(r.Description)),
		Status:          TeamStatus(r.Status),
		Version:         int(r.Version),
		MaxCost:         nullInt64ToInt64Ptr(r.MaxCost),
		MaxTokens:       nullInt64ToInt64Ptr(r.MaxTokens),
		CostSoFarMicros: r.CostSoFarMicros,
		CreatedAt:       time.UnixMilli(r.CreatedAt),
		UpdatedAt:       time.UnixMilli(r.UpdatedAt),
		ArchivedAt:      nullInt64ToTimePtr(r.ArchivedAt),
	}
}

func toTeamMember(r db.TeamMember) TeamMember {
	return TeamMember{
		ID:              r.ID,
		TeamID:          r.TeamID,
		SessionID:       nullStrToStrPtr(r.SessionID),
		Name:            r.Name,
		Role:            r.Role,
		AgentProfile:    r.AgentProfile,
		ModelProvider:   nullStrToStrPtr(r.ModelProvider),
		ModelName:       nullStrToStrPtr(r.ModelName),
		Status:          MemberStatus(r.Status),
		CurrentTaskID:   nullStrToStrPtr(r.CurrentTaskID),
		CurrentRunID:    nullStrToStrPtr(r.CurrentRunID),
		CurrentToolName: nullStrToStrPtr(r.CurrentToolName),
		LastEventSeq:    r.LastEventSeq,
		MaxCost:         nullInt64ToInt64Ptr(r.MaxCost),
		MaxTokens:       nullInt64ToInt64Ptr(r.MaxTokens),
		CostSoFarMicros: r.CostSoFarMicros,
		Version:         int(r.Version),
		CreatedAt:       time.UnixMilli(r.CreatedAt),
		UpdatedAt:       time.UnixMilli(r.UpdatedAt),
		StoppedAt:       nullInt64ToTimePtr(r.StoppedAt),
	}
}

func toTeamTask(r db.TeamTask) TeamTask {
	return TeamTask{
		ID:                r.ID,
		TeamID:            r.TeamID,
		Title:             r.Title,
		Description:       strPtrWithDefault(nullStrToStrPtr(r.Description)),
		Status:            TaskStatus(r.Status),
		AssigneeMemberID:  nullStrToStrPtr(r.AssigneeMemberID),
		CreatedByMemberID: r.CreatedByMemberID,
		Priority:          int(r.Priority),
		Version:           int(r.Version),
		ResultSummary:     nullStrToStrPtr(r.ResultSummary),
		CreatedAt:         time.UnixMilli(r.CreatedAt),
		UpdatedAt:         time.UnixMilli(r.UpdatedAt),
		CompletedAt:       nullInt64ToTimePtr(r.CompletedAt),
	}
}

func toTeamRun(r db.TeamRun) TeamRun {
	return TeamRun{
		ID:               r.ID,
		TeamID:           r.TeamID,
		MemberID:         r.MemberID,
		TaskID:           nullStrToStrPtr(r.TaskID),
		SessionID:        r.SessionID,
		Status:           RunStatus(r.Status),
		Attempt:          int(r.Attempt),
		HeartbeatAt:      nullInt64ToTimePtr(r.HeartbeatAt),
		StartedAt:        nullInt64ToTimePtr(r.StartedAt),
		FinishedAt:       nullInt64ToTimePtr(r.FinishedAt),
		PromptTokens:     nullInt64ToInt64Ptr(r.PromptTokens),
		CompletionTokens: nullInt64ToInt64Ptr(r.CompletionTokens),
		CostMicros:       nullInt64ToInt64Ptr(r.CostMicros),
		UsageStatus:      strPtrWithDefault(nullStrToStrPtr(r.UsageStatus)),
		Error:            strPtrWithDefault(nullStrToStrPtr(r.Error)),
	}
}

func toTeamEvent(r db.TeamEvent) TeamEvent {
	return TeamEvent{
		Seq:           r.Seq,
		ID:            r.ID,
		WorkspaceID:   r.WorkspaceID,
		TeamID:        r.TeamID,
		EventType:     r.EventType,
		EntityType:    r.EntityType,
		EntityID:      r.EntityID,
		ActorMemberID: nullStrToStrPtr(r.ActorMemberID),
		TaskID:        nullStrToStrPtr(r.TaskID),
		RunID:         nullStrToStrPtr(r.RunID),
		MessageID:     nullStrToStrPtr(r.MessageID),
		PayloadJSON:   nullStrToStrPtr(r.PayloadJSON),
		PublishedAt:   nullInt64ToTimePtr(r.PublishedAt),
		CreatedAt:     time.UnixMilli(r.CreatedAt),
	}
}

func toAuditEvent(r db.TeamAuditEvent) AuditEvent {
	return AuditEvent{
		ID:           r.ID,
		WorkspaceID:  r.WorkspaceID,
		TeamID:       r.TeamID,
		MemberID:     nullStrToStrPtr(r.MemberID),
		TaskID:       nullStrToStrPtr(r.TaskID),
		RunID:        nullStrToStrPtr(r.RunID),
		SessionID:    nullStrToStrPtr(r.SessionID),
		ToolCallID:   nullStrToStrPtr(r.ToolCallID),
		EventType:    r.EventType,
		Action:       nullStrToStrPtr(r.Action),
		ResourceType: nullStrToStrPtr(r.ResourceType),
		ResourceRef:  nullStrToStrPtr(r.ResourceRef),
		InputHash:    nullStrToStrPtr(r.InputHash),
		Summary:      nullStrToStrPtr(r.Summary),
		Decision:     nullStrToStrPtr(r.Decision),
		Scope:        nullStrToStrPtr(r.Scope),
		CreatedAt:    time.UnixMilli(r.CreatedAt),
	}
}

func toMailboxMessage(r db.TeamMailboxMessage) MailboxMessage {
	return MailboxMessage{
		ID:           r.ID,
		TeamID:       r.TeamID,
		FromMemberID: r.FromMemberID,
		Kind:         MessageKind(r.Kind),
		Summary:      r.Summary,
		Payload:      r.Payload,
		CreatedAt:    time.UnixMilli(r.CreatedAt),
	}
}

func toMessageReceipt(r db.TeamMessageReceipt) MessageReceipt {
	return MessageReceipt{
		ID:          r.ID,
		MessageID:   r.MessageID,
		ToMemberID:  r.ToMemberID,
		DeliveredAt: nullInt64ToTimePtr(r.DeliveredAt),
		ReadAt:      nullInt64ToTimePtr(r.ReadAt),
	}
}

func toTeamTaskDependency(r db.TeamTaskDependency) TeamTaskDependency {
	return TeamTaskDependency{
		TaskID:          r.TaskID,
		DependsOnTaskID: r.DependsOnTaskID,
		TeamID:          r.TeamID,
		CreatedAt:       time.UnixMilli(r.CreatedAt),
	}
}

func toSessionLink(r db.TeamSessionLink) TeamSessionLink {
	return TeamSessionLink{
		ID:        r.ID,
		TeamID:    r.TeamID,
		MemberID:  r.MemberID,
		SessionID: r.SessionID,
		LinkType:  r.LinkType,
		LinkedAt:  time.UnixMilli(r.LinkedAt),
	}
}
