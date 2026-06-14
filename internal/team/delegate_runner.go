
package team

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/charmbracelet/crush/internal/actor"
	"github.com/charmbracelet/crush/internal/agent"
	"github.com/google/uuid"
)

// DelegateRunner manages the lifecycle of parallel read-only delegates. Each
// delegate runs in its own goroutine via an agent.TurnRunner built fresh per
// task from factory.BuildRunner (M1-04 interface). M2 is pure in-memory: no
// DB writes, no team tables. Delegates do not communicate with each other.
//
// NOTE: M1-04 shipped the AgentFactory/TurnRunner interfaces but no
// production implementation. M2-01 depends only on the interface; the real
// factory (backed by sessionAgent) is a follow-up task. Until then the runner
// is exercised by mock-factory tests.
type DelegateRunner struct {
	factory agent.AgentFactory
	mu      sync.Mutex // protects groups
	groups  map[string]*DelegateRunGroup
}

// NewDelegateRunner creates a DelegateRunner backed by the given factory.
func NewDelegateRunner(factory agent.AgentFactory) *DelegateRunner {
	return &DelegateRunner{
		factory: factory,
		groups:  make(map[string]*DelegateRunGroup),
	}
}

// RunGroup launches one goroutine per task and returns immediately. Each
// goroutine builds an AgentSpec + TeamAgentCall, calls factory.BuildRunner
// then runner.Run, and writes the outcome into group.Results[idx] under
// group.mu. A recovered panic is recorded as TurnFailed with a "panic: ..."
// error (acceptance #3). A trailing goroutine waits for all delegates and
// flips group.Status from "running" to "done" (acceptance #4), then
// unregisters the group (acceptance #5: pure in-memory).
//
// RunGroup does NOT bound concurrency in M2-01 (the master doc mentions a
// default of 3 but defers the semaphore to a later task; M2-01's acceptance
// #2 only requires parallel start, which an unbounded fan-out satisfies).
func (d *DelegateRunner) RunGroup(ctx context.Context, tasks []DelegateTask) *DelegateRunGroup {
	groupCtx, groupCancel := context.WithCancel(ctx)

	group := &DelegateRunGroup{
		ID:      uuid.New().String(),
		Tasks:   tasks,
		Results: make([]DelegateResult, len(tasks)),
		Status:  "running",
		ctx:     groupCtx,
		cancel:  groupCancel,
		done:    make(chan struct{}),
	}

	d.mu.Lock()
	d.groups[group.ID] = group
	d.mu.Unlock()

	if len(tasks) == 0 {
		// No tasks: flip to done immediately so Wait() callers observe the
		// final status, then close done (Wait() blocks on it), then
		// unregister.
		group.mu.Lock()
		group.Status = "done"
		group.mu.Unlock()
		d.mu.Lock()
		delete(d.groups, group.ID)
		d.mu.Unlock()
		close(group.done)
		return group
	}

	group.wg.Add(len(tasks))
	policy := delegateReadOnlyPolicy()

	for i, task := range tasks {
		go func(idx int, t DelegateTask) {
			// wg.Done registered first (runs last) so a panic still counts
			// the WaitGroup down; recover registered second (runs before
			// Done) so the panic is captured into the Results slot.
			defer group.wg.Done()
			defer func() {
				if r := recover(); r != nil {
					slog.Error("delegate panic recovered",
						"group_id", group.ID, "task_id", t.ID, "panic", r)
					group.mu.Lock()
					group.Results[idx] = DelegateResult{
						TaskID: t.ID,
						Status: agent.TurnFailed,
						Error:  fmt.Sprintf("panic: %v", r),
					}
					group.mu.Unlock()
				}
			}()

			startTime := time.Now()

			spec := agent.AgentSpec{
				AgentType:      t.AgentID,
				PermissionMode: "default",
				ToolPolicy:     policy,
			}

			runner, err := d.factory.BuildRunner(groupCtx, spec)
			if err != nil {
				dur := time.Since(startTime)
				group.mu.Lock()
				group.Results[idx] = DelegateResult{
					TaskID:     t.ID,
					Status:     agent.TurnFailed,
					Error:      fmt.Sprintf("build runner: %v", err),
					DurationMs: dur.Milliseconds(),
				}
				group.mu.Unlock()
				return
			}

			call := agent.TeamAgentCall{
				// SessionID: a fresh per-delegate id (the delegate owns its
				// own session, distinct from any parent). M2-01 generates a
				// uuid; the production factory may override.
				SessionID:      uuid.New().String(),
				PromptEnvelope: t.Prompt,
				ToolPolicy:     policy,
				Actor: actor.ActorContext{
					SessionID: t.ID,
					TaskID:    t.ID,
					RunID:     group.ID,
				},
			}

			res, runErr := runner.Run(groupCtx, call)
			dur := time.Since(startTime)

			group.mu.Lock()
			switch {
			case errors.Is(runErr, context.Canceled):
				// Cancellation (CancelGroup / parent ctx cancel): record
				// TurnCanceled, not TurnFailed (M2-02 acceptance #2). A
				// deadline-exceeded turn is NOT a cancel — it stays TurnFailed
				// (genuine failure to finish).
				group.Results[idx] = DelegateResult{
					TaskID:     t.ID,
					Status:     agent.TurnCanceled,
					Error:      runErr.Error(),
					DurationMs: dur.Milliseconds(),
				}
			case runErr != nil:
				group.Results[idx] = DelegateResult{
					TaskID:     t.ID,
					Status:     agent.TurnFailed,
					Error:      runErr.Error(),
					DurationMs: dur.Milliseconds(),
				}
			default:
				group.Results[idx] = DelegateResult{
					TaskID:         t.ID,
					Status:         res.Status,
					Result:         res.Result,
					ChildSessionID: call.SessionID,
					DurationMs:     dur.Milliseconds(),
				}
			}
			group.mu.Unlock()

			slog.Debug("delegate finished",
				"group_id", group.ID,
				"task_id", t.ID,
				"duration_ms", dur.Milliseconds())
		}(i, task)
	}

	// Trailing goroutine: flip Status to "done" once every delegate finishes,
	// unregister the group (acceptance #5), then close done. Wait() blocks on
	// wg then done, so a caller returning from Wait() is guaranteed to see the
	// final Status and an unregistered group (Seam 8 — without the done close,
	// Wait() could return before this goroutine flips Status, a real race).
	go func() {
		group.wg.Wait()
		group.mu.Lock()
		if group.Status == "running" {
			group.Status = "done"
		}
		group.mu.Unlock()

		d.mu.Lock()
		delete(d.groups, group.ID)
		d.mu.Unlock()

		slog.Debug("delegate group finished", "group_id", group.ID, "status", group.Status)
		close(group.done)
	}()

	return group
}

