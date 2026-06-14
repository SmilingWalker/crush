
package team

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"charm.land/fantasy"
	"github.com/charmbracelet/crush/internal/agent"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// mockTurnRunner is a TurnRunner that sleeps for `delay` then returns the
// configured TurnRunResult. The optional runErr is returned instead of the
// result; runPanic makes Run panic (to exercise the recover path).
type mockTurnRunner struct {
	delay     time.Duration
	result    agent.TurnRunResult
	runErr    error
	runPanic  bool
}

func (m *mockTurnRunner) Run(ctx context.Context, _ agent.TeamAgentCall) (agent.TurnRunResult, error) {
	if m.runPanic {
		panic("simulated run panic")
	}
	select {
	case <-time.After(m.delay):
	case <-ctx.Done():
		return agent.TurnRunResult{Status: agent.TurnCanceled}, ctx.Err()
	}
	if m.runErr != nil {
		return agent.TurnRunResult{Status: agent.TurnFailed}, m.runErr
	}
	return m.result, nil
}
func (m *mockTurnRunner) Cancel(_ string)             {}
func (m *mockTurnRunner) IsSessionBusy(_ string) bool { return false }

// mockAgentFactory builds a mockTurnRunner per AgentSpec. Per-task behavior is
// selected by the spec's AgentType: a key in overrides (if non-nil) takes
// precedence; otherwise the factory's default delay/result/runErr apply.
// panicTypes triggers a BuildRunner panic for matching AgentTypes. This lets a
// single group mix a panicking task with succeeding tasks (acceptance #3).
type mockAgentFactory struct {
	defaultDelay time.Duration
	defaultResult agent.TurnRunResult
	defaultRunErr error
	panicTypes    map[string]bool      // AgentTypes that panic at BuildRunner
	overrides     map[string]mockBehavior // AgentType -> per-type behavior
}

type mockBehavior struct {
	delay    time.Duration
	result   agent.TurnRunResult
	runErr   error
	runPanic bool
}

func (f *mockAgentFactory) BuildRunner(_ context.Context, spec agent.AgentSpec) (agent.TurnRunner, error) {
	if f.panicTypes[spec.AgentType] {
		panic("simulated build panic")
	}
	b := mockBehavior{
		delay:  f.defaultDelay,
		result: f.defaultResult,
		runErr: f.defaultRunErr,
	}
	if ov, ok := f.overrides[spec.AgentType]; ok {
		b = ov
	}
	return &mockTurnRunner{delay: b.delay, result: b.result, runErr: b.runErr, runPanic: b.runPanic}, nil
}

// completedResult is the standard success TurnRunResult used across tests.
func completedResult() agent.TurnRunResult {
	return agent.TurnRunResult{
		Status: agent.TurnCompleted,
		Result: &fantasy.AgentResult{
			Response:   fantasy.Response{Content: fantasy.ResponseContent{fantasy.TextContent{Text: "ok"}}},
			TotalUsage: fantasy.Usage{InputTokens: 10, OutputTokens: 5},
		},
	}
}

// TestDelegateRunner_ParallelExecution locks acceptance #1, #2, #4: RunGroup
// returns a non-nil group, the three tasks run in parallel (total wall-clock
// is well under 3x the per-task delay), and every Results slot is filled with
// TurnCompleted after the group finishes.
func TestDelegateRunner_ParallelExecution(t *testing.T) {
	factory := &mockAgentFactory{
		defaultDelay:  100 * time.Millisecond,
		defaultResult: completedResult(),
	}
	runner := NewDelegateRunner(factory)

	tasks := []DelegateTask{
		{ID: "1", Prompt: "task 1", AgentID: "explore"},
		{ID: "2", Prompt: "task 2", AgentID: "explore"},
		{ID: "3", Prompt: "task 3", AgentID: "explore"},
	}

	start := time.Now()
	group := runner.RunGroup(context.Background(), tasks)
	require.NotNil(t, group)

	// Wait for all goroutines to finish (acceptance #4: Results fully filled).
	group.Wait()
	elapsed := time.Since(start)

	assert.Equal(t, "done", group.Status)
	require.Len(t, group.Results, 3)
	for i, r := range group.Results {
		assert.Equal(t, agent.TurnCompleted, r.Status, "slot %d not completed", i)
		assert.Equal(t, tasks[i].ID, r.TaskID)
	}

	// Parallel: 3 x 100ms serial = 300ms; parallel should be < 250ms.
	assert.Less(t, elapsed, 250*time.Millisecond,
		"three 100ms tasks ran serially (took %v); expected parallel", elapsed)

	// Acceptance #5: pure in-memory — the runner holds no group after Wait
	// (the trailing goroutine deletes it).
	group.Wait() // idempotent, no-op second call
	assert.Equal(t, 0, runner.ActiveGroupCount())
}

