# M1-06 Structured AgentToolResult Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Give the `agent` tool a structured JSON return value: when a sub-agent completes, the handler serializes an `AgentToolResult` (content + token usage + tool-call count + duration + agent identity) instead of the raw text. The sync `runSubAgent` core is reused unchanged.

**Architecture:** `AgentToolResult`/`Usage`/`buildAgentToolResult`/`countToolCalls` live in a new `agent_result.go`. `runSubAgent` keeps its `(fantasy.ToolResponse, error)` signature (the reusable sync core — used by `TestRunSubAgent` and `runSubAgentAsync`), but a new sibling `runSubAgentStructured` runs the same pipeline and additionally returns the `*fantasy.AgentResult` so the structured builder has access to `TotalUsage` and `Steps`. The `agent` tool handler calls `runSubAgentStructured`, builds an `AgentToolResult`, and returns it as a JSON text response. `SubAgentStatus.Result` is migrated from `*fantasy.ToolResponse` to `*AgentToolResult` per the M1-05 plan's forward-reference, and `runSubAgentAsync` builds it.

**Tech Stack:** Go 1.24, `encoding/json`, `time`, `charm.land/fantasy v0.26.0`, `github.com/stretchr/testify`. Module `github.com/charmbracelet/crush`. Worktree `g:/ai-project/remote-github/crush-worktrees/m1-06`, branch `m1-06-agent-result`. Shell: bash on win32 (forward slashes, `/dev/null`).

---

## Design seam corrections (vs master task doc `02-m1-subagent-foundation.md:1019-1142`)

The master task doc is the approved design. The following are **implementation refinements**, recorded so the reviewer and downstream authors see the deviations:

