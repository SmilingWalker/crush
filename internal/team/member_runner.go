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

	"charm.land/fantasy"

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

	mu           sync.Mutex
	ctx          context.Context
	cancel       context.CancelFunc
	shuttingDown bool                       // set during Shutdown; loop drops wakes when true
	flushFn      func(context.Context) error // flush pending writes before terminal state (nil-safe)
	version      int                         // DB version from last successful UpdateMemberState CAS
	createSession func(context.Context) (string, error) // nil-safe; creates session on first wake
}

// NewMemberRunner creates a MemberRunner in MemberCreated state. The factory
// is used in Start() to build the TurnRunner. The Service is used to persist
// state transitions and record runs.
func NewMemberRunner(
	id, teamID string,
	spec agent.AgentSpec,
	factory agent.AgentFactory,
	svc Service,
	createSession func(context.Context) (string, error),
) *MemberRunner {
	return &MemberRunner{
		ID:            id,
		TeamID:        teamID,
		Spec:          spec,
		State:         MemberCreated,
		factory:       factory,
		svc:           svc,
		wakeCh:        make(chan WakeSource, 10),
		createSession: createSession,
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
			// internally. On success, store the new version for crash-recovery
			// consistency. Guard against nil svc (unit tests may not wire one).
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
					m.mu.Lock()
					m.version = updated.Version
					m.mu.Unlock()
				}()
			}
			return
		}
	}
	slog.Warn("invalid state transition",
		"member_id", m.ID, "from", m.State, "to", to)
}

// Status returns a point-in-time snapshot of this member's runtime state.
// Safe to call from any goroutine — handles its own locking.
func (m *MemberRunner) Status() MemberRuntimeState {
	m.mu.Lock()
	defer m.mu.Unlock()
	return MemberRuntimeState{
		State:        m.State,
		Role:         m.Role,
		SessionID:    m.sessionID,
		CurrentRunID: m.currentRunID,
	}
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

// Stop initiates shutdown via the 9-step Shutdown sequence in Force mode.
// Safe to call multiple times. After Stop, the member is terminal (stopped).
// This is the fast shutdown path: stopWakeups + cancel + markRunTerminal + stopped.
func (m *MemberRunner) Stop() {
	_ = m.Shutdown(context.Background(), StopForce)
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

	// M4-06: inject member tools (team_report_status + team_send_message)
	// into the TurnRunner if it supports the optional ToolSettableRunner interface.
	// Uses AppendTools (not SetTools) to preserve any standard tools
	// (bash/read/write/grep etc.) already on the runner (Finding 3).
	if ts, ok := runner.(agent.ToolSettableRunner); ok {
		ts.AppendTools([]fantasy.AgentTool{
			NewTeamReportStatusTool(m.ID, m.TeamID, m.svc),
			NewTeamSendMessageTool(m.ID, m.TeamID, m.Role, m.svc),
		})
	}

	// Enter idle and launch loop.
	m.transitionLocked(MemberIdle)
	m.ctx, m.cancel = context.WithCancel(ctx)
	go m.loop()
	return nil
}

// loop is the main event loop. It blocks on the context or wake channel.
// All work is serial — only one wake is processed at a time.
// During shutdown (shuttingDown flag set), incoming wakes are dropped.
func (m *MemberRunner) loop() {
	defer m.cancel()
	for {
		select {
		case <-m.ctx.Done():
			return
		case source := <-m.wakeCh:
			m.mu.Lock()
			if m.shuttingDown {
				m.mu.Unlock()
				slog.Debug("loop: dropping wake during shutdown",
					"member_id", m.ID, "source", source)
				continue
			}
			m.mu.Unlock()
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

	// Auto-create a session if none exists (first wake after spawn).
	if m.sessionID == "" && m.createSession != nil {
		sid, err := m.createSession(m.ctx)
		if err != nil {
			slog.Error("handleWake: createSession failed", "member_id", m.ID, "error", err)
			m.mu.Lock()
			m.transitionLocked(MemberFailed)
			m.mu.Unlock()
			return
		}
		m.mu.Lock()
		m.sessionID = sid
		m.mu.Unlock()
		// Persist the session link so the session can be recovered on restart.
		if m.svc != nil {
			if err := m.svc.LinkSessionToMember(m.ctx, m.TeamID, m.ID, sid); err != nil {
				slog.Error("handleWake: LinkSessionToMember failed", "member_id", m.ID, "error", err)
			}
		}
	}

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

	// Build the prompt envelope via M4-05 PromptBuilder.
	prompt, buildErr := m.buildPrompt(m.ctx, source)
	if buildErr != nil {
		slog.Error("handleWake: buildPrompt failed", "member_id", m.ID, "error", buildErr)
		m.mu.Lock()
		m.transitionLocked(MemberFailed)
		m.mu.Unlock()
		return
	}

	// Execute one turn.
	result, err := m.runner.Run(m.ctx, agent.TeamAgentCall{
		SessionID:      m.sessionID,
		PromptEnvelope: prompt,
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

// DefaultMaxPromptBytes is the fallback prompt byte budget when AgentSpec
// doesn't specify one. 200KB fits most model context windows with headroom.
const DefaultMaxPromptBytes = 200_000

// buildPrompt assembles the full prompt envelope via PromptBuilder (M4-05).
// The m.svc satisfies the MailboxReader interface; current task is nil until
// task claiming is wired into the wake flow.
func (m *MemberRunner) buildPrompt(ctx context.Context, source WakeSource) (string, error) {
	pb := NewPromptBuilder(m.ID, m.TeamID, m.Role, nil, m.svc)
	maxBytes := m.Spec.MaxPromptBytes
	if maxBytes == 0 {
		maxBytes = DefaultMaxPromptBytes
	}
	pb.SetMaxBytes(maxBytes)
	return pb.Build(ctx)
}

// actorCtx builds an ActorContext populated from this member's identity fields.
func (m *MemberRunner) actorCtx() actor.ActorContext {
	return actor.ActorContext{
		TeamID:     m.TeamID,
		MemberID:   m.ID,
		MemberRole: m.Role,
	}
}
