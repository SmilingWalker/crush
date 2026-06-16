// app_team.go makes *AppWorkspace implement TeamWorkspace by delegating each
// method to an injected team.Service (M3-05). Each method translates the proto
// request → team.Service request → domain result → proto result.
//
// The Service is OPTIONAL: AppWorkspace holds it via SetTeamService (Seam 1),
// defaulting to nil. A nil Service yields ErrTeamServiceNotConfigured from
// every method — this lets AppWorkspace satisfy TeamWorkspace at compile time
// even before the M3-08 wiring injects a real Service. The compile-assert at
// the bottom of this file is acceptance #1.

package workspace

import (
	"context"
	"errors"

	"github.com/charmbracelet/crush/internal/proto"
	"github.com/charmbracelet/crush/internal/team"
)

// ErrTeamServiceNotConfigured is returned by every TeamWorkspace method when
// no team.Service has been injected via SetTeamService (e.g. before M3-08
// wires the feature flag, or in AppWorkspaces that don't exercise team).
var ErrTeamServiceNotConfigured = errors.New("team service not configured on this workspace")

// SetTeamService injects the M3-05 team.Service that backs AppWorkspace's
// TeamWorkspace methods. Mirrors the App()/Store() accessor pattern: the
// Service is constructed and injected by the caller (M3-08 wiring / a
// NewAppWorkspace follow-up), keeping app.go out of M3-06's scope.
func (w *AppWorkspace) SetTeamService(svc team.Service) {
	w.team = svc
}

func (w *AppWorkspace) nilService() error {
	if w.team == nil {
		return ErrTeamServiceNotConfigured
	}
	return nil
}

// --- TeamWorkspace methods (proto → team.Service → proto) ---

func (w *AppWorkspace) CreateTeam(ctx context.Context, req proto.CreateTeamRequest) (proto.TeamSnapshot, error) {
	if err := w.nilService(); err != nil {
		return proto.TeamSnapshot{}, err
	}
	snap, err := w.team.CreateTeam(ctx, protoToCreateTeamReq(req))
	if err != nil {
		return proto.TeamSnapshot{}, err
	}
	return teamToProtoSnapshot(snap), nil
}

func (w *AppWorkspace) ListTeams(ctx context.Context, req proto.ListTeamsRequest) (proto.ListTeamsResponse, error) {
	if err := w.nilService(); err != nil {
		return proto.ListTeamsResponse{}, err
	}
	teams, err := w.team.ListTeams(ctx, req.WorkspaceID, team.TeamFilter{IncludeArchived: req.IncludeArchived})
	if err != nil {
		return proto.ListTeamsResponse{}, err
	}
	out := proto.ListTeamsResponse{}
	if len(teams) > 0 {
		out.Teams = make([]proto.Team, len(teams))
		for i, t := range teams {
			out.Teams[i] = teamToProtoTeam(t)
		}
	}
	return out, nil
}

func (w *AppWorkspace) GetTeamSnapshot(ctx context.Context, workspaceID, teamID string) (proto.TeamSnapshot, error) {
	if err := w.nilService(); err != nil {
		return proto.TeamSnapshot{}, err
	}
	snap, err := w.team.GetTeamSnapshot(ctx, workspaceID, teamID)
	if err != nil {
		return proto.TeamSnapshot{}, err
	}
	return teamToProtoSnapshot(snap), nil
}

func (w *AppWorkspace) SpawnMember(ctx context.Context, req proto.SpawnMemberRequest) (proto.TeamMember, error) {
	if err := w.nilService(); err != nil {
		return proto.TeamMember{}, err
	}
	m, err := w.team.SpawnMember(ctx, protoToSpawnMemberReq(req))
	if err != nil {
		return proto.TeamMember{}, err
	}
	return teamToProtoMember(m), nil
}

func (w *AppWorkspace) CreateTask(ctx context.Context, req proto.CreateTeamTaskRequest) (proto.TeamTask, error) {
	if err := w.nilService(); err != nil {
		return proto.TeamTask{}, err
	}
	tk, err := w.team.CreateTask(ctx, protoToCreateTaskReq(req))
	if err != nil {
		return proto.TeamTask{}, err
	}
	return teamToProtoTask(tk), nil
}

func (w *AppWorkspace) UpdateTask(ctx context.Context, req proto.UpdateTeamTaskRequest) (proto.TeamTask, error) {
	if err := w.nilService(); err != nil {
		return proto.TeamTask{}, err
	}
	tk, err := w.team.UpdateTask(ctx, protoToUpdateTaskReq(req))
	if err != nil {
		return proto.TeamTask{}, err
	}
	return teamToProtoTask(tk), nil
}

func (w *AppWorkspace) ListEventsAfter(ctx context.Context, workspaceID, teamID string, afterSeq int64, limit int) (proto.TeamEventsResponse, error) {
	if err := w.nilService(); err != nil {
		return proto.TeamEventsResponse{}, err
	}
	events, err := w.team.ListEventsAfter(ctx, teamID, afterSeq, limit)
	if err != nil {
		return proto.TeamEventsResponse{}, err
	}
	out := proto.TeamEventsResponse{}
	if len(events) > 0 {
		out.Events = make([]proto.TeamEvent, len(events))
		for i, e := range events {
			out.Events[i] = teamToProtoEvent(e)
		}
	}
	return out, nil
}

// TeamRunner returns the TeamRunner instance backing this workspace, or nil
// if the app was created via NewForTest.
func (w *AppWorkspace) TeamRunner() team.TeamRunner {
	return w.app.TeamRunner()
}

// Compile-time check that AppWorkspace implements TeamWorkspace (acceptance #1).
var _ TeamWorkspace = (*AppWorkspace)(nil)
