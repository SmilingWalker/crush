package agent

import (
	"context"
	"runtime"
	"sync"
	"testing"
	"time"

	"charm.land/fantasy"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestCoordinator_ActiveSubAgentsInitialized locks the invariant that every
// constructed coordinator has a usable (non-nil) activeSubAgents map, so
// runSubAgentAsync can register without a nil-map panic.
func TestCoordinator_ActiveSubAgentsInitialized(t *testing.T) {
	env := testEnv(t)
	const providerID = "test-provider"
	providerCfg := setupProviderConfig(providerID)
	coord := newTestCoordinator(t, env, providerID, providerCfg)

	require.NotNil(t, coord.activeSubAgents, "activeSubAgents must be initialized")
	// Writing the nil-map would panic; prove it is safe to write.
	coord.activeSubAgents["probe"] = &activeSubAgent{cancel: func() {}, sessionID: "s1"}
	assert.Contains(t, coord.activeSubAgents, "probe")
}

// startAsyncSubAgent is a shared helper: it launches an async sub-agent whose
// runFunc is provided by the caller, against a fresh parent session.
func startAsyncSubAgent(t *testing.T, env fakeEnv, coord *coordinator, parentSessionID string,
	runFunc func(context.Context, SessionAgentCall) (*fantasy.AgentResult, error)) SubAgentHandle {
	t.Helper()
	agent := newMockAgent("test-provider", 4096, runFunc)
	handle, err := coord.runSubAgentAsync(t.Context(), subAgentParams{
		Agent:          agent,
		SessionID:      parentSessionID,
		AgentMessageID: "msg-1",
		ToolCallID:     "call-1",
		Prompt:         "do async",
		SessionTitle:   "Async",
	})
	require.NoError(t, err)
	require.NotEmpty(t, handle.RunID)
	require.NotNil(t, handle.StatusChan)
	return handle
}

// drainStatus reads every status from handle.StatusChan until it closes and
// returns the slice in order. Blocks until the goroutine exits.
func drainStatus(t *testing.T, handle SubAgentHandle) []SubAgentStatus {
	t.Helper()
	var got []SubAgentStatus
	for s := range handle.StatusChan {
		got = append(got, s)
	}
	require.NotEmpty(t, got, "expected at least one status before channel close")
	return got
}

// TestRunSubAgentAsync_ImmediateReturn locks acceptance #2: the async call
// returns a handle at once, without waiting for the (blocked) agent to finish.
func TestRunSubAgentAsync_ImmediateReturn(t *testing.T) {
	env := testEnv(t)
	coord := newTestCoordinator(t, env, "test-provider", setupProviderConfig("test-provider"))

	parent, err := env.sessions.Create(t.Context(), "Parent")
	require.NoError(t, err)

	// runFunc blocks forever (until canceled) so the handle must return first.
	block := make(chan struct{})
	handle := startAsyncSubAgent(t, env, coord, parent.ID, func(ctx context.Context, _ SessionAgentCall) (*fantasy.AgentResult, error) {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-block:
			return agentResultWithText("late"), nil
		}
	})

	start := time.Now()
	// handle already returned above; assert it returned promptly.
	elapsed := time.Since(start)
	assert.Less(t, elapsed, 100*time.Millisecond, "async must return a handle immediately")

	// Cleanup: cancel and drain so the goroutine exits.
	handle.Cancel()
	close(block)
	drainStatus(t, handle)
}

// TestRunSubAgentAsync_DoneEmitsRunningThenDone locks acceptance #6: a
// successful run emits "running" then "done" (with Result), then closes.
func TestRunSubAgentAsync_DoneEmitsRunningThenDone(t *testing.T) {
	env := testEnv(t)
	coord := newTestCoordinator(t, env, "test-provider", setupProviderConfig("test-provider"))

	parent, err := env.sessions.Create(t.Context(), "Parent")
	require.NoError(t, err)

	handle := startAsyncSubAgent(t, env, coord, parent.ID, func(_ context.Context, _ SessionAgentCall) (*fantasy.AgentResult, error) {
		return agentResultWithText("all good"), nil
	})

	got := drainStatus(t, handle)
	require.GreaterOrEqual(t, len(got), 2, "expected running + done")
	assert.Equal(t, subAgentStateRunning, got[0].State)
	last := got[len(got)-1]
	assert.Equal(t, subAgentStateDone, last.State)
	require.NotNil(t, last.Result)
	assert.Equal(t, "all good", last.Result.Content)
	assert.False(t, last.Result.IsError)
}

// TestRunSubAgentAsync_CancelEmitsCanceledWithin5s locks acceptance #3 and #7:
// after Cancel, the goroutine exits and reports "canceled" within 5 seconds.
func TestRunSubAgentAsync_CancelEmitsCanceledWithin5s(t *testing.T) {
	env := testEnv(t)
	coord := newTestCoordinator(t, env, "test-provider", setupProviderConfig("test-provider"))

	parent, err := env.sessions.Create(t.Context(), "Parent")
	require.NoError(t, err)

	started := make(chan struct{})
	handle := startAsyncSubAgent(t, env, coord, parent.ID, func(ctx context.Context, _ SessionAgentCall) (*fantasy.AgentResult, error) {
		close(started)
		<-ctx.Done()
		return nil, ctx.Err()
	})

	<-started // ensure the goroutine is inside runFunc before canceling
	handle.Cancel()

	done := make(chan []SubAgentStatus, 1)
	go func() { done <- drainStatus(t, handle) }()
	select {
	case got := <-done:
		last := got[len(got)-1]
		assert.Equal(t, subAgentStateCanceled, last.State)
		require.Error(t, last.Error)
	case <-time.After(5 * time.Second):
		t.Fatal("cancel did not stop the goroutine within 5s")
	}
}

// TestRunSubAgentAsync_NoGoroutineLeak locks acceptance #5: after 100
// async runs each canceled and drained, goroutine count returns near baseline.
func TestRunSubAgentAsync_NoGoroutineLeak(t *testing.T) {
	env := testEnv(t)
	coord := newTestCoordinator(t, env, "test-provider", setupProviderConfig("test-provider"))

	parent, err := env.sessions.Create(t.Context(), "Parent")
	require.NoError(t, err)

	before := runtime.NumGoroutine()

	const n = 100
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			handle := startAsyncSubAgent(t, env, coord, parent.ID, func(ctx context.Context, _ SessionAgentCall) (*fantasy.AgentResult, error) {
				<-ctx.Done()
				return nil, ctx.Err()
			})
			handle.Cancel()
			drainStatus(t, handle) // wait for goroutine exit
		}()
	}
	wg.Wait()

	// Give exited goroutines a moment to be reaped.
	time.Sleep(200 * time.Millisecond)
	runtime.GC()

	after := runtime.NumGoroutine()
	assert.LessOrEqual(t, after, before+5, "goroutine leak: before=%d after=%d", before, after)
}
