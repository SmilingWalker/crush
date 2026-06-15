// team.go defines the TeamWorkspace interface (the team-data-domain surface
// exposed to frontends as proto wire types) and the domain(team)↔proto
// converters. The interface is proto in / proto out; AppWorkspace's
// implementation (app_team.go) translates each proto request into a
// team.Service call and the domain result back to proto.
//
// proto and team are SEPARATE type sets with identical field shapes (see
// proto/team.go): the converters below are pure field copies + a string cast
// across the named-string Status boundary. This mirrors M3-04's
// persistence↔domain convert.go, just a different axis (domain↔wire).

package workspace

import (
	"context"

	"github.com/charmbracelet/crush/internal/proto"
	"github.com/charmbracelet/crush/internal/team"
)

// TeamWorkspace exposes the M3 team-data-domain operations to frontends as
// proto wire types. AppWorkspace implements it by delegating to a team.Service
// (M3-05); ClientWorkspace (M3-07) implements it by proxying to the HTTP
// server. Method names align to team.Service (SpawnMember/CreateTask/
// UpdateTask/ListEventsAfter) so AppWorkspace is a thin passthrough.
// SendTeamMessage is deferred to M3b (no Service method exists; mailbox tables
// not yet created).
type TeamWorkspace interface {
	CreateTeam(ctx context.Context, req proto.CreateTeamRequest) (proto.TeamSnapshot, error)
	ListTeams(ctx context.Context, req proto.ListTeamsRequest) (proto.ListTeamsResponse, error)
	GetTeamSnapshot(ctx context.Context, workspaceID, teamID string) (proto.TeamSnapshot, error)
	SpawnMember(ctx context.Context, req proto.SpawnMemberRequest) (proto.TeamMember, error)
	CreateTask(ctx context.Context, req proto.CreateTeamTaskRequest) (proto.TeamTask, error)
	UpdateTask(ctx context.Context, req proto.UpdateTeamTaskRequest) (proto.TeamTask, error)
	ListEventsAfter(ctx context.Context, workspaceID, teamID string, afterSeq int64, limit int) (proto.TeamEventsResponse, error)
}

// --- domain(team) → proto converters ---

func teamToProtoTeam(t team.Team) proto.Team {
	return proto.Team{
		ID:              t.ID,
		WorkspaceID:     t.WorkspaceID,
		LeaderSessionID: t.LeaderSessionID,
		Name:            t.Name,
		Description:     t.Description,
		Status:          proto.TeamStatus(t.Status),
		Version:         t.Version,
		MaxCost:         t.MaxCost,
		MaxTokens:       t.MaxTokens,
		CostSoFarMicros: t.CostSoFarMicros,
		CreatedAt:       t.CreatedAt,
		UpdatedAt:       t.UpdatedAt,
		ArchivedAt:      t.ArchivedAt,
	}
}

func teamToProtoMember(m team.TeamMember) proto.TeamMember {
	return proto.TeamMember{
		ID: m.ID, TeamID: m.TeamID, SessionID: m.SessionID,
		Name: m.Name, Role: m.Role, AgentProfile: m.AgentProfile,
		ModelProvider: m.ModelProvider, ModelName: m.ModelName,
		Status: proto.MemberStatus(m.Status),
		CurrentTaskID: m.CurrentTaskID, CurrentRunID: m.CurrentRunID, CurrentToolName: m.CurrentToolName,
		LastEventSeq: m.LastEventSeq, MaxCost: m.MaxCost, MaxTokens: m.MaxTokens,
		CostSoFarMicros: m.CostSoFarMicros, Version: m.Version,
		CreatedAt: m.CreatedAt, UpdatedAt: m.UpdatedAt, StoppedAt: m.StoppedAt,
	}
}

func teamToProtoTask(tk team.TeamTask) proto.TeamTask {
	return proto.TeamTask{
		ID: tk.ID, TeamID: tk.TeamID, Title: tk.Title, Description: tk.Description,
		Status: proto.TaskStatus(tk.Status),
		AssigneeMemberID: tk.AssigneeMemberID, CreatedByMemberID: tk.CreatedByMemberID,
		Priority: tk.Priority, Version: tk.Version, ResultSummary: tk.ResultSummary,
		CreatedAt: tk.CreatedAt, UpdatedAt: tk.UpdatedAt, CompletedAt: tk.CompletedAt,
	}
}

func teamToProtoRun(r team.TeamRun) proto.TeamRun {
	return proto.TeamRun{
		ID: r.ID, TeamID: r.TeamID, MemberID: r.MemberID, TaskID: r.TaskID,
		SessionID: r.SessionID, Status: proto.RunStatus(r.Status), Attempt: r.Attempt,
		HeartbeatAt: r.HeartbeatAt, StartedAt: r.StartedAt, FinishedAt: r.FinishedAt,
		PromptTokens: r.PromptTokens, CompletionTokens: r.CompletionTokens,
		CostMicros: r.CostMicros, UsageStatus: r.UsageStatus, Error: r.Error,
	}
}

func teamToProtoEvent(e team.TeamEvent) proto.TeamEvent {
	return proto.TeamEvent{
		Seq: e.Seq, ID: e.ID, WorkspaceID: e.WorkspaceID, TeamID: e.TeamID,
		EventType: e.EventType, EntityType: e.EntityType, EntityID: e.EntityID,
		ActorMemberID: e.ActorMemberID, TaskID: e.TaskID, RunID: e.RunID,
		MessageID: e.MessageID, PayloadJSON: e.PayloadJSON, PublishedAt: e.PublishedAt,
		CreatedAt: e.CreatedAt,
	}
}

