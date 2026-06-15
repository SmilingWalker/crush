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
}

// teamRunner is the concrete implementation of TeamRunner.
type teamRunner struct {
	svc     Service
	factory agent.AgentFactory
	members map[string]*MemberRunner
	mu      sync.RWMutex
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

// StartTeam loads all non-terminal members from the DB and starts their
// MemberRunners. Already-registered members are left untouched; only members
// that are not in the registry yet are created and started.
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
		spec := agent.AgentSpec{}
		mr := NewMemberRunner(member.ID, teamID, spec, t.factory, t.svc)
		t.members[member.ID] = mr
		go mr.Start(ctx)
	}
	return nil
}

// StopTeam shuts down all registered members for the given team using the
// specified StopMode. For StopGraceful, it polls each member's state until
// idle/stopped/failed with a per-member deadline; for StopCancel/StopForce,
// it calls Stop() immediately.
func (t *teamRunner) StopTeam(ctx context.Context, teamID string, mode StopMode) error {
	t.mu.RLock()
	defer t.mu.RUnlock()

	for _, mr := range t.members {
		if mr.TeamID != teamID {
			continue
		}
		switch mode {
		case StopGraceful:
			deadline := time.Now().Add(30 * time.Second)
			for time.Now().Before(deadline) {
				mr.mu.Lock()
				state := mr.State
				mr.mu.Unlock()
				if state == MemberIdle || state == MemberStopped || state == MemberFailed {
					break
				}
				select {
				case <-ctx.Done():
					return ctx.Err()
				case <-time.After(100 * time.Millisecond):
				}
			}
			mr.Stop()
		case StopCancel, StopForce:
			mr.Stop()
		}
	}
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

	mr := NewMemberRunner(dbMember.ID, teamID, spec, t.factory, t.svc)
	if err := mr.Start(ctx); err != nil {
		return TeamMember{}, fmt.Errorf("start member runner: %w", err)
	}

	t.mu.Lock()
	t.members[dbMember.ID] = mr
	t.mu.Unlock()

	return dbMember, nil
}

// StopMember looks up a member by ID and stops it with the given mode.
// For StopGraceful, it polls the member's state until idle/stopped/failed
// (with a 30s deadline) before calling Stop(). For StopCancel/StopForce,
// it calls Stop() immediately.
func (t *teamRunner) StopMember(ctx context.Context, teamID, memberID string, mode StopMode) error {
	t.mu.RLock()
	mr, ok := t.members[memberID]
	t.mu.RUnlock()
	if !ok {
		return fmt.Errorf("member %s not found in team %s", memberID, teamID)
	}

	switch mode {
	case StopGraceful:
		deadline := time.Now().Add(30 * time.Second)
		for time.Now().Before(deadline) {
			mr.mu.Lock()
			state := mr.State
			mr.mu.Unlock()
			if state == MemberIdle || state == MemberStopped || state == MemberFailed {
				break
			}
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(100 * time.Millisecond):
			}
		}
		mr.Stop()
	case StopCancel, StopForce:
		mr.Stop()
	}
	return nil
}

// CancelMemberTurn cancels a member's current turn. Idempotent — calling it
// on an already-stopped or failed member is a no-op.
//
// The cancel sequence follows the validTransitions state machine:
//   running → canceling_turn (CAS)
//   cancel context → wait for handleWake to detect ctx.Done()
//   idle → shutting_down → stopped (terminal)
//
// This is critical for acceptance #5: late Run results are guarded by
// validTransitions — a transitionLocked to MemberIdle from MemberStopped
// (or MemberShuttingDown) is invalid, so the late result cannot overwrite
// the terminal state.
func (t *teamRunner) CancelMemberTurn(ctx context.Context, req CancelMemberTurnRequest) error {
	t.mu.RLock()
	mr, ok := t.members[req.MemberID]
	t.mu.RUnlock()
	if !ok {
		return fmt.Errorf("member %s not found in team %s", req.MemberID, req.TeamID)
	}

	mr.mu.Lock()

	// Idempotent: if already terminal, nothing to do.
	if mr.State == MemberStopped || mr.State == MemberFailed {
		mr.mu.Unlock()
		return nil
	}

	// CAS: if currently in a runnable state, transition to canceling_turn.
	// This gives handleWake a valid state to transition from when Run() returns.
	if mr.State == MemberRunning || mr.State == MemberWaitingPermission {
		mr.transitionLocked(MemberCancelingTurn)
	}

	// Cancel the loop context — this causes Run() to return with context.Canceled.
	if mr.cancel != nil {
		mr.cancel()
	}
	mr.mu.Unlock()

	// Wait for handleWake to detect the cancelled context and transition to idle.
	timeout := req.Timeout
	if timeout == 0 {
		timeout = 30 * time.Second
	}
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		mr.mu.Lock()
		state := mr.State
		mr.mu.Unlock()
		if state == MemberIdle || state == MemberStopped || state == MemberFailed {
			break
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(100 * time.Millisecond):
		}
	}

	// Final transition to stopped via the valid path: shutting_down → stopped.
	mr.mu.Lock()
	if mr.State == MemberIdle || mr.State == MemberCancelingTurn {
		mr.transitionLocked(MemberShuttingDown)
		mr.transitionLocked(MemberStopped)
	}
	mr.mu.Unlock()

	return nil
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
		mr.mu.Lock()
		ms := MemberRuntimeState{
			State:        mr.State,
			Role:         mr.Role,
			CurrentRunID: mr.currentRunID,
		}
		mr.mu.Unlock()
		status.Members[id] = ms
		if ms.State == MemberRunning || ms.State == MemberQueued {
			status.ActiveRuns++
		}
	}
	return status, nil
}
