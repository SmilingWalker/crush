// team_runner.go implements the M4-02 TeamRunner — the runtime orchestrator
// that manages N MemberRunner instances for a long-lived team. It owns the
// member registry (map + RWMutex), exposes lifecycle methods (StartTeam,
// StopTeam, SpawnMember, StopMember, CancelMemberTurn, Status), and implements
// graceful shutdown via StopMode (StopGraceful/StopCancel/StopForce).
//
// The teamRunner struct is the single concrete implementation wired over
// M3-05 Service + M4-01 MemberRunner. All methods are safe for concurrent use.

package team

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/charmbracelet/crush/internal/agent"
)

// StopMode controls the shutdown behaviour of a team or member.
type StopMode int

const (
	StopGraceful StopMode = iota // wait for current turn to complete
	StopCancel                    // cancel current turn then stop
	StopForce                     // force stop, don't wait for flush
)

// CancelMemberTurnRequest carries the parameters for cancelling a member's
// current turn. TeamID+MemberID identify the target; RequestedBy+Reason are
// audit-only; Timeout bounds the wait for the turn to drain.
type CancelMemberTurnRequest struct {
	TeamID      string
	MemberID    string
	RequestedBy string
	Reason      string
	Timeout     time.Duration
}

// TeamRuntimeStatus is a point-in-time snapshot of a team's runtime state.
type TeamRuntimeStatus struct {
	TeamID     string
	Members    map[string]MemberRuntimeState
	ActiveRuns int
}

// MemberRuntimeState is a point-in-time snapshot of a single member's runtime
// state. Fields that depend on M4-05 (PromptEnvelope) and M4-12 (Cost
// Accounting) are zero-value placeholders until those milestones land.
type MemberRuntimeState struct {
	State        MemberStatus
	Role         string
	CurrentRunID string
	CurrentTask  string
	CurrentTool  string
	ActivityDesc string
	CostMicros   int64
}

// TeamRunner is the runtime orchestrator for a long-lived team. It manages
// the lifecycle of N MemberRunners — spawning, stopping, cancelling turns,
// and querying aggregate status.
type TeamRunner interface {
	StartTeam(ctx context.Context, teamID string) error
	StopTeam(ctx context.Context, teamID string, mode StopMode) error
	SpawnMember(ctx context.Context, teamID, name, role, agentProfile string, spec agent.AgentSpec) (TeamMember, error)
	StopMember(ctx context.Context, teamID, memberID string, mode StopMode) error
	CancelMemberTurn(ctx context.Context, req CancelMemberTurnRequest) error
	Status(ctx context.Context, teamID string) (TeamRuntimeStatus, error)

	// WakeMember sends a wake signal to a member's wake channel. Used after
	// SendMessage so the recipient processes the new message. Returns an error
	// if the member is not found in the registry.
	WakeMember(ctx context.Context, teamID, memberID string, source WakeSource) error
}

// teamRunner is the concrete implementation of TeamRunner.
type teamRunner struct {
	svc           Service
	factory       agent.AgentFactory
	members       map[string]*MemberRunner
	mu            sync.RWMutex
	createSession func(context.Context) (string, error)
}

// NewTeamRunner creates a TeamRunner wired over the given Service and
// AgentFactory. The member registry starts empty; members are added via
// StartTeam (loads from DB) or SpawnMember (creates + registers).
func NewTeamRunner(svc Service, factory agent.AgentFactory) TeamRunner {
	return &teamRunner{
		svc:     svc,
		factory: factory,
		members: make(map[string]*MemberRunner),
	}
}

// NewTeamRunnerWithSession creates a TeamRunner with session creation support.
// When members are spawned without a session, the createSession func is called
// on first wake to auto-create one (M4.5b gap fix).
func NewTeamRunnerWithSession(svc Service, factory agent.AgentFactory, createSession func(context.Context) (string, error)) TeamRunner {
	return &teamRunner{
		svc:           svc,
		factory:       factory,
		members:       make(map[string]*MemberRunner),
		createSession: createSession,
	}
}

// StartTeam loads all non-terminal members from the DB and starts their
// MemberRunners. Already-registered members are left untouched; only members
// that are not in the registry yet are created and started.
//
// For each member, it calls RecoverMemberSession to find any previously
// linked session (M4-10). If a session link exists, the member's SessionID
// is restored so MemberRunner can resume the conversation.
func (t *teamRunner) StartTeam(ctx context.Context, teamID string) error {
	members, err := t.svc.ListMembers(ctx, teamID)
	if err != nil {
		return fmt.Errorf("list members: %w", err)
	}

	t.mu.Lock()
	defer t.mu.Unlock()

	for _, member := range members {
		if member.Status == MemberStopped || member.Status == MemberFailed {
			continue
		}
		// Skip already-registered members.
		if _, exists := t.members[member.ID]; exists {
			continue
		}
		// Recover session link for this member (M4-10).
		sessionID, err := t.svc.RecoverMemberSession(ctx, teamID, member.ID)
		if err != nil {
			// Logged at call site; non-fatal — member starts without a session.
			sessionID = ""
		}
		if sessionID != "" {
			member.SessionID = &sessionID
		}
		spec := agent.AgentSpec{}
		mr := NewMemberRunner(member.ID, teamID, spec, t.factory, t.svc, t.createSession)
		t.members[member.ID] = mr
		go func() {
			if err := mr.Start(context.Background()); err != nil {
				slog.Error("StartTeam: member Start failed",
					"member_id", member.ID, "team_id", teamID, "error", err)
			}
		}()
	}
	return nil
}

