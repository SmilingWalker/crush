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
// MemberRunners. Already-registered members are left untouched.
func (t *teamRunner) StartTeam(ctx context.Context, teamID string) error {
	return fmt.Errorf("not implemented")
}

// StopTeam shuts down all registered members using the given StopMode.
func (t *teamRunner) StopTeam(ctx context.Context, teamID string, mode StopMode) error {
	return fmt.Errorf("not implemented")
}

// SpawnMember creates a member in the DB, builds a MemberRunner, starts it,
// and registers it in the member map.
func (t *teamRunner) SpawnMember(ctx context.Context, teamID, name, role, agentProfile string, spec agent.AgentSpec) (TeamMember, error) {
	return TeamMember{}, fmt.Errorf("not implemented")
}

// StopMember looks up a member by ID and stops it with the given mode.
func (t *teamRunner) StopMember(ctx context.Context, teamID, memberID string, mode StopMode) error {
	return fmt.Errorf("not implemented")
}

// CancelMemberTurn cancels a member's current turn. Idempotent — calling it
// on an already-stopped member is a no-op.
func (t *teamRunner) CancelMemberTurn(ctx context.Context, req CancelMemberTurnRequest) error {
	return fmt.Errorf("not implemented")
}

// Status returns a point-in-time snapshot of all registered members for a team.
func (t *teamRunner) Status(ctx context.Context, teamID string) (TeamRuntimeStatus, error) {
	return TeamRuntimeStatus{}, fmt.Errorf("not implemented")
}