// TestDelegateRunner_GroupStatusFlipsToDone locks acceptance #4's tail: after
// all goroutines finish, Status flips from "running" to "done". Uses a single
// fast task.
func TestDelegateRunner_GroupStatusFlipsToDone(t *testing.T) {
	factory := &mockAgentFactory{defaultDelay: 5 * time.Millisecond, defaultResult: completedResult()}
	runner := NewDelegateRunner(factory)

	group := runner.RunGroup(context.Background(), []DelegateTask{
		{ID: "x", Prompt: "p", AgentID: "explore"},
	})
	assert.Equal(t, "running", group.Status)

	group.Wait()
	assert.Equal(t, "done", group.Status)
}

// TestDelegateRunner_PanicRecovery locks acceptance #3: a goroutine that
// panics (either at BuildRunner or Run) is recovered, its Results slot is
// written with TurnFailed + an error mentioning "panic", and sibling
// delegates in the SAME group are unaffected.
func TestDelegateRunner_PanicRecovery(t *testing.T) {
	t.Run("panic in BuildRunner", func(t *testing.T) {
		factory := &mockAgentFactory{
			panicTypes: map[string]bool{"explore": true},
		}
		runner := NewDelegateRunner(factory)

		group := runner.RunGroup(context.Background(), []DelegateTask{
			{ID: "1", Prompt: "p", AgentID: "explore"},
		})
		group.Wait()

		require.Len(t, group.Results, 1)
		assert.Equal(t, agent.TurnFailed, group.Results[0].Status)
		assert.Contains(t, group.Results[0].Error, "panic")
	})

	t.Run("panic in Run", func(t *testing.T) {
		factory := &mockAgentFactory{
			overrides: map[string]mockBehavior{
				"explore": {runPanic: true},
			},
		}
		runner := NewDelegateRunner(factory)

		group := runner.RunGroup(context.Background(), []DelegateTask{
			{ID: "1", Prompt: "p", AgentID: "explore"},
		})
		group.Wait()

		require.Len(t, group.Results, 1)
		assert.Equal(t, agent.TurnFailed, group.Results[0].Status)
		assert.Contains(t, group.Results[0].Error, "panic")
	})

	t.Run("one panicking delegate does not affect siblings in the same group", func(t *testing.T) {
		// One group, two tasks with different AgentTypes: "panic" panics at
		// build; "ok" completes. This is the real acceptance #3 test — both
		// goroutines share the group, the panic is recovered per-goroutine,
		// and the sibling still writes its result.
		factory := &mockAgentFactory{
			panicTypes: map[string]bool{"panic": true},
			overrides: map[string]mockBehavior{
				"ok": {delay: 5 * time.Millisecond, result: completedResult()},
			},
		}
		runner := NewDelegateRunner(factory)

		group := runner.RunGroup(context.Background(), []DelegateTask{
			{ID: "p", Prompt: "p", AgentID: "panic"},
			{ID: "o", Prompt: "p", AgentID: "ok"},
		})
		group.Wait()

		require.Len(t, group.Results, 2)

		// Locate each result by TaskID (goroutine completion order is not
		// guaranteed, so index by the task's own ID).
		byID := map[string]DelegateResult{}
		for _, r := range group.Results {
			byID[r.TaskID] = r
		}
		assert.Equal(t, agent.TurnFailed, byID["p"].Status)
		assert.Contains(t, byID["p"].Error, "panic")
		assert.Equal(t, agent.TurnCompleted, byID["o"].Status, "sibling must complete despite the panic")
		assert.Equal(t, "done", group.Status, "group still finishes")
	})
}