func teamToProtoSnapshot(s team.TeamSnapshot) proto.TeamSnapshot {
	out := proto.TeamSnapshot{
		Team: teamToProtoTeam(s.Team),
		Cost: s.Cost,
	}
	if len(s.Members) > 0 {
		out.Members = make([]proto.TeamMember, len(s.Members))
		for i, m := range s.Members {
			out.Members[i] = teamToProtoMember(m)
		}
	}
	if len(s.Tasks) > 0 {
		out.Tasks = make([]proto.TeamTask, len(s.Tasks))
		for i, tk := range s.Tasks {
			out.Tasks[i] = teamToProtoTask(tk)
		}
	}
	if len(s.Runs) > 0 {
		out.Runs = make([]proto.TeamRun, len(s.Runs))
		for i, r := range s.Runs {
			out.Runs[i] = teamToProtoRun(r)
		}
	}
	return out
}

// --- proto → domain(team) converters (for requests; server-set fields are NOT set here) ---

func protoToTeamTeam(p proto.Team) team.Team {
	return team.Team{
		ID: p.ID, WorkspaceID: p.WorkspaceID, LeaderSessionID: p.LeaderSessionID,
		Name: p.Name, Description: p.Description, Status: team.TeamStatus(p.Status),
		Version: p.Version, MaxCost: p.MaxCost, MaxTokens: p.MaxTokens,
		CostSoFarMicros: p.CostSoFarMicros, CreatedAt: p.CreatedAt, UpdatedAt: p.UpdatedAt,
		ArchivedAt: p.ArchivedAt,
	}
}

func protoToTeamMember(p proto.TeamMember) team.TeamMember {
	return team.TeamMember{
		ID: p.ID, TeamID: p.TeamID, SessionID: p.SessionID,
		Name: p.Name, Role: p.Role, AgentProfile: p.AgentProfile,
		ModelProvider: p.ModelProvider, ModelName: p.ModelName,
		Status: team.MemberStatus(p.Status),
		CurrentTaskID: p.CurrentTaskID, CurrentRunID: p.CurrentRunID, CurrentToolName: p.CurrentToolName,
		LastEventSeq: p.LastEventSeq, MaxCost: p.MaxCost, MaxTokens: p.MaxTokens,
		CostSoFarMicros: p.CostSoFarMicros, Version: p.Version,
		CreatedAt: p.CreatedAt, UpdatedAt: p.UpdatedAt, StoppedAt: p.StoppedAt,
	}
}

func protoToTeamTask(p proto.TeamTask) team.TeamTask {
	return team.TeamTask{
		ID: p.ID, TeamID: p.TeamID, Title: p.Title, Description: p.Description,
		Status: team.TaskStatus(p.Status),
		AssigneeMemberID: p.AssigneeMemberID, CreatedByMemberID: p.CreatedByMemberID,
		Priority: p.Priority, Version: p.Version, ResultSummary: p.ResultSummary,
		CreatedAt: p.CreatedAt, UpdatedAt: p.UpdatedAt, CompletedAt: p.CompletedAt,
	}
}

func protoToTeamRun(p proto.TeamRun) team.TeamRun {
	return team.TeamRun{
		ID: p.ID, TeamID: p.TeamID, MemberID: p.MemberID, TaskID: p.TaskID,
		SessionID: p.SessionID, Status: team.RunStatus(p.Status), Attempt: p.Attempt,
		HeartbeatAt: p.HeartbeatAt, StartedAt: p.StartedAt, FinishedAt: p.FinishedAt,
		PromptTokens: p.PromptTokens, CompletionTokens: p.CompletionTokens,
		CostMicros: p.CostMicros, UsageStatus: p.UsageStatus, Error: p.Error,
	}
}

func protoToTeamEvent(p proto.TeamEvent) team.TeamEvent {
	return team.TeamEvent{
		Seq: p.Seq, ID: p.ID, WorkspaceID: p.WorkspaceID, TeamID: p.TeamID,
		EventType: p.EventType, EntityType: p.EntityType, EntityID: p.EntityID,
		ActorMemberID: p.ActorMemberID, TaskID: p.TaskID, RunID: p.RunID,
		MessageID: p.MessageID, PayloadJSON: p.PayloadJSON, PublishedAt: p.PublishedAt,
		CreatedAt: p.CreatedAt,
	}
}

// --- request converters (proto request → team.Service request) ---

func protoToCreateTeamReq(p proto.CreateTeamRequest) team.CreateTeamRequest {
	return team.CreateTeamRequest{
		WorkspaceID: p.WorkspaceID, LeaderSessionID: p.LeaderSessionID,
		Name: p.Name, Description: p.Description, MaxCost: p.MaxCost, MaxTokens: p.MaxTokens,
	}
}

func protoToSpawnMemberReq(p proto.SpawnMemberRequest) team.SpawnMemberRequest {
	return team.SpawnMemberRequest{
		TeamID: p.TeamID, Name: p.Name, Role: p.Role, AgentProfile: p.AgentProfile,
	}
}

func protoToCreateTaskReq(p proto.CreateTeamTaskRequest) team.CreateTaskRequest {
	return team.CreateTaskRequest{
		TeamID: p.TeamID, Title: p.Title, Description: p.Description,
		CreatedByMemberID: p.CreatedByMemberID, Priority: p.Priority,
	}
}

func protoToUpdateTaskReq(p proto.UpdateTeamTaskRequest) team.UpdateTaskRequest {
	return team.UpdateTaskRequest{
		ID: p.ID, TeamID: p.TeamID, Status: team.TaskStatus(p.Status),
		AssigneeMemberID: p.AssigneeMemberID, ResultSummary: p.ResultSummary,
	}
}
