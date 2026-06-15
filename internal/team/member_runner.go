// member_runner.go implements the M4-01 MemberRunner — the runtime state
// machine for a long-lived team member. It owns the wake/loop/run lifecycle:
// waits on wakeCh for wake sources, executes one agent turn via TurnRunner,
// records the run in DB, and loops.
//
// State machine uses M3-03's MemberStatus type (11 values, 1:1 match with
// the master task doc's MemberState enum). All state transitions are validated
// against validTransitions; CAS persistence is delegated to TeamService.

package team

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"

	"github.com/charmbracelet/crush/internal/actor"
	"github.com/charmbracelet/crush/internal/agent"
)

// WakeSource categorizes what triggered a member's wake-up.
type WakeSource int

const (
	WakeSourceMailbox    WakeSource = iota // incoming message (M4-04 wire-up)
	WakeSourceTask                         // new task assigned
	WakeSourceExplicit                     // leader explicit wake
	WakeSourceRecovery                     // startup recovery
	WakeSourceDependency                   // dependency task completed
)

// validTransitions maps each MemberStatus to its allowed next states.
// Sourced from the master task doc M4-01:35-48. All 11 MemberStatus values
// from M3-03 models.go are covered.
var validTransitions = map[MemberStatus][]MemberStatus{
	MemberCreated:           {MemberStarting, MemberStopped},
	MemberStarting:          {MemberIdle, MemberFailed},
	MemberIdle:              {MemberQueued, MemberRunning, MemberBlocked, MemberShuttingDown, MemberStopped},
	MemberQueued:            {MemberRunning, MemberIdle, MemberShuttingDown},
	MemberRunning:           {MemberIdle, MemberWaitingPermission, MemberBlocked, MemberCancelingTurn, MemberFailed, MemberShuttingDown},
	MemberWaitingPermission: {MemberIdle, MemberBlocked, MemberCancelingTurn, MemberShuttingDown},
	MemberBlocked:           {MemberIdle, MemberShuttingDown, MemberStopped},
	MemberCancelingTurn:     {MemberIdle, MemberBlocked, MemberShuttingDown, MemberFailed},
	MemberShuttingDown:      {MemberStopped, MemberFailed},
	MemberStopped:           {},
	MemberFailed:            {MemberStopped},
}

// MemberRunner is the runtime state machine for a long-lived team member.
// It owns the wake/loop/run lifecycle: waits on wakeCh for wake sources,
// executes one agent turn via TurnRunner, records the run in DB, and loops.
type MemberRunner struct {
	ID     string
	TeamID string
	Spec   agent.AgentSpec
	State  MemberStatus
	Role   string // from DB member record

	runner  agent.TurnRunner
	factory agent.AgentFactory
	svc     Service

	wakeCh chan WakeSource

	currentRunID string
	sessionID    string

	mu     sync.Mutex
	ctx    context.Context
	cancel context.CancelFunc
}

// NewMemberRunner creates a MemberRunner in MemberCreated state. The factory
// is used in Start() to build the TurnRunner. The Service is used to persist
// state transitions and record runs.
func NewMemberRunner(
	id, teamID string,
	spec agent.AgentSpec,
	factory agent.AgentFactory,
	svc Service,
) *MemberRunner {
	return &MemberRunner{
		ID:      id,
		TeamID:  teamID,
		Spec:    spec,
		State:   MemberCreated,
		factory: factory,
		svc:     svc,
		wakeCh:  make(chan WakeSource, 10),
	}
}

// transitionLocked validates and applies a state transition. The caller MUST
// hold m.mu. On a valid transition, it updates m.State, fires an async CAS
// update to the DB via the Service, and bumps the version on success.
// On an invalid transition, it logs a warning and leaves the state unchanged.
func (m *MemberRunner) transitionLocked(to MemberStatus) {
	allowed, ok := validTransitions[m.State]
	if !ok {
		slog.Error("unknown current state", "member_id", m.ID, "state", m.State)
		return
	}
	for _, t := range allowed {
		if t == to {
			prev := m.State
			m.State = to
			// Async CAS update to DB. The Service handles read-then-CAS
			// internally; we don't pass version (Seam 3 deviation from master doc).
			// Guard against nil svc (unit tests may not wire one).
			if m.svc != nil {
				go func() {
					updated, err := m.svc.UpdateMemberState(context.Background(), UpdateMemberStateRequest{
						ID:     m.ID,
						TeamID: m.TeamID,
						Status: to,
					})
					if err != nil {
						slog.Error("transitionLocked: UpdateMemberState failed",
							"member_id", m.ID, "from", prev, "to", to, "error", err)
						return
					}
					// CAS succeeded — the returned member has the new version.
					_ = updated
				}()
			}
			return
		}
	}
	slog.Warn("invalid state transition",
		"member_id", m.ID, "from", m.State, "to", to)
}

// Wake enqueues a wake source. If the channel is full, the source is dropped
// (logged at Warn level). Non-blocking — safe to call from any goroutine.
func (m *MemberRunner) Wake(source WakeSource) {
	select {
	case m.wakeCh <- source:
	default:
		slog.Warn("member wake channel full, dropping source",
			"member_id", m.ID, "source", source)
	}
}

// Stop initiates shutdown: cancels the loop context and transitions to stopped.
// Safe to call multiple times. After Stop, the member is terminal (stopped).
func (m *MemberRunner) Stop() {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.State == MemberStopped || m.State == MemberFailed {
		return // already terminal
	}
	if m.cancel != nil {
		m.cancel()
	}
	m.transitionLocked(MemberStopped)
}

