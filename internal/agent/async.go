package agent

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/charmbracelet/crush/internal/config"
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
//
// Caller contract for StatusChan: drain the channel, or ensure a single run's
// send count stays within the buffer (currently 10; a single run sends at most
// 2 — "running" plus one terminal status). Future periodic progress updates
// must respect this budget or the channel will backpressure the goroutine.
type SubAgentHandle struct {
	RunID      string
	StatusChan <-chan SubAgentStatus
	Cancel     context.CancelFunc
}

// SubAgentStatus is a single observation of an async sub-agent run. State is
// one of the subAgentState* constants. Result is populated when State ==
// "done"; Error is populated when State is "error" or "canceled".
//
// M1-06: Result is the structured *AgentToolResult (migrated from
// *fantasy.ToolResponse per the M1-05 plan's forward-reference). It carries
// content, token usage, tool-call count, and duration so async consumers
// (M2 UI) get the full result without re-running the agent.
type SubAgentStatus struct {
	State    string
	Progress string
	Result   *AgentToolResult
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
		// Registered last so it runs first (LIFO): a panic from runSubAgent
		// (e.g. inside Agent.Run, an interface dispatch into provider code)
		// is recovered into an error status instead of crashing the process.
		// This goroutine is an isolated boundary with no outer recover, unlike
		// the sync request path. The send is non-blocking: if the buffer is
		// full we drop the status rather than deadlock inside recover.
		defer func() {
			if r := recover(); r != nil {
				select {
				case statusChan <- SubAgentStatus{State: subAgentStateError, Error: fmt.Errorf("sub-agent panic: %v", r)}:
				default:
				}
			}
		}()

		statusChan <- SubAgentStatus{State: subAgentStateRunning, Progress: "starting"}

		start := time.Now()
		resp, ar, err := c.runSubAgentStructured(ctx, params)
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
		// agent.Run failure: ar == nil, resp is an error ToolResponse. Surface
		// as an error status (no structured Result).
		if ar == nil {
			statusChan <- SubAgentStatus{State: subAgentStateError, Error: fmt.Errorf("sub-agent failed: %s", resp.Content)}
			return
		}

		result := buildAgentToolResult(ar, config.AgentTask, start, c.sessions.CreateAgentToolSessionID(params.AgentMessageID, params.ToolCallID))
		statusChan <- SubAgentStatus{State: subAgentStateDone, Result: &result}
	}()

	return handle, nil
}
