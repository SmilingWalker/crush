package agent

import (
	"context"
	"errors"

	"charm.land/fantasy"
	"github.com/google/uuid"
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

// runSubAgentAsync launches params in a background goroutine and returns a
// SubAgentHandle immediately (non-blocking). The goroutine runs the existing
// sync pipeline (runSubAgent) on a derived cancellable context, streams status
// onto handle.StatusChan, and unregisters itself on exit.
//
// A run is classified as "canceled" when ctx.Err() is non-nil after runSubAgent
// returns — runSubAgent itself swallows agent.Run failures into an error
// response with err == nil, so ctx.Err() is the reliable signal.
func (c *coordinator) runSubAgentAsync(ctx context.Context, params subAgentParams) (SubAgentHandle, error) {
	runID := uuid.New().String()
	ctx, cancel := context.WithCancel(ctx)

	c.activeSubAgentsMu.Lock()
	c.activeSubAgents[runID] = &activeSubAgent{cancel: cancel, sessionID: params.SessionID}
	c.activeSubAgentsMu.Unlock()

	statusChan := make(chan SubAgentStatus, 10)
	handle := SubAgentHandle{
		RunID:      runID,
		StatusChan: statusChan,
		Cancel:     cancel,
	}

	go func() {
		defer close(statusChan)
		defer func() {
			c.activeSubAgentsMu.Lock()
			delete(c.activeSubAgents, runID)
			c.activeSubAgentsMu.Unlock()
		}()

		statusChan <- SubAgentStatus{State: subAgentStateRunning, Progress: "starting"}

		result, err := c.runSubAgent(ctx, params)
		if ctx.Err() != nil {
			statusChan <- SubAgentStatus{State: subAgentStateCanceled, Error: ctx.Err()}
			return
		}
		if err != nil {
			state := subAgentStateError
			if errors.Is(err, context.Canceled) {
				state = subAgentStateCanceled
			}
			statusChan <- SubAgentStatus{State: state, Error: err}
			return
		}

		statusChan <- SubAgentStatus{State: subAgentStateDone, Result: &result}
	}()

	return handle, nil
}