1. **`runSubAgent` is NOT retyped to return `AgentToolResult`.** The task doc's改动2 shows the handler calling `c.runSubAgent(ctx, params)` and then `json.Marshal(result)` on the return — implying `runSubAgent` returns the structured result. Doing that breaks two unmovable contracts:
   - `TestRunSubAgent` (`coordinator_test.go:88-286`) calls `runSubAgent` directly and asserts `resp.Content` / `resp.IsError` on the returned `fantasy.ToolResponse`, including a subtest `agent run error returns error response` that **locks** the `(errorResponse, nil)` Go-error-swallowing semantic (seam correction #4 below). Retyping the return would require rewriting the entire `TestRunSubAgent` table and would silently change error semantics — out of scope for M1-06.
   - `runSubAgentAsync` (`async.go:113`) does `Result: &result` where `result` is the `fantasy.ToolResponse` return. Retyping forces the async migration to happen first and in lockstep.

   **Decision:** `runSubAgent` keeps `(fantasy.ToolResponse, error)`. The handler instead calls a **new** sibling `runSubAgentStructured(ctx, params) (fantasy.ToolResponse, *fantasy.AgentResult, error)` which runs the identical pipeline and additionally returns the `*fantasy.AgentResult` (seam #2). `runSubAgent` becomes a one-line wrapper that discards the `AgentResult` (DRY — no duplicated session/cost logic). The handler builds the `AgentToolResult` from the `AgentResult`, then serializes.

2. **`*fantasy.AgentResult` is threaded via `runSubAgentStructured`, not retained inside `runSubAgent`.** The task doc's buildAgentToolResult needs `result.TotalUsage` and `result.Steps`, but the current `runSubAgent` (`coordinator.go:1160-1174`) does `result, err := params.Agent.Run(...)` and then only reads `result.Response.Content.Text()`. `runSubAgentStructured` runs the same body and returns the full `result` pointer so the builder has access. This is the minimal change that exposes `AgentResult` without disturbing the sync core's public contract.

3. **fantasy API verification (done).** The master task doc is a design draft; the real `fantasy v0.26.0` API matches it almost exactly, with two notes:
   - `fantasy.AgentResult` is exactly `{Steps []StepResult; Response Response; TotalUsage Usage}` — matches. `StepResult` embeds `Response` (so `step.Content` and `step.Response.Content` both exist) plus `Messages`.
   - `fantasy.Usage` fields are `InputTokens`, `OutputTokens`, `TotalTokens`, `ReasoningTokens`, `CacheCreationTokens`, `CacheReadTokens` (all `int64`, JSON-tagged). `CacheReadTokens` matches the task doc's `result.TotalUsage.CacheReadTokens`. The task doc recomputes `TotalTokens` as `InputTokens + OutputTokens`; fantasy already exposes `TotalTokens` but the task doc's definition (input+output, excluding cache/reasoning) is intentional for the public shape, so we keep the task doc's formula. Verified via `go doc charm.land/fantasy Usage`.
   - `result.Response.Content` is `fantasy.ResponseContent` (`[]Content`) with a `.Text()` method — matches `result.Response.Content.Text()`. Verified.
   - `fantasy.ToolCallContent` exists (`content.go:429`) and is reachable as `step.Content` elements — `countToolCalls`'s type assertion is valid. Verified.

4. **err-path semantic is UNCHANGED: `(errorResponse, nil)`.** `runSubAgent` / `runSubAgentStructured` swallow an `agent.Run` failure into `fantasy.NewTextErrorResponse(...)` with `err == nil` (`coordinator.go:1172-1173`). M1-06 keeps this. Consequence for the handler: on `agent.Run` failure, `runSubAgentStructured` returns `(errorResponse, nil, nil)` — `ar == nil`, so the handler returns the error `ToolResponse` directly (NOT a JSON `AgentToolResult`). This preserves the existing error-reporting contract for the parent agent and keeps the `TestRunSubAgent/agent run error returns error response` subtest green.

5. **`SubAgentStatus.Result` migrates `*fantasy.ToolResponse` → `*AgentToolResult`** (forward-referenced by the M1-05 plan, correction #5). `runSubAgentAsync` now calls `runSubAgentStructured`, builds the `AgentToolResult`, and stores it in the terminal `done` status. The async test `TestRunSubAgentAsync_DoneEmitsRunningThenDone` (`async_test.go:112-114`) is updated to assert on the structured fields (`last.Result.Content`, `last.Result.AgentType`) instead of `last.Result.Content`/`last.Result.IsError` on a `*fantasy.ToolResponse`. This migration is in-scope because the master task doc names `SubAgentStatus.Result` as the consumer of `AgentToolResult`, and leaving it on `*ToolResponse` would make the field meaningless once the structured result exists.

6. **`TotalTokens` field on `AgentToolResult` uses input+output (task doc formula), NOT `Usage.TotalTokens`.** fantasy's `Usage.TotalTokens` may include cache/reasoning tokens depending on provider; the public `AgentToolResult` contract (consumed by the parent agent and, later, M2 UI) defines `total_tokens = input + output` for a stable, provider-agnostic number. Kept as the task doc specifies.

---

## File Structure

**Files touched (scope is STRICT — M1-06 only):**

- Create: `internal/agent/agent_result.go` — `AgentToolResult`, `Usage`, `buildAgentToolResult`, `countToolCalls`.
- Create: `internal/agent/agent_result_test.go` — JSON round-trip, `countToolCalls`, `buildAgentToolResult` from a real-shaped `*fantasy.AgentResult`.
- Modify: `internal/agent/coordinator.go:1131-1182` — extract `runSubAgentStructured` from the `runSubAgent` body; `runSubAgent` becomes a thin wrapper.
- Modify: `internal/agent/agent_tool.go:92-99` — handler calls `runSubAgentStructured`, builds `AgentToolResult`, serializes to JSON.
- Modify: `internal/agent/async.go:38-43,99-113` — `SubAgentStatus.Result` type → `*AgentToolResult`; `runSubAgentAsync` builds it.
- Modify: `internal/agent/async_test.go:112-114` — adapt `DoneEmitsRunningThenDone` assertions to the structured result.

**Files NOT touched:**

- `coordinator.go` `Cancel`/`CancelAll`/`activeSubAgents` (M1-05 surface) — unchanged.
- `subAgentParams` struct — unchanged.
- The `agent` tool's recursion-depth guard (`agent_tool.go:71-80`) — unchanged.

**Pre-existing test interaction (must NOT regress):**

- `TestRunSubAgent` (`coordinator_test.go:88-286`) calls `runSubAgent` directly. Because `runSubAgent` keeps its `(fantasy.ToolResponse, error)` signature and the same body (now delegated), every subtest — happy path, MaxTokens override, canceled-context session failure, provider-not-configured, **agent run error returns error response**, session setup callback, cost propagation — stays GREEN untouched.
- `TestUpdateParentSessionCost` (`coordinator_test.go:288`) constructs coordinators and never calls `runSubAgent`; GREEN untouched.
- `TestCancel_*` / `TestCancelAll_*` (`async_test.go`) do not read `Result`; GREEN untouched. Only `TestRunSubAgentAsync_DoneEmitsRunningThenDone` reads `last.Result` and is updated (Task 4).
- `TestCoordinator_ActiveSubAgentsInitialized` (`async_test.go`) — GREEN untouched.

---

## Task 1: AgentToolResult / Usage / buildAgentToolResult / countToolCalls (TDD)

**Files:**
- Create: `internal/agent/agent_result.go`
- Test: `internal/agent/agent_result_test.go`

- [ ] **Step 1: Write the failing tests**

Create `internal/agent/agent_result_test.go`:

```go
package agent

import (
	"encoding/json"
	"testing"
	"time"

	"charm.land/fantasy"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestAgentToolResult_JSONRoundTrip locks acceptance #1/#2: the structured
// result serializes to legal JSON whose fields survive a round-trip.
func TestAgentToolResult_JSONRoundTrip(t *testing.T) {
	original := AgentToolResult{
		AgentID:           "sess_123",
		AgentType:         "general-purpose",
		Content:           "Found 5 files",
		TotalTokens:       1500,
		TotalToolUseCount: 8,
		TotalDurationMs:   45200,
		Usage: Usage{
			InputTokens:     800,
			OutputTokens:    700,
			CacheReadTokens: 123,
		},
	}

	data, err := json.Marshal(original)
	require.NoError(t, err)
	require.True(t, json.Valid(data), "serialized result must be legal JSON")

	var decoded AgentToolResult
	require.NoError(t, json.Unmarshal(data, &decoded))

	assert.Equal(t, original.AgentID, decoded.AgentID)
	assert.Equal(t, original.AgentType, decoded.AgentType)
	assert.Equal(t, original.Content, decoded.Content)
	assert.Equal(t, original.TotalTokens, decoded.TotalTokens)
	assert.Equal(t, original.TotalToolUseCount, decoded.TotalToolUseCount)
	assert.Equal(t, original.TotalDurationMs, decoded.TotalDurationMs)
	assert.Equal(t, original.Usage, decoded.Usage)
}

// TestCountToolCalls_NoToolCalls locks acceptance #3 for the empty case.
func TestCountToolCalls_NoToolCalls(t *testing.T) {
	result := &fantasy.AgentResult{
		Response: fantasy.Response{
			Content: fantasy.ResponseContent{
				fantasy.TextContent{Text: "just text, no tool calls"},
			},
		},
	}
	assert.Equal(t, 0, countToolCalls(result))
}

// TestCountToolCalls_CountsAcrossSteps locks acceptance #3: tool calls across
// every step are counted exactly once each.
func TestCountToolCalls_CountsAcrossSteps(t *testing.T) {
	result := &fantasy.AgentResult{
		Steps: []fantasy.StepResult{
			{Response: fantasy.Response{Content: fantasy.ResponseContent{
				fantasy.TextContent{Text: "thinking..."},
				fantasy.ToolCallContent{ToolCallID: "c1"},
				fantasy.ToolCallContent{ToolCallID: "c2"},
			}}},
			{Response: fantasy.Response{Content: fantasy.ResponseContent{
				fantasy.ToolCallContent{ToolCallID: "c3"},
			}}},
		},
	}
	assert.Equal(t, 3, countToolCalls(result))
}

// TestBuildAgentToolResult_FromAgentResult locks acceptance #1-#5 together:
// a real-shaped *fantasy.AgentResult is projected into the structured result
// with correct usage tokens, tool-call count, and content text.
func TestBuildAgentToolResult_FromAgentResult(t *testing.T) {
	result := &fantasy.AgentResult{
		Steps: []fantasy.StepResult{
			{Response: fantasy.Response{Content: fantasy.ResponseContent{
				fantasy.ToolCallContent{ToolCallID: "c1"},
			}}},
		},
		Response: fantasy.Response{
			Content: fantasy.ResponseContent{
				fantasy.TextContent{Text: "final answer"},
			},
		},
		TotalUsage: fantasy.Usage{
			InputTokens:     800,
			OutputTokens:    700,
			CacheReadTokens: 50,
		},
	}

	start := time.Now()
	ar := buildAgentToolResult(result, "general-purpose", start, "sess_123")

	assert.Equal(t, "sess_123", ar.AgentID)
	assert.Equal(t, "general-purpose", ar.AgentType)
	assert.Equal(t, "final answer", ar.Content)
	assert.Equal(t, int64(1500), ar.TotalTokens) // 800 + 700
	assert.Equal(t, 1, ar.TotalToolUseCount)
	assert.GreaterOrEqual(t, ar.TotalDurationMs, int64(0))
	assert.Equal(t, int64(800), ar.Usage.InputTokens)
	assert.Equal(t, int64(700), ar.Usage.OutputTokens)
	assert.Equal(t, int64(50), ar.Usage.CacheReadTokens)

	// The built result must itself serialize cleanly (acceptance #1).
	data, err := json.Marshal(ar)
	require.NoError(t, err)
	require.True(t, json.Valid(data))
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd g:/ai-project/remote-github/crush-worktrees/m1-06 && go test ./internal/agent/ -run 'TestAgentToolResult_|TestCountToolCalls_|TestBuildAgentToolResult_' -v`
Expected: COMPILE FAIL — `AgentToolResult` undefined, `Usage` undefined, `buildAgentToolResult` undefined, `countToolCalls` undefined.

- [ ] **Step 3: Write minimal implementation**

Create `internal/agent/agent_result.go`:

```go
package agent

import (
	"time"

	"charm.land/fantasy"
)

// AgentToolResult is the structured result of a sub-agent run. It is serialized
// to JSON and embedded in the ToolResponse.Content returned to the parent agent,
// so the parent can read content, token usage, tool-call count, and duration
// without parsing free text.
type AgentToolResult struct {
	AgentID           string `json:"agent_id"`
	AgentType         string `json:"agent_type"`
	Content           string `json:"content"`
	TotalTokens       int64  `json:"total_tokens"`
	TotalToolUseCount int    `json:"total_tool_use_count"`
	TotalDurationMs   int64  `json:"total_duration_ms"`
	Usage             Usage  `json:"usage"`
}

// Usage is the token-usage breakdown surfaced in AgentToolResult. CacheReadTokens
// is omitted from JSON when zero (providers that don't report cache reads).
type Usage struct {
	InputTokens     int64 `json:"input_tokens"`
	OutputTokens    int64 `json:"output_tokens"`
	CacheReadTokens int64 `json:"cache_read_tokens,omitempty"`
}

// buildAgentToolResult projects a *fantasy.AgentResult into the structured
// AgentToolResult. agentType is the configured agent type (e.g. "task"),
// startTime marks when the run began (for duration), and sessionID is the
// sub-session id that ran the agent.
func buildAgentToolResult(
	result *fantasy.AgentResult,
	agentType string,
	startTime time.Time,
	sessionID string,
) AgentToolResult {
	return AgentToolResult{
		AgentID:           sessionID,
		AgentType:         agentType,
		Content:           result.Response.Content.Text(),
		TotalTokens:       result.TotalUsage.InputTokens + result.TotalUsage.OutputTokens,
		TotalToolUseCount: countToolCalls(result),
		TotalDurationMs:   time.Since(startTime).Milliseconds(),
		Usage: Usage{
			InputTokens:     result.TotalUsage.InputTokens,
			OutputTokens:    result.TotalUsage.OutputTokens,
			CacheReadTokens: result.TotalUsage.CacheReadTokens,
		},
	}
}

// countToolCalls counts the number of tool calls the model made across every
// step of the agent run. Each ToolCallContent element counts once.
func countToolCalls(result *fantasy.AgentResult) int {
	count := 0
	for _, step := range result.Steps {
		for _, content := range step.Content {
			if _, ok := content.(fantasy.ToolCallContent); ok {
				count++
			}
		}
	}
	return count
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `cd g:/ai-project/remote-github/crush-worktrees/m1-06 && go test ./internal/agent/ -run 'TestAgentToolResult_|TestCountToolCalls_|TestBuildAgentToolResult_' -v`
Expected: PASS (all four).

- [ ] **Step 5: Commit**

```bash
cd g:/ai-project/remote-github/crush-worktrees/m1-06
git add internal/agent/agent_result.go internal/agent/agent_result_test.go
git commit -m "feat(agent): add structured AgentToolResult with usage and tool-call count"
```

---

## Task 2: runSubAgentStructured — expose *fantasy.AgentResult (TDD)

**Files:**
- Modify: `internal/agent/coordinator.go:1131-1182`
- Test: `internal/agent/coordinator_test.go` (add a structured subtest)

- [ ] **Step 1: Write the failing test**

Append to `internal/agent/coordinator_test.go` (imports already include `context`, `testing`, `fantasy`, testify):

```go
// TestRunSubAgentStructured_ReturnsAgentResult locks seam #2: the structured
// runner exposes the full *fantasy.AgentResult so the handler can build the
// structured AgentToolResult, while the sync ToolResponse contract is preserved
// (same Content text, same error-swallowing on agent.Run failure).
func TestRunSubAgentStructured_ReturnsAgentResult(t *testing.T) {
	const providerID = "test-provider"
	providerCfg := config.ProviderConfig{ID: providerID}

	t.Run("returns agent result with usage on success", func(t *testing.T) {
		env := testEnv(t)
		coord := newTestCoordinator(t, env, providerID, providerCfg)

		parentSession, err := env.sessions.Create(t.Context(), "Parent")
		require.NoError(t, err)

		agent := newMockAgent(providerID, 4096, func(_ context.Context, _ SessionAgentCall) (*fantasy.AgentResult, error) {
			return &fantasy.AgentResult{
				Response: fantasy.Response{
					Content: fantasy.ResponseContent{fantasy.TextContent{Text: "done"}},
				},
				TotalUsage: fantasy.Usage{
					InputTokens:  100,
					OutputTokens: 50,
				},
			}, nil
		})

		resp, ar, err := coord.runSubAgentStructured(t.Context(), subAgentParams{
			Agent:          agent,
			SessionID:      parentSession.ID,
			AgentMessageID: "msg-1",
			ToolCallID:     "call-1",
			Prompt:         "do something",
			SessionTitle:   "Test Session",
		})
		require.NoError(t, err)
		assert.Equal(t, "done", resp.Content)
		assert.False(t, resp.IsError)
		require.NotNil(t, ar, "AgentResult must be returned on success")
		assert.Equal(t, "done", ar.Response.Content.Text())
		assert.Equal(t, int64(100), ar.TotalUsage.InputTokens)
		assert.Equal(t, int64(50), ar.TotalUsage.OutputTokens)
	})

	t.Run("agent run failure returns error response with nil AgentResult", func(t *testing.T) {
		env := testEnv(t)
		coord := newTestCoordinator(t, env, providerID, providerCfg)

		parentSession, err := env.sessions.Create(t.Context(), "Parent")
		require.NoError(t, err)

		agent := newMockAgent(providerID, 4096, func(_ context.Context, _ SessionAgentCall) (*fantasy.AgentResult, error) {
			return nil, errors.New("provider request failed")
		})

		resp, ar, err := coord.runSubAgentStructured(t.Context(), subAgentParams{
			Agent:          agent,
			SessionID:      parentSession.ID,
			AgentMessageID: "msg-1",
			ToolCallID:     "call-1",
			Prompt:         "test",
			SessionTitle:   "Test",
		})
		// Same swallowing semantic as runSubAgent: (errorResponse, nil, nil).
		require.NoError(t, err)
		assert.True(t, resp.IsError)
		assert.Nil(t, ar, "AgentResult must be nil when agent.Run fails")
	})
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd g:/ai-project/remote-github/crush-worktrees/m1-06 && go test ./internal/agent/ -run TestRunSubAgentStructured_ReturnsAgentResult -v`
Expected: COMPILE FAIL — `coord.runSubAgentStructured` undefined.

- [ ] **Step 3: Write minimal implementation**

Edit `internal/agent/coordinator.go`. Replace the body of `runSubAgent` (current lines 1131-1182) so the real pipeline lives in `runSubAgentStructured`, and `runSubAgent` delegates. The current code to replace is:

```go
// runSubAgent runs a sub-agent and handles session management and cost accumulation.
// It creates a sub-session, runs the agent with the given prompt, and propagates
// the cost to the parent session.
func (c *coordinator) runSubAgent(ctx context.Context, params subAgentParams) (fantasy.ToolResponse, error) {
	// Create sub-session
	agentToolSessionID := c.sessions.CreateAgentToolSessionID(params.AgentMessageID, params.ToolCallID)
	session, err := c.sessions.CreateTaskSession(ctx, agentToolSessionID, params.SessionID, params.SessionTitle)
	if err != nil {
		return fantasy.ToolResponse{}, fmt.Errorf("create session: %w", err)
	}

	// Call session setup function if provided
	if params.SessionSetup != nil {
		params.SessionSetup(session.ID)
	}

	// Get model configuration
	model := params.Agent.Model()
	maxTokens := model.CatwalkCfg.DefaultMaxTokens
	if model.ModelCfg.MaxTokens != 0 {
		maxTokens = model.ModelCfg.MaxTokens
	}

	providerCfg, ok := c.cfg.Config().Providers.Get(model.ModelCfg.Provider)
	if !ok {
		return fantasy.ToolResponse{}, errModelProviderNotConfigured
	}

	// Run the agent
	result, err := params.Agent.Run(ctx, SessionAgentCall{
		SessionID:        session.ID,
		Prompt:           params.Prompt,
		MaxOutputTokens:  maxTokens,
		ProviderOptions:  getProviderOptions(model, providerCfg),
		Temperature:      model.ModelCfg.Temperature,
		TopP:             model.ModelCfg.TopP,
		TopK:             model.ModelCfg.TopK,
		FrequencyPenalty: model.ModelCfg.FrequencyPenalty,
		PresencePenalty:  model.ModelCfg.PresencePenalty,
		NonInteractive:   true,
	})
	if err != nil {
		return fantasy.NewTextErrorResponse(fmt.Sprintf("Failed to generate response: %s", err)), nil
	}

	// Update parent session cost
	if err := c.updateParentSessionCost(ctx, session.ID, params.SessionID); err != nil {
		return fantasy.ToolResponse{}, err
	}

	return fantasy.NewTextResponse(result.Response.Content.Text()), nil
}
```

Replace it with:

```go
// runSubAgent runs a sub-agent and handles session management and cost
// accumulation. It is a thin wrapper over runSubAgentStructured that preserves
// the sync (fantasy.ToolResponse, error) contract used by the agent tool
// handler's pre-M1-06 callers and by runSubAgentAsync.
func (c *coordinator) runSubAgent(ctx context.Context, params subAgentParams) (fantasy.ToolResponse, error) {
	resp, _, err := c.runSubAgentStructured(ctx, params)
	return resp, err
}

// runSubAgentStructured runs the sub-agent pipeline (session creation, model
// run, parent-cost propagation) and additionally returns the *fantasy.AgentResult
// so callers (the agent tool handler, runSubAgentAsync) can build a structured
// AgentToolResult with token usage and tool-call counts.
//
// ar is nil when the run did not produce a result (agent.Run failure, session
// creation failure, provider misconfiguration). On agent.Run failure the error
// is swallowed into an error ToolResponse with err == nil — same semantic as
// runSubAgent, so the parent agent receives a text error, not a Go error.
func (c *coordinator) runSubAgentStructured(ctx context.Context, params subAgentParams) (fantasy.ToolResponse, *fantasy.AgentResult, error) {
	// Create sub-session
	agentToolSessionID := c.sessions.CreateAgentToolSessionID(params.AgentMessageID, params.ToolCallID)
	session, err := c.sessions.CreateTaskSession(ctx, agentToolSessionID, params.SessionID, params.SessionTitle)
	if err != nil {
		return fantasy.ToolResponse{}, nil, fmt.Errorf("create session: %w", err)
	}

	// Call session setup function if provided
	if params.SessionSetup != nil {
		params.SessionSetup(session.ID)
	}

	// Get model configuration
	model := params.Agent.Model()
	maxTokens := model.CatwalkCfg.DefaultMaxTokens
	if model.ModelCfg.MaxTokens != 0 {
		maxTokens = model.ModelCfg.MaxTokens
	}

	providerCfg, ok := c.cfg.Config().Providers.Get(model.ModelCfg.Provider)
	if !ok {
		return fantasy.ToolResponse{}, nil, errModelProviderNotConfigured
	}

	// Run the agent
	result, err := params.Agent.Run(ctx, SessionAgentCall{
		SessionID:        session.ID,
		Prompt:           params.Prompt,
		MaxOutputTokens:  maxTokens,
		ProviderOptions:  getProviderOptions(model, providerCfg),
		Temperature:      model.ModelCfg.Temperature,
		TopP:             model.ModelCfg.TopP,
		TopK:             model.ModelCfg.TopK,
		FrequencyPenalty: model.ModelCfg.FrequencyPenalty,
		PresencePenalty:  model.ModelCfg.PresencePenalty,
		NonInteractive:   true,
	})
	if err != nil {
		return fantasy.NewTextErrorResponse(fmt.Sprintf("Failed to generate response: %s", err)), nil, nil
	}

	// Update parent session cost
	if err := c.updateParentSessionCost(ctx, session.ID, params.SessionID); err != nil {
		return fantasy.ToolResponse{}, nil, err
	}

	return fantasy.NewTextResponse(result.Response.Content.Text()), result, nil
}
```

(No new imports — `fmt`, `fantasy` are already imported in `coordinator.go`.)

- [ ] **Step 4: Run test to verify it passes**

Run: `cd g:/ai-project/remote-github/crush-worktrees/m1-06 && go test ./internal/agent/ -run 'TestRunSubAgentStructured_ReturnsAgentResult' -v`
Expected: PASS (both subtests).

Also confirm the sync core contract is unchanged (the whole `TestRunSubAgent` table must stay green):
Run: `cd g:/ai-project/remote-github/crush-worktrees/m1-06 && go test ./internal/agent/ -run 'TestRunSubAgent|TestUpdateParentSessionCost' -v`
Expected: PASS (all subtests green — proves the refactor preserved the sync contract).

- [ ] **Step 5: Commit**

```bash
cd g:/ai-project/remote-github/crush-worktrees/m1-06
git add internal/agent/coordinator.go internal/agent/coordinator_test.go
git commit -m "feat(agent): expose AgentResult from sub-agent run via runSubAgentStructured"
```

---

## Task 3: agent tool handler serializes AgentToolResult (TDD)

**Files:**
- Modify: `internal/agent/agent_tool.go:49-102`
- Test: `internal/agent/agent_tool_test.go` (create)

- [ ] **Step 1: Write the failing tests**

Create `internal/agent/agent_tool_test.go`. The handler closure is built inside `fantasy.NewParallelAgentTool`; exercising it end-to-end needs a fully-built `SessionAgent` with a real provider/model (not feasible as a unit test). We gate the handler's contract by replaying the **exact call sequence the handler runs** (`runSubAgentStructured` → `buildAgentToolResult` → `json.Marshal` → `NewTextResponse`) and asserting the resulting `ToolResponse.Content` is legal JSON with the expected `AgentToolResult` fields. The handler change itself (Step 3) is a thin glue edit verified by `go build` plus these tests.

```go
package agent

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"charm.land/fantasy"
	"github.com/charmbracelet/crush/internal/config"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestAgentTool_ReturnsStructuredJSON was intentionally removed: the handler
// closure is built inside fantasy.NewParallelAgentTool and exercising it
// end-to-end needs a fully-built SessionAgent + real provider, which is not
// feasible as a focused unit test. The handler contract is gated instead by
// TestAgentTool_HandlerSerializesResult and _HandlerReturnsErrorOnAgentRunFailure
// below, which replay the exact call sequence the handler runs.

// TestAgentTool_HandlerSerializesResult is the focused serialization gate:
// it invokes the handler closure directly by constructing the tool and
// exercising it through a coordinator whose task agent is a mock. Because
// agentTool builds its own SessionAgent internally, we instead verify the
// serialization contract by calling runSubAgentStructured + buildAgentToolResult
// (the exact sequence the handler runs) and asserting the JSON shape.
func TestAgentTool_HandlerSerializesResult(t *testing.T) {
	const providerID = "test-provider"
	providerCfg := setupProviderConfig(providerID)
	env := testEnv(t)
	coord := newTestCoordinator(t, env, providerID, providerCfg)

	parent, err := env.sessions.Create(t.Context(), "Parent")
	require.NoError(t, err)

	agent := newMockAgent(providerID, 4096, func(_ context.Context, _ SessionAgentCall) (*fantasy.AgentResult, error) {
		return &fantasy.AgentResult{
			Response: fantasy.Response{
				Content: fantasy.ResponseContent{fantasy.TextContent{Text: "sub-agent says hi"}},
			},
			TotalUsage: fantasy.Usage{
				InputTokens:  200,
				OutputTokens: 100,
			},
		}, nil
	})

	start := time.Now()
	_, ar, err := coord.runSubAgentStructured(t.Context(), subAgentParams{
		Agent:          agent,
		SessionID:      parent.ID,
		AgentMessageID: "msg-1",
		ToolCallID:     "call-1",
		Prompt:         "do it",
		SessionTitle:   "Sub",
	})
	require.NoError(t, err)
	require.NotNil(t, ar)

	// Exact handler sequence: build with the sub-session id, marshal, wrap.
	subSessionID := coord.sessions.CreateAgentToolSessionID("msg-1", "call-1")
	result := buildAgentToolResult(ar, config.AgentTask, start, subSessionID)
	data, mErr := json.Marshal(result)
	require.NoError(t, mErr)
	assert.True(t, json.Valid(data))

	// The handler wraps the JSON in a text response.
	toolResp := fantasy.NewTextResponse(string(data))
	assert.False(t, toolResp.IsError)

	var decoded AgentToolResult
	require.NoError(t, json.Unmarshal([]byte(toolResp.Content), &decoded))
	assert.Equal(t, config.AgentTask, decoded.AgentType)
	assert.Equal(t, subSessionID, decoded.AgentID)
	assert.Equal(t, "sub-agent says hi", decoded.Content)
	assert.Equal(t, int64(300), decoded.TotalTokens) // 200 + 100
	assert.Equal(t, int64(200), decoded.Usage.InputTokens)
	assert.Equal(t, int64(100), decoded.Usage.OutputTokens)
}

// TestAgentTool_HandlerReturnsErrorOnAgentRunFailure locks seam #4: when the
// sub-agent fails, the handler returns the error ToolResponse directly, NOT a
// JSON AgentToolResult (ar == nil).
func TestAgentTool_HandlerReturnsErrorOnAgentRunFailure(t *testing.T) {
	const providerID = "test-provider"
	providerCfg := setupProviderConfig(providerID)
	env := testEnv(t)
	coord := newTestCoordinator(t, env, providerID, providerCfg)

	parent, err := env.sessions.Create(t.Context(), "Parent")
	require.NoError(t, err)

	agent := newMockAgent(providerID, 4096, func(_ context.Context, _ SessionAgentCall) (*fantasy.AgentResult, error) {
		return nil, context.DeadlineExceeded
	})

	resp, ar, err := coord.runSubAgentStructured(t.Context(), subAgentParams{
		Agent:          agent,
		SessionID:      parent.ID,
		AgentMessageID: "msg-1",
		ToolCallID:     "call-1",
		Prompt:         "do it",
		SessionTitle:   "Sub",
	})
	require.NoError(t, err) // swallowed into the error response
	assert.Nil(t, ar)
	assert.True(t, resp.IsError)

	// The handler path: because ar == nil, the handler returns resp directly
	// (it does NOT attempt to build/marshal an AgentToolResult).
	assert.Contains(t, resp.Content, "Failed to generate response")
	// And resp.Content is NOT legal AgentToolResult JSON.
	var decoded AgentToolResult
	json.Unmarshal([]byte(resp.Content), &decoded)
	assert.Equal(t, AgentToolResult{}, decoded, "error response must not be AgentToolResult JSON")
}
```

The `time` import is already in the import block above. After Tasks 1-2 land, these two subtests PASS immediately (they exercise `runSubAgentStructured` + `buildAgentToolResult`, both implemented). They are the binding gate for the handler's serialization contract; Step 3's handler edit is the production change, verified by `go build` plus these tests.

- [ ] **Step 2: Run tests to verify they pass**

Run: `cd g:/ai-project/remote-github/crush-worktrees/m1-06 && go test ./internal/agent/ -run 'TestAgentTool_' -v`
Expected: PASS (both `Handler*` subtests green). If they FAIL, `runSubAgentStructured` or `buildAgentToolResult` from Tasks 1-2 regressed — fix before proceeding. (Writing these tests after the implementation is intentional: they verify the handler's call sequence rather than drive new code; the production change is the handler glue in Step 3.)

- [ ] **Step 3: Write minimal implementation**

Edit `internal/agent/agent_tool.go`. First add `"encoding/json"` and `"time"` to the import block (current lines 3-14):

```go
import (
	"context"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"charm.land/fantasy"

	"github.com/charmbracelet/crush/internal/agent/prompt"
	"github.com/charmbracelet/crush/internal/agent/tools"
	"github.com/charmbracelet/crush/internal/config"
)
```

Then replace the handler's `return c.runSubAgent(...)` block (current lines 92-99):

```go
			return c.runSubAgent(ctx, subAgentParams{
				Agent:          agent,
				SessionID:      sessionID,
				AgentMessageID: agentMessageID,
				ToolCallID:     call.ID,
				Prompt:         params.Prompt,
				SessionTitle:   "New Agent Session",
			})
```

with the structured build:

```go
			start := time.Now()
			resp, ar, err := c.runSubAgentStructured(ctx, subAgentParams{
				Agent:          agent,
				SessionID:      sessionID,
				AgentMessageID: agentMessageID,
				ToolCallID:     call.ID,
				Prompt:         params.Prompt,
				SessionTitle:   "New Agent Session",
			})
			if err != nil {
				return resp, err
			}
			// agent.Run failure: runSubAgentStructured returns an error
			// ToolResponse with ar == nil — surface it directly, do not wrap.
			if ar == nil {
				return resp, nil
			}
			result := buildAgentToolResult(ar, config.AgentTask, start, c.sessions.CreateAgentToolSessionID(agentMessageID, call.ID))
			resultJSON, mErr := json.Marshal(result)
			if mErr != nil {
				return fantasy.NewTextErrorResponse(fmt.Sprintf("failed to serialize agent result: %s", mErr)), nil
			}
			return fantasy.NewTextResponse(string(resultJSON)), nil
```

**AgentID source:** `runSubAgentStructured` computes the sub-session id internally as `c.sessions.CreateAgentToolSessionID(agentMessageID, toolCallID)` but does not return it. Rather than expand the return tuple, the handler recomputes it with the same call. `CreateAgentToolSessionID` is a pure deterministic function, so the two calls yield the same id and `AgentToolResult.AgentID` matches the sub-session that actually ran the agent. This keeps the handler change to a single file (`agent_tool.go`), no new helper.

- [ ] **Step 4: Run test to verify it passes**

Run: `cd g:/ai-project/remote-github/crush-worktrees/m1-06 && go test ./internal/agent/ -run 'TestAgentTool_HandlerSerializesResult|TestAgentTool_HandlerReturnsErrorOnAgentRunFailure' -v`
Expected: PASS (both).

- [ ] **Step 5: Commit**

```bash
cd g:/ai-project/remote-github/crush-worktrees/m1-06
git add internal/agent/agent_tool.go internal/agent/agent_tool_test.go
git commit -m "feat(agent): serialize structured AgentToolResult from agent tool handler"
```

---

## Task 4: migrate SubAgentStatus.Result to *AgentToolResult (TDD)

**Files:**
- Modify: `internal/agent/async.go:38-43,99-113`
- Modify: `internal/agent/async_test.go:112-114`

- [ ] **Step 1: Update the failing test first**

Edit `internal/agent/async_test.go`. The `TestRunSubAgentAsync_DoneEmitsRunningThenDone` subtest currently asserts (lines 112-114):

```go
	require.NotNil(t, last.Result)
	assert.Equal(t, "all good", last.Result.Content)
	assert.False(t, last.Result.IsError)
```

Replace those three lines with assertions against the structured result:

```go
	require.NotNil(t, last.Result)
	assert.Equal(t, "all good", last.Result.Content)
	assert.Equal(t, config.AgentTask, last.Result.AgentType)
	assert.GreaterOrEqual(t, last.Result.TotalDurationMs, int64(0))
```

This requires `internal/config` to be imported in `async_test.go`. Check the current import block of `async_test.go`; if `config` is not imported, add `"github.com/charmbracelet/crush/internal/config"`.

(The mock agent's runFunc returns `agentResultWithText("all good")`, which has no `AgentType` metadata — `AgentType` is supplied by the caller (`runSubAgentAsync`), so it is `config.AgentTask` as wired in Task 4 Step 3. `Content` comes from `result.Response.Content.Text()` = "all good".)

- [ ] **Step 2: Run test to verify it fails**

Run: `cd g:/ai-project/remote-github/crush-worktrees/m1-06 && go test ./internal/agent/ -run TestRunSubAgentAsync_DoneEmitsRunningThenDone -v`
Expected: COMPILE FAIL — `last.Result.AgentType` undefined because `last.Result` is still `*fantasy.ToolResponse`. (Or, if the test was edited but `async.go` not yet, it fails to compile on `last.Result.AgentType`.)

- [ ] **Step 3: Write minimal implementation**

Edit `internal/agent/async.go`. Change the `SubAgentStatus.Result` field type (current line 41) and the `runSubAgentAsync` goroutine (current lines 99-113).

First, update the import block (current lines 3-10). After migrating `SubAgentStatus.Result` to `*AgentToolResult`, `async.go` has NO remaining `fantasy.` references (verified: line 41 was the only one), so the `charm.land/fantasy` import MUST be removed or the build breaks. Add `time` and `config`; drop `fantasy`:

```go
import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/charmbracelet/crush/internal/config"
	"github.com/google/uuid"
)
```

Update the `SubAgentStatus` struct comment + field (current lines 35-43):

```go
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
```

Update the goroutine's done path (current lines 99-113). Replace:

```go
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
```

with:

```go
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
```

(`resp` is read in the `ar == nil` branch (`resp.Content`), so it stays used. The `fantasy` import was removed in the import-block edit above — `async.go` no longer references any `fantasy.` symbol after the `SubAgentStatus.Result` type change, confirmed by grep on the pre-change file.)

- [ ] **Step 4: Run test to verify it passes**

Run: `cd g:/ai-project/remote-github/crush-worktrees/m1-06 && go test ./internal/agent/ -run TestRunSubAgentAsync_DoneEmitsRunningThenDone -v`
Expected: PASS.

Confirm the full async + cancel surface is still green (no regression in the cancel/error/leak paths):
Run: `cd g:/ai-project/remote-github/crush-worktrees/m1-06 && go test ./internal/agent/ -run 'TestRunSubAgentAsync|TestCancel|TestCancelAll|TestCoordinator_ActiveSubAgentsInitialized' -v -race`
Expected: PASS (all green), no race warnings.

- [ ] **Step 5: Commit**

```bash
cd g:/ai-project/remote-github/crush-worktrees/m1-06
git add internal/agent/async.go internal/agent/async_test.go
git commit -m "feat(agent): migrate SubAgentStatus.Result to structured AgentToolResult"
```

---

## Final verification (acceptance gate)

Run the whole M1-06 surface plus the regression set, with the race detector:

```bash
cd g:/ai-project/remote-github/crush-worktrees/m1-06
go test ./internal/agent/ -run 'TestAgentToolResult_|TestCountToolCalls_|TestBuildAgentToolResult_|TestRunSubAgentStructured_|TestRunSubAgent$|TestRunSubAgentAsync|TestCancel|TestCancelAll|TestUpdateParentSessionCost|TestCoordinator_ActiveSubAgentsInitialized' -v -race
go build ./...
```

Acceptance criteria mapping (master task doc:1110-1116):
1. Sub-agent completion → ToolResponse.Content is legal JSON — `TestAgentTool_HandlerSerializesResult` (asserts `json.Valid`), `TestAgentToolResult_JSONRoundTrip`.
2. JSON deserializes to AgentToolResult with real `agent_type` — `TestAgentTool_HandlerSerializesResult` (decodes `agent_type` = `config.AgentTask`), `TestBuildAgentToolResult_FromAgentResult`.
3. `total_tool_use_count` correct — `TestCountToolCalls_CountsAcrossSteps`, `TestCountToolCalls_NoToolCalls`, `TestBuildAgentToolResult_FromAgentResult`.
4. `total_duration_ms` is real elapsed — `TestBuildAgentToolResult_FromAgentResult` (`>= 0`), `TestRunSubAgentAsync_DoneEmitsRunningThenDone`.
5. `usage.input_tokens` / `usage.output_tokens` match the model response — `TestBuildAgentToolResult_FromAgentResult`, `TestAgentTool_HandlerSerializesResult`, `TestRunSubAgentStructured_ReturnsAgentResult`.

Regression guard: `TestRunSubAgent` (sync core contract, untouched), `TestUpdateParentSessionCost`, `TestCancel_*`, `TestCancelAll_*`, `TestCoordinator_ActiveSubAgentsInitialized` all stay green — the `runSubAgent` wrapper preserves the old return shape and the async cancel machinery is untouched.

If everything is green, the work is complete. Report back to the team-lead with the test output and the commit list.
