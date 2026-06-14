
// Package team implements the M2 delegate runtime: parallel read-only
// sub-agent dispatch (DelegateRunner) and its result aggregation types
// (DelegateRunGroup). M2 is pure in-memory — no DB writes, no team tables.
// The runner depends only on the agent.AgentFactory / agent.TurnRunner
// interfaces defined in internal/agent/team_call.go (M1-04).
package team

import (
	"context"
	"sync"

	"charm.land/fantasy"
	"github.com/charmbracelet/crush/internal/agent"
)

// DelegateTask describes one task a delegate executes. ID is the caller's
// unique identifier for the task (used to attribute results). Prompt is the
// assembled prompt envelope handed to the delegate's TurnRunner. AgentID is
// the agent type the delegate runs as (e.g. "general-purpose" | "explore" |
// "plan"); it flows into AgentSpec.AgentType.
type DelegateTask struct {
	ID      string `json:"id"`
	Prompt  string `json:"prompt"`
	AgentID string `json:"agent_id"`
}

// DelegateResult is the outcome of one delegate task. Status is the terminal
// TurnStatus the runner returned (completed | canceled | failed). Result is
// the *fantasy.AgentResult on success (nil on failure). Error carries a
// human-readable failure reason (build-runner error, run error, or recovered
// panic). DurationMs is wall-clock from goroutine start to result write.
type DelegateResult struct {
	TaskID         string              `json:"task_id"`
	Status         agent.TurnStatus    `json:"status"`
	Result         *fantasy.AgentResult `json:"-"`
	ChildSessionID string              `json:"child_session_id,omitempty"`
	Error          string              `json:"error,omitempty"`
	DurationMs     int64               `json:"duration_ms"`
}

// DelegateRunGroup manages the lifecycle of a set of parallel delegates.
// RunGroup pre-allocates Results to len(Tasks) zero-value slots; a slot's
// Status == "" (the zero TurnStatus) means "still running" until the slot's
// goroutine writes a terminal status. M2 is pure in-memory — the group is
// never persisted.
type DelegateRunGroup struct {
	ID      string           `json:"id"`
	Tasks   []DelegateTask   `json:"tasks"`
	Results []DelegateResult `json:"results"`
	Status  string           `json:"status"` // "running" | "partial" | "done" | "canceled"

	mu     sync.Mutex
	wg     sync.WaitGroup
	// ctx and cancel are owned by DelegateRunner.RunGroup (set there in
	// Task 2). Declared on this struct so RunGroup and future cancel/error
	// paths (M2-02) can reach them without a second allocation.
	ctx    context.Context
	cancel context.CancelFunc
	// done is closed by the trailing status-flip goroutine AFTER Status has
	// been flipped and the group unregistered. Wait() blocks on wg then done,
	// so a caller returning from Wait() is guaranteed to see the final Status
	// (acceptance #4 — without this, Wait() could return before the trailing
	// goroutine flips Status from "running" to "done", a real race).
	done chan struct{}
}

// RunningCount returns the number of delegate slots still in flight (Status
// not yet set to a terminal value).
func (g *DelegateRunGroup) RunningCount() int {
	g.mu.Lock()
	defer g.mu.Unlock()
	count := 0
	for _, r := range g.Results {
		if r.Status == "" { // zero TurnStatus: not yet filled
			count++
		}
	}
	return count
}

// DoneCount returns the number of delegates that completed successfully.
func (g *DelegateRunGroup) DoneCount() int {
	g.mu.Lock()
	defer g.mu.Unlock()
	count := 0
	for _, r := range g.Results {
		if r.Status == agent.TurnCompleted {
			count++
		}
	}
	return count
}

// FailedCount returns the number of delegates that failed or were canceled.
func (g *DelegateRunGroup) FailedCount() int {
	g.mu.Lock()
	defer g.mu.Unlock()
	count := 0
	for _, r := range g.Results {
		if r.Status == agent.TurnCanceled || r.Status == agent.TurnFailed {
			count++
		}
	}
	return count
}

// TotalTokens sums InputTokens+OutputTokens across every delegate result that
// carries a *fantasy.AgentResult. Slots with a nil Result contribute zero.
func (g *DelegateRunGroup) TotalTokens() int64 {
	g.mu.Lock()
	defer g.mu.Unlock()
	var total int64
	for _, r := range g.Results {
		if r.Result != nil {
			total += r.Result.TotalUsage.InputTokens + r.Result.TotalUsage.OutputTokens
		}
	}
	return total
}