// StopTeam shuts down all registered members for the given team using the
// specified StopMode. Each member shutdown runs concurrently — total time is
// bounded by the slowest member (max 30s), not N×30s.
// Each member's Shutdown() handles the full 9-step sequence internally, so
// there is no cross-member coordination needed.
func (t *teamRunner) StopTeam(ctx context.Context, teamID string, mode StopMode) error {
	t.mu.RLock()
	members := make([]*MemberRunner, 0, len(t.members))
	for _, mr := range t.members {
		if mr.TeamID == teamID {
			members = append(members, mr)
		}
	}
	t.mu.RUnlock()

	var wg sync.WaitGroup
	for _, mr := range members {
		wg.Add(1)
		go func(m *MemberRunner) {
			defer wg.Done()
			_ = m.Shutdown(ctx, mode)
		}(mr)
	}
	wg.Wait()
	return nil
}

// SpawnMember creates a member in the DB, builds a MemberRunner, starts it,
// and registers it in the member map. The returned TeamMember comes from the
// DB insert (ID is server-generated).
func (t *teamRunner) SpawnMember(ctx context.Context, teamID, name, role, agentProfile string, spec agent.AgentSpec) (TeamMember, error) {
	dbMember, err := t.svc.SpawnMember(ctx, SpawnMemberRequest{
		TeamID:       teamID,
		Name:         name,
		Role:         role,
		AgentProfile: agentProfile,
	})
	if err != nil {
		return TeamMember{}, fmt.Errorf("spawn member in db: %w", err)
	}

	mr := NewMemberRunner(dbMember.ID, teamID, spec, t.factory, t.svc, t.createSession)
	// Use background context — the member's lifetime is independent of the
	// spawning call's context (which is a short-lived tool handler ctx).
	if err := mr.Start(context.Background()); err != nil {
		return TeamMember{}, fmt.Errorf("start member runner: %w", err)
	}

	t.mu.Lock()
	t.members[dbMember.ID] = mr
	t.mu.Unlock()

	return dbMember, nil
}

// StopMember looks up a member by ID and shuts it down with the given mode.
// Delegates to MemberRunner.Shutdown() which handles the full 9-step sequence
// including wait loops — no external polling needed.
func (t *teamRunner) StopMember(ctx context.Context, teamID, memberID string, mode StopMode) error {
	t.mu.RLock()
	mr, ok := t.members[memberID]
	t.mu.RUnlock()
	if !ok {
		return fmt.Errorf("member %s not found in team %s", memberID, teamID)
	}
	return mr.Shutdown(ctx, mode)
}

// CancelMemberTurn cancels a member's current turn via the full 9-step shutdown
// sequence in Cancel mode. Idempotent — calling it on an already-stopped or
// failed member is a no-op (Shutdown handles this internally).
//
// The Shutdown sequence subsumes the previous inline cancel logic:
//   stopWakeups → shutdown event → cancel context → wait for drain →
//   flush → MarkRunTerminal(RunCanceled) → stopped → stopped event → publish
//
// Late run result safety (acceptance #5) is preserved: validTransitions
// prevents transitionLocked to Idle from Stopped/ShuttingDown.
func (t *teamRunner) CancelMemberTurn(ctx context.Context, req CancelMemberTurnRequest) error {
	t.mu.RLock()
	mr, ok := t.members[req.MemberID]
	t.mu.RUnlock()
	if !ok {
		return fmt.Errorf("member %s not found in team %s", req.MemberID, req.TeamID)
	}
	return mr.Shutdown(ctx, StopCancel)
}

// Status returns a point-in-time snapshot of all registered members for a team.
func (t *teamRunner) Status(ctx context.Context, teamID string) (TeamRuntimeStatus, error) {
	t.mu.RLock()
	defer t.mu.RUnlock()

	status := TeamRuntimeStatus{
		TeamID:  teamID,
		Members: make(map[string]MemberRuntimeState, len(t.members)),
	}

	for id, mr := range t.members {
		if mr.TeamID != teamID {
			continue
		}
		ms := mr.Status()
		status.Members[id] = ms
		if ms.State == MemberRunning || ms.State == MemberQueued {
			status.ActiveRuns++
		}
	}
	return status, nil
}

// WakeMember sends a wake signal to a member's wake channel. Used after
// SendMessage so the recipient processes the new message. Returns an error
// if the member is not found in the registry.
func (t *teamRunner) WakeMember(ctx context.Context, teamID, memberID string, source WakeSource) error {
	t.mu.RLock()
	mr, ok := t.members[memberID]
	t.mu.RUnlock()
	if !ok {
		return fmt.Errorf("member %s not found in team %s", memberID, teamID)
	}
	mr.Wake(source)
	return nil
}