// TestDelegateRunner_BuildRunnerError locks the non-panic error path: a
// BuildRunner that returns a real error (no panic) is surfaced as TurnFailed
// with the error string.
func TestDelegateRunner_BuildRunnerError(t *testing.T) {
	factory := &errAgentFactory{err: errors.New("no provider configured")}
	runner := NewDelegateRunner(factory)

	group := runner.RunGroup(context.Background(), []DelegateTask{
		{ID: "1", Prompt: "p", AgentID: "explore"},
	})
	group.Wait()

	require.Len(t, group.Results, 1)
	assert.Equal(t, agent.TurnFailed, group.Results[0].Status)
	assert.Contains(t, group.Results[0].Error, "no provider configured")
}

// TestDelegateRunner_RunError locks the runner.Run error path: a Run that
// returns (TurnFailed, err) is surfaced with Status TurnFailed and the error
// string, and DurationMs is recorded.
func TestDelegateRunner_RunError(t *testing.T) {
	factory := &mockAgentFactory{
		defaultDelay: 3 * time.Millisecond,
		defaultRunErr: errors.New("model timed out"),
	}
	runner := NewDelegateRunner(factory)

	group := runner.RunGroup(context.Background(), []DelegateTask{
		{ID: "1", Prompt: "p", AgentID: "explore"},
	})
	group.Wait()

	require.Len(t, group.Results, 1)
	assert.Equal(t, agent.TurnFailed, group.Results[0].Status)
	assert.Contains(t, group.Results[0].Error, "model timed out")
	assert.GreaterOrEqual(t, group.Results[0].DurationMs, int64(0))
}

// TestDelegateRunner_ActorAttribution locks Seam 4: the TeamAgentCall handed
// to the runner carries an ActorContext tagged with the group's RunID and the
// task's ID, so downstream observability can attribute the delegate. It
// captures the call via a recording factory.
func TestDelegateRunner_ActorAttribution(t *testing.T) {
	rec := &recordingFactory{}
	runner := NewDelegateRunner(rec)

	group := runner.RunGroup(context.Background(), []DelegateTask{
		{ID: "task-7", Prompt: "find x", AgentID: "explore"},
	})
	group.Wait()

	require.Len(t, rec.calls, 1)
	call := rec.calls[0]
	assert.Equal(t, "find x", call.PromptEnvelope)
	assert.Equal(t, "task-7", call.Actor.TaskID)
	assert.Equal(t, group.ID, call.Actor.RunID, "Actor.RunID must equal group.ID for attribution")
	assert.NotEmpty(t, call.SessionID, "delegate call must carry a generated SessionID")
	// Policy is the read-only profile.
	assert.Contains(t, call.ToolPolicy.AllowedTools, "view")
	for _, banned := range []string{"bash", "write", "edit"} {
		assert.Contains(t, call.ToolPolicy.DisallowedTools, banned)
	}
}

// errAgentFactory returns a non-panic error from BuildRunner.
type errAgentFactory struct{ err error }

func (f *errAgentFactory) BuildRunner(_ context.Context, _ agent.AgentSpec) (agent.TurnRunner, error) {
	return nil, f.err
}

// recordingFactory captures the TeamAgentCall each Run receives, returning a
// completed result. Used to assert actor attribution / policy wiring.
type recordingFactory struct {
	mu    sync.Mutex
	calls []agent.TeamAgentCall
}

func (f *recordingFactory) BuildRunner(_ context.Context, _ agent.AgentSpec) (agent.TurnRunner, error) {
	return &recordingRunner{factory: f}, nil
}

type recordingRunner struct{ factory *recordingFactory }

func (r *recordingRunner) Run(_ context.Context, call agent.TeamAgentCall) (agent.TurnRunResult, error) {
	r.factory.mu.Lock()
	r.factory.calls = append(r.factory.calls, call)
	r.factory.mu.Unlock()
	return completedResult(), nil
}
func (r *recordingRunner) Cancel(_ string)             {}
func (r *recordingRunner) IsSessionBusy(_ string) bool { return false }