// Wait blocks until every delegate in the group has reached a terminal state
// AND the trailing status-flip goroutine has flipped Status and unregistered
// the group. Safe to call concurrently and idempotent (multiple callers all
// unblock on the same close of done). Provided so callers (tests, M2-04 UI,
// M2-05 aggregate) can deterministically observe acceptance #4 (all Results
// filled, final Status visible) and #5 (group unregistered) without sleeping.
//
// The two-phase wait (wg then done) is required: wg alone would let Wait()
// return before the trailing goroutine flips Status — a real race (Seam 8).
func (g *DelegateRunGroup) Wait() {
	g.wg.Wait()
	<-g.done
}

// ActiveGroupCount returns the number of groups currently in flight (started
// but not yet finished). Useful for tests asserting acceptance #5 (the runner
// holds no group after completion).
func (d *DelegateRunner) ActiveGroupCount() int {
	d.mu.Lock()
	defer d.mu.Unlock()
	return len(d.groups)
}

// CancelGroup cancels every in-flight delegate in the named group (M2-02
// acceptance #1, #2). Idempotent (acceptance #3): a repeat call on an
// already-canceled group is a no-op that returns nil. Returns an error only
// if the group id is unknown. The group is unregistered by the trailing
// status-flip goroutine once all workers observe ctx.Done() (acceptance #4).
//
// Implementation delegates to cancelGroup so CancelAllGroups can reuse the
// per-group flip+cancel logic without re-acquiring d.mu (Seam 4 lock order).
func (d *DelegateRunner) CancelGroup(groupID string) error {
	return d.cancelGroup(groupID)
}

// cancelGroup is the per-group flip+cancel logic shared by CancelGroup and
// CancelAllGroups. It looks the group up under d.mu, releases d.mu, then
// takes group.mu — the SAME lock order as the trailing status-flip goroutine
// (group.mu then d.mu, delegate_runner.go trailing goroutine), so the two
// paths can never form an AB-BA deadlock. Idempotent via the group.mu re-check
// of Status == "canceled" (acceptance #3).
func (d *DelegateRunner) cancelGroup(groupID string) error {
	d.mu.Lock()
	group, ok := d.groups[groupID]
	d.mu.Unlock()

	if !ok {
		return fmt.Errorf("delegate group %s not found", groupID)
	}

	group.mu.Lock()
	if group.Status == "canceled" {
		group.mu.Unlock()
		return nil // idempotent
	}
	group.Status = "canceled"
	group.mu.Unlock()

	group.cancel() // ctx.Done() fires in every worker goroutine

	slog.Debug("canceled delegate group", "group_id", groupID)
	return nil
}

// CancelAllGroups cancels every active delegate group the runner currently
// holds. Idempotent and safe to call concurrently with RunGroup / natural
// completion. Groups that have already reached a terminal status ("done" or
// "canceled") are left as-is; only "running" groups are flipped to "canceled"
// and have their context canceled.
//
// Lock order (Seam 4): this method snapshots the groups under d.mu, releases
// d.mu, then takes group.mu per group. This is the OPPOSITE order from the
// trailing status-flip goroutine (group.mu then d.mu, delegate_runner.go
// trailing goroutine), so the two can never form an AB-BA deadlock. Holding
// d.mu across the per-group group.mu.Lock() would deadlock against the
// trailing goroutine.
func (d *DelegateRunner) CancelAllGroups() {
	// Snapshot under d.mu; do NOT hold d.mu while taking group.mu (Seam 4).
	d.mu.Lock()
	groups := make([]*DelegateRunGroup, 0, len(d.groups))
	for _, g := range d.groups {
		groups = append(groups, g)
	}
	d.mu.Unlock()

	for _, g := range groups {
		g.mu.Lock()
		if g.Status == "running" {
			g.Status = "canceled"
			g.mu.Unlock()
			// Safe to cancel after releasing group.mu: cancel takes no group.mu.
			g.cancel()
			slog.Debug("canceled delegate group", "group_id", g.ID)
		} else {
			// Already terminal ("done" via natural completion, or "canceled"
			// via a racing CancelGroup): leave it untouched.
			g.mu.Unlock()
		}
	}
}


