package agent

import (
	"context"

	"charm.land/fantasy"
)

// async sub-agent run states reported on SubAgentStatus.State.
const (
	subAgentStateRunning  = "running"
	subAgentStateDone     = "done"
	subAgentStateError    = "error"
	subAgentStateCanceled = "canceled"
)

// SubAgentHandle is the caller's handle to an async sub-agent run, returned
// immediately by (*coordinator).runSubAgentAsync. StatusChan delivers status
// observations and is closed when the run reaches a terminal state.
// Cancel stops the background goroutine.
type SubAgentHandle struct {
	RunID      string
	StatusChan <-chan SubAgentStatus
	Cancel     context.CancelFunc
}

// SubAgentStatus is a single observation of an async sub-agent run. State is
// one of the subAgentState* constants. Result is populated when State ==
// "done"; Error is populated when State is "error" or "canceled".
type SubAgentStatus struct {
	State    string
	Progress string
	Result   *fantasy.ToolResponse
	Error    error
}

// activeSubAgent is the internal bookkeeping record for one in-flight async
// sub-agent run, stored in coordinator.activeSubAgents keyed by RunID.
type activeSubAgent struct {
	cancel    context.CancelFunc
	sessionID string
}