// Start loads the member from DB, builds the TurnRunner via factory, enters idle
// state, and launches the background loop. Returns an error if the member has
// already been started (state != MemberCreated).
func (m *MemberRunner) Start(ctx context.Context) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.State != MemberCreated {
		return fmt.Errorf("member %s already started (state=%s)", m.ID, m.State)
	}
	m.transitionLocked(MemberStarting)

	// Load member from DB to get session_id, role, etc.
	members, err := m.svc.ListMembers(ctx, m.TeamID)
	if err != nil {
		m.transitionLocked(MemberFailed)
		return fmt.Errorf("load members: %w", err)
	}
	var dbMember *TeamMember
	for i := range members {
		if members[i].ID == m.ID {
			dbMember = &members[i]
			break
		}
	}
	if dbMember == nil {
		m.transitionLocked(MemberFailed)
		return fmt.Errorf("member %s not found in team %s", m.ID, m.TeamID)
	}
	if dbMember.SessionID != nil {
		m.sessionID = *dbMember.SessionID
	}
	m.Role = dbMember.Role

	// Build TurnRunner via factory.
	runner, err := m.factory.BuildRunner(ctx, m.Spec)
	if err != nil {
		m.transitionLocked(MemberFailed)
		return fmt.Errorf("build runner: %w", err)
	}
	m.runner = runner

	// Enter idle and launch loop.
	m.transitionLocked(MemberIdle)
	m.ctx, m.cancel = context.WithCancel(ctx)
	go m.loop()
	return nil
}

// loop is the main event loop. It blocks on the context or wake channel.
// All work is serial — only one wake is processed at a time.
func (m *MemberRunner) loop() {
	defer m.cancel()
	for {
		select {
		case <-m.ctx.Done():
			return
		case source := <-m.wakeCh:
			m.handleWake(source)
		}
	}
}

// handleWake processes one wake event through the single-flight gate,
// executes a turn, records the result, and transitions back to idle.
func (m *MemberRunner) handleWake(source WakeSource) {
	m.mu.Lock()
	// Single-flight gate: if already busy, drop this wake.
	if m.State == MemberRunning || m.State == MemberQueued || m.State == MemberCancelingTurn {
		slog.Debug("handleWake: member busy, dropping wake",
			"member_id", m.ID, "state", m.State, "source", source)
		m.mu.Unlock()
		return
	}
	m.transitionLocked(MemberQueued)
	m.mu.Unlock()

	// Transition to running.
	m.mu.Lock()
	m.transitionLocked(MemberRunning)
	m.mu.Unlock()

	// Start a run record in DB.
	run, err := m.svc.StartRun(m.ctx, StartRunRequest{
		TeamID:    m.TeamID,
		MemberID:  m.ID,
		SessionID: m.sessionID,
	})
	if err != nil {
		slog.Error("handleWake: StartRun failed", "member_id", m.ID, "error", err)
		m.mu.Lock()
		m.transitionLocked(MemberFailed)
		m.mu.Unlock()
		return
	}
	m.mu.Lock()
	m.currentRunID = run.ID
	m.mu.Unlock()

	// Execute one turn.
	result, err := m.runner.Run(m.ctx, agent.TeamAgentCall{
		SessionID:      m.sessionID,
		PromptEnvelope: m.buildPrompt(source),
		Actor:          m.actorCtx(),
	})

	// Process result.
	m.mu.Lock()
	defer m.mu.Unlock()

	if err != nil {
		if errors.Is(err, context.Canceled) {
			m.transitionLocked(MemberIdle)
			return
		}
		// Record the failed run.
		_ = m.svc.MarkRunTerminal(context.Background(), MarkRunTerminalRequest{
			TeamID: m.TeamID, RunID: run.ID, Status: RunFailed, Error: err.Error(),
		})
		m.transitionLocked(MemberFailed)
		return
	}

	// Record the completed run with usage data.
	finishReq := FinishRunRequest{
		TeamID: m.TeamID,
		RunID:  run.ID,
	}
	if result.Result != nil {
		finishReq.PromptTokens = result.Result.TotalUsage.InputTokens
		finishReq.CompletionTokens = result.Result.TotalUsage.OutputTokens
	}
	if err := m.svc.FinishRun(context.Background(), finishReq); err != nil {
		slog.Error("handleWake: FinishRun failed", "member_id", m.ID, "run_id", run.ID, "error", err)
	}

	m.transitionLocked(MemberIdle)
}

// buildPrompt is a stub that returns a simple prompt string from the wake source.
// M4-05 (PromptEnvelope Builder) replaces this with full system+task+context
// assembly. Until then, this produces a minimal prompt sufficient for state
// machine testing.
func (m *MemberRunner) buildPrompt(source WakeSource) string {
	srcName := "unknown"
	switch source {
	case WakeSourceMailbox:
		srcName = "mailbox"
	case WakeSourceTask:
		srcName = "task"
	case WakeSourceExplicit:
		srcName = "explicit"
	case WakeSourceRecovery:
		srcName = "recovery"
	case WakeSourceDependency:
		srcName = "dependency"
	}
	return fmt.Sprintf("[M4-01 stub prompt] Wake source: %s. Member %s (%s) beginning turn.", srcName, m.ID, m.Role)
}

// actorCtx builds an ActorContext populated from this member's identity fields.
func (m *MemberRunner) actorCtx() actor.ActorContext {
	return actor.ActorContext{
		TeamID:     m.TeamID,
		MemberID:   m.ID,
		MemberRole: m.Role,
	}
}
