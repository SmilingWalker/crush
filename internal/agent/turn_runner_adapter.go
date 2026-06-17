// turn_runner_adapter.go bridges the M1 SessionAgent (Run takes SessionAgentCall)
// to the M4 TurnRunner interface (Run takes TeamAgentCall). It is the minimal
// production AgentFactory seam deferred since M2 — maps PromptEnvelope→Prompt,
// SessionID→SessionID, and marks member turns as NonInteractive (no UI notifications).
//
// TurnRunnerAdapter wraps a single SessionAgent. SessionAgentFactory creates
// new TurnRunner instances from a SessionAgent provider closure.

package agent

import (
	"context"
	"fmt"

	"charm.land/fantasy"
)

// TurnRunnerAdapter wraps a SessionAgent and implements TurnRunner.
type TurnRunnerAdapter struct {
	sa SessionAgent
}

// NewTurnRunnerFromSessionAgent creates a TurnRunner that delegates to sa.
func NewTurnRunnerFromSessionAgent(sa SessionAgent) TurnRunner {
	return &TurnRunnerAdapter{sa: sa}
}

// Run executes one agent turn. Maps TeamAgentCall fields to SessionAgentCall.
func (a *TurnRunnerAdapter) Run(ctx context.Context, call TeamAgentCall) (TurnRunResult, error) {
	// Inject actor context so downstream (tools, PermissionBridge, hooks)
	// can identify this as a team member session.
	ctx = call.Actor.WithContext(ctx)
	result, err := a.sa.Run(ctx, SessionAgentCall{
		SessionID:      call.SessionID,
		Prompt:         call.PromptEnvelope,
		NonInteractive: false, // member turns need full multi-turn tool loop
	})
	if err != nil {
		return TurnRunResult{Status: TurnFailed}, fmt.Errorf("session agent run: %w", err)
	}
	return TurnRunResult{Status: TurnCompleted, Result: result}, nil
}

// Cancel delegates to the wrapped SessionAgent.
func (a *TurnRunnerAdapter) Cancel(sessionID string) {
	a.sa.Cancel(sessionID)
}

// IsSessionBusy delegates to the wrapped SessionAgent.
func (a *TurnRunnerAdapter) IsSessionBusy(sessionID string) bool {
	return a.sa.IsSessionBusy(sessionID)
}

// AppendTools appends team tools to the wrapped SessionAgent's tool list
// without replacing existing tools (bash/read/write/grep etc.). Implements the
// optional ToolSettableRunner interface (M4-06) so MemberRunner can inject
// team tools into the runner before turns begin.
func (a *TurnRunnerAdapter) AppendTools(tools []fantasy.AgentTool) {
	a.sa.AppendTools(tools)
}

// SessionAgentFactory is a minimal AgentFactory that wraps a SessionAgent
// provider function. Each BuildRunner call invokes the provider to get a fresh
// SessionAgent and wraps it in a TurnRunnerAdapter.
//
// This factory does NOT use AgentSpec fields to configure models/tools/system
// prompt — the caller pre-configures the SessionAgent before passing it to the
// provider closure.
type SessionAgentFactory struct {
	newSA func() SessionAgent
}

// NewAgentFactory creates an AgentFactory from a SessionAgent provider.
func NewAgentFactory(newSA func() SessionAgent) AgentFactory {
	return &SessionAgentFactory{newSA: newSA}
}

// BuildRunner creates a new TurnRunner wrapping a fresh SessionAgent.
func (f *SessionAgentFactory) BuildRunner(ctx context.Context, spec AgentSpec) (TurnRunner, error) {
	sa := f.newSA()
	if sa == nil {
		return nil, fmt.Errorf("SessionAgentFactory: provider returned nil SessionAgent")
	}
	return NewTurnRunnerFromSessionAgent(sa), nil
}
