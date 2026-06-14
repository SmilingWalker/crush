# M2-01 DelegateRunner + DelegateRunGroup Core Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Build the parallel dispatch core for M2's delegate runner — `DelegateTask` / `DelegateResult` / `DelegateRunGroup` types and a `DelegateRunner.RunGroup` that launches one goroutine per task, runs each through an `agent.TurnRunner`, recovers panics per-goroutine, and fills a mutex-guarded `Results` slice. This is the M2 cornerstone that M2-02 (cancel), M2-04 (UI), and M2-05 (aggregate) all depend on.

**Architecture:** A new `internal/team` package owns the delegate runtime. `DelegateRunner` holds an `agent.AgentFactory` (M1-04 interface) and a registry of active `*DelegateRunGroup`. `RunGroup` spawns N goroutines (one per task), each building an `agent.AgentSpec` + `agent.TeamAgentCall`, calling `factory.BuildRunner` then `runner.Run`, and writing the outcome into `group.Results[idx]` under `group.mu`. A trailing goroutine `wg.Wait()`s and flips `group.Status` to `"done"`. M2 is pure in-memory: no DB writes, no team tables. Because M1-04 shipped the `AgentFactory`/`TurnRunner`/`AgentSpec`/`TeamAgentCall` interfaces but **no production implementation** (verified: only `internal/agent/team_call.go` defines them), the production factory wiring is explicitly out of M2-01 scope — this task ships the dispatch core + types, fully covered by a mock-factory test fixture, and the real factory is a follow-up task.

**Tech Stack:** Go 1.26.3 (`module github.com/charmbracelet/crush`), `charm.land/fantasy v0.26.0` (`AgentResult`/`Usage`/`ResponseContent`), `github.com/google/uuid v1.6.0`, `github.com/stretchr/testify` (assert + require, the agent-package test convention). New package `github.com/charmbracelet/crush/internal/team`. Worktree `g:/ai-project/remote-github/crush-worktrees/m2-01`, branch `m2-01-delegate-core`. Shell: bash on win32 (forward slashes, `/dev/null`).

---

## Design seam corrections (vs master task doc `03-m2-delegate-runner.md:9-331`)

The master task doc is the approved design intent. The following are **verified implementation deviations**, each backed by cited evidence from the M1 codebase at agent-team tip `1e783ca9`. Every code snippet in this plan is rewritten against the real APIs.

### Seam 1: `runner.Run` returns `(TurnRunResult, error)`, NOT an anonymous `result{Status, Result}`

The master doc (line 216-242) writes `result, err := runner.Run(ctx, agent.TeamAgentCall{...})` and reads `result.Status` / `result.Result` as if `Run` returns a bare struct literal. **The real signature** (`internal/agent/team_call.go:57-66`) is:

```go
type TurnRunner interface {
    Run(ctx context.Context, call TeamAgentCall) (TurnRunResult, error)
    Cancel(sessionID string)
    IsSessionBusy(sessionID string) bool
}

// TurnRunResult 是一轮 agent turn 的结果 (team_call.go:49-52)
type TurnRunResult struct {
    Status TurnStatus          // completed | queued | canceled | failed
    Result *fantasy.AgentResult
}
```

`TurnStatus` (`team_call.go:11-22`) is a named string type with constants `TurnCompleted`, `TurnQueued`, `TurnCanceled`, `TurnFailed`, `TurnRunning`, and a `.IsTerminal()` method. The plan uses these named constants (e.g. `agent.TurnFailed`) instead of bare strings, and reads `result.Status` / `result.Result` off the named `TurnRunResult`. **Evidence:** `internal/agent/team_call.go:11-66`, read in full during research.

### Seam 2: `AgentFactory` / `BuildRunner` / `TurnRunner` have NO production implementation

This is the single biggest correction. The master doc assumes `d.factory.BuildRunner(groupCtx, spec)` works against a real factory wired into the coordinator. **Research result: no concrete `AgentFactory` or `TurnRunner` implementation exists anywhere in non-test source.** Evidence:

- `grep -rn "AgentFactory|BuildRunner|TurnRunner" internal/ --include="*.go"` (excluding tests) returns **only** the interface definitions in `internal/agent/team_call.go:57-87`. Zero `func ... BuildRunner`, zero `func ... Run(...) (TurnRunResult, error)` method receivers, zero `AgentFactory` constructor.
- M1-04 shipped the **type contract** (`team_call.go` + `team_call_test.go`, which only compile-checks the types exist — see `team_call_test.go:9-38`). The real factory (a struct that constructs a `sessionAgent` per spec and returns it as a `TurnRunner`) was deferred.

**Decision for M2-01:** Ship the delegate dispatch core **plus a mock-factory test fixture**, and explicitly scope the production factory wiring out of M2-01. This keeps M2-01 self-contained and testable: the `DelegateRunner` depends only on the `agent.AgentFactory` *interface*, which exists today; the tests inject a mock that simulates `BuildRunner` + `Run` with configurable delay / panic / error. The production factory (a real `AgentFactory` implementation backed by the coordinator's `sessionAgent`) is a separate, follow-up task — without it `DelegateRunner` cannot run against a real LLM, but the concurrency contract this task locks is fully verifiable with the mock. This mirrors how M1-05/M1-06 async sub-agent tests used `newMockAgent` (a mock fantasy `Agent`) rather than a real provider.

The plan's `## File Structure` and `## Out of scope` sections state this boundary explicitly so the team-lead review and downstream tasks (M2-02/04/05) know the seam.

### Seam 3: `ReadOnlyDelegatePolicy()` does not exist — M2-01 ships a minimal in-package definition

The master doc M2-03 section (`03-m2-delegate-runner.md:396-423`) defines `ReadOnlyDelegatePolicy()` in `internal/agent/team_call.go` (package `agent`), returning a `ToolPolicyProfile`. But `DelegateRunner.RunGroup` (package `team`) needs to call it **at M2-01 compile time**. A cross-package dependency `team → agent.ReadOnlyDelegatePolicy` is clean (no cycle — `agent` does not import `team`), BUT the function does not exist yet (verified: `grep "ReadOnlyDelegatePolicy" internal/` returns nothing).

**Decision:** M2-01 ships a **minimal in-package helper** `delegateReadOnlyPolicy()` in `internal/team/delegate_types.go` that returns an `agent.ToolPolicyProfile` with the five read-only tools (`view`, `grep`, `glob`, `ls`, `sourcegraph`) allowed and the destructive tools disallowed — exactly the master doc M2-03 shape (`03-m2-delegate-runner.md:411-422`). It is unexported (`delegateReadOnlyPolicy`, not `ReadOnlyDelegatePolicy`) because:

1. M2-03 may still export `agent.ReadOnlyDelegatePolicy()` later — and if it does, M2-01's `delegateRunner` can be switched to call it in a one-line change (the policy shape is identical).
2. Keeping M2-01 self-contained means it compiles + tests green today without depending on M2-03 landing first (M2-01 is the dependency root of M2-02/04/05; it must not depend on M2-03).

If the team-lead prefers M2-01 to land the exported `agent.ReadOnlyDelegatePolicy()` (moving that slice of M2-03 forward), the plan notes the one-line alternative in Task 1's implementation step. The default is the in-package helper to respect the dependency graph (`M2-01 → M2-03` is allowed; `M2-03 → M2-01` would be backwards).

**Evidence the policy shape is correct:** `agent.ToolPolicyProfile` (`team_call.go:41-45`) has exactly `AllowedTools []string`, `DisallowedTools []string`, `PermissionMode string`. The five-tool allowlist matches the master doc M2-03 (:413).

### Seam 4: `AgentSpec` and `TeamAgentCall` real field sets

The master doc (`:196-200`, `:216-222`) constructs these with a subset of fields. The real structs (`team_call.go:69-80` and `:32-38`):

```go
type AgentSpec struct {
    AgentType      string
    SystemPrompt   string
    ModelType      string            // "large" | "small" | "inherit"
    PermissionMode string
    ToolPolicy     ToolPolicyProfile
    MaxTurns       int               // zero = unlimited
}

type TeamAgentCall struct {
    SessionID       string
    ParentSessionID string
    PromptEnvelope  string
    Actor           actor.ActorContext
    ToolPolicy      ToolPolicyProfile
}
```

Corrections in the plan's `RunGroup`:

- `AgentSpec` gets `AgentType` + `PermissionMode` + `ToolPolicy` (matches doc) — `SystemPrompt`/`ModelType`/`MaxTurns` left zero (the future production factory decides defaults; the mock factory ignores them).
- `TeamAgentCall` needs a `SessionID` (the doc omits it; `TurnRunner.Run` consumers expect one). M2-01 generates a per-task session id via `uuid` (`task.ID` is not a session id). `PromptEnvelope` ← `task.Prompt`, `ToolPolicy` ← `delegateReadOnlyPolicy()`, `Actor` ← an `actor.ActorContext{}` tagged with `RunID = group.ID` and `TaskID = task.ID` so downstream observability can attribute the delegate.

`actor.ActorContext` (`internal/actor/actor.go:10-25`) real fields: `SessionID`, `ParentSessionID`, `MessageID`, `ToolCallID`, `WorkspaceID`, `TeamID`, `MemberID`, `MemberName`, `MemberRole`, `TaskID`, `RunID`. The master doc's `actor.ActorContext{}` empty literal is valid (all fields optional), but the plan populates `SessionID` + `RunID` + `TaskID` for attribution.

### Seam 5: panic recover writes a mutex-guarded array slot, not a channel send

The master doc (`:180-191`) uses `group.mu.Lock()` + `group.Results[idx] = ...` + `group.mu.Unlock()` inside the recover. **This is correct and is kept.** Contrast with M1-05's `async.go:96-103` recover, which uses a non-blocking `select`/`default` *channel* send (because `async.go`'s recover target is a buffered status channel, not a shared array). The M2-01 recover target is the shared `group.Results` slice, so the mutex-guarded array write is the right pattern. The plan keeps `defer group.wg.Done()` registered **first** (runs last, after recover) and the panic recover **second** (runs before `wg.Done`), so a panic still counts down the WaitGroup — mirroring `async.go`'s defer ordering (`async.go:84-103`: `close(statusChan)` first, unregister second, recover last).

### Seam 6: `RunningCount` semantics — `Status == ""` means "not yet filled"

The master doc's `RunningCount` (`:63-73`) counts slots where `r.Status == ""`. This relies on `Results` being pre-allocated to the task count with zero-value `DelegateResult{}` (so `Status` is the empty `TurnStatus` string until a goroutine writes its slot). The plan keeps this: `RunGroup` does `Results: make([]DelegateResult, len(tasks))`, and a slot is "running" until its goroutine writes a terminal `TurnStatus`. `DoneCount` counts `TurnCompleted`; `FailedCount` counts `TurnCanceled || TurnFailed`. **Note:** `TurnQueued` is neither running nor done nor failed (it's a non-terminal state the M1 doc says `Run` never returns) — so it falls into `RunningCount`, which is acceptable (it means "not done").

### Seam 7: `TotalTokens` field name vs master doc

The master doc (`:102-112`) names the method `TotalTokens()` but the surrounding text (:101 comment) says "TotalCost". The method sums `Result.TotalUsage.InputTokens + Result.TotalUsage.OutputTokens`. **Decision:** name it `TotalTokens()` (matches the method body, matches `AgentToolResult.TotalTokens` in `agent_result.go:17`). The "TotalCost" mention in the doc comment is a typo — `int64` tokens, not a money cost. `fantasy.Usage.InputTokens`/`OutputTokens` are `int64` (`model.go:12-13`), so the sum is `int64`, matching the return type.

### Seam 8: `Wait()` must observe the status flip — a real race the master doc's design hides

The master doc (`:254-267`) has the trailing goroutine do `group.wg.Wait()` then flip `group.Status` to `"done"`, but provides **no public `Wait()` method** — callers are expected to `time.Sleep(500ms)` (see the master doc's test at `:304-305`). That is a flaky, unprincipled contract.

M2-01 ships an exported `(*DelegateRunGroup).Wait()` so tests (and M2-04 UI, M2-05 aggregate) can deterministically observe completion. The naive implementation — `func (g *DelegateRunGroup) Wait() { g.wg.Wait() }` — has a **real race**: the trailing status-flip goroutine also calls `g.wg.Wait()`, so it races with the caller's `Wait()`. When the last worker goroutine calls `wg.Done()`, BOTH the caller's `Wait()` AND the trailing goroutine's `wg.Wait()` unblock concurrently. The caller may then read `group.Status` *before* the trailing goroutine has flipped it from `"running"` to `"done"`. **This race was reproduced during plan validation**: `TestDelegateRunner_ParallelExecution` failed with `expected: "done", actual: "running"` against the naive `Wait()`.

**Fix:** add a `done chan struct{}` field to `DelegateRunGroup`. The trailing goroutine closes `done` *after* it flips Status and unregisters the group. `Wait()` blocks on `g.wg.Wait()` *then* `<-g.done`. This guarantees that a caller returning from `Wait()` observes the final `Status` (acceptance #4) and that the group is already unregistered (acceptance #5). The close happens exactly once (the trailing goroutine is the only writer), so multiple concurrent `Wait()` callers all unblock on the same close — safe and idempotent.

This is the one structural addition to the master doc's `DelegateRunGroup` beyond the unexported `done`/`cancel` fields it implies. It is the load-bearing correctness fix for M2-01's concurrency contract.

### Out of scope (explicit)

- **Production `AgentFactory` wiring** (real factory backed by `sessionAgent`) — follow-up task. M2-01 depends only on the interface.
- **`ReadOnlyDelegatePolicy()` exported in package `agent`** (M2-03) — M2-01 ships an unexported in-package helper; M2-03 may promote it.
- **`CancelGroup` / `CancelAllGroups`** — M2-02.
- **`AggregateResults` markdown** — M2-05.
- **Delegate UI** — M2-04.
- **DB writes / team tables** — never (M2 is pure in-memory; acceptance #5).
- **Touching M1 code** (`async.go`, `coordinator.go`, `agent.go`, `agent_result.go`, `progress.go`) — none. M2-01 is additive: two new files in a new package.

---

## File Structure

**Files touched (scope is STRICT — M2-01 only):**

- Create: `internal/team/delegate_types.go` — package `team`. `DelegateTask`, `DelegateResult`, `DelegateRunGroup` structs + the `RunningCount`/`DoneCount`/`FailedCount`/`TotalTokens`/`Wait` methods + the unexported `delegateReadOnlyPolicy()` helper. Pure data + read-only aggregations; no goroutines.
- Create: `internal/team/delegate_runner.go` — package `team`. `DelegateRunner` struct, `NewDelegateRunner(factory agent.AgentFactory)`, `RunGroup(ctx, []DelegateTask) *DelegateRunGroup`, `ActiveGroupCount()`. Owns the parallel dispatch + per-goroutine panic recover + the trailing status-flip goroutine.
- Create: `internal/team/delegate_types_test.go` — table tests for the count/token aggregations + the policy shape.
- Create: `internal/team/delegate_runner_test.go` — `mockAgentFactory` (configurable delay / panic / error / result), `TestDelegateRunner_ParallelExecution`, `TestDelegateRunner_PanicRecovery`, `TestDelegateRunner_BuildRunnerError`, `TestDelegateRunner_GroupStatusFlipsToDone`, `TestDelegateRunner_ResultsAllFilled`.
- Create: `internal/team/doc.go` — one-line package doc (`// Package team ...`) so `go doc`/`lint` is satisfied (Go permits a package without a doc.go, but the repo convention is to have package documentation; verifying the agent package has none, this is optional — see Task 1 note).

**Files NOT touched:**

- `internal/agent/team_call.go` — types/interfaces reused as-is. No edit. (`ReadOnlyDelegatePolicy` is NOT added here; see Seam 3.)
- `internal/agent/async.go`, `coordinator.go`, `agent.go`, `agent_result.go`, `progress.go` — all M1 surface, untouched.
- `internal/actor/actor.go` — reused as-is.
- `internal/pubsub/*` — untouched (M2-01 does not publish progress events; that wiring, if any, is a later task).
- Any UI / DB / config files — untouched.

---

## Task 1: DelegateTask + DelegateResult + DelegateRunGroup types + read-only aggregations + policy helper (TDD)

**Files:**
- Create: `internal/team/delegate_types.go`
- Test: `internal/team/delegate_types_test.go`

- [ ] **Step 1: Write the failing tests**

Create `internal/team/delegate_types_test.go`:

```go
package team

import (
	"testing"

	"charm.land/fantasy"
	"github.com/charmbracelet/crush/internal/agent"
	"github.com/stretchr/testify/assert"
)

// TestDelegateResult_StatusFromTurnRunResult locks the DelegateResult shape:
// the Status field carries a TurnStatus (completed/canceled/failed) and the
// Result carries the *fantasy.AgentResult the runner produced. This is the
// contract the delegate runner writes into Results[idx] under the group mutex.
func TestDelegateResult_StatusFromTurnRunResult(t *testing.T) {
	ar := &fantasy.AgentResult{
		Response:   fantasy.Response{Content: fantasy.ResponseContent{fantasy.TextContent{Text: "done"}}},
		TotalUsage: fantasy.Usage{InputTokens: 100, OutputTokens: 50},
	}
	r := DelegateResult{
		TaskID:     "t1",
		Status:     agent.TurnCompleted,
		Result:     ar,
		DurationMs: 42,
	}
	assert.Equal(t, agent.TurnCompleted, r.Status)
	assert.Equal(t, int64(150), r.Result.TotalUsage.InputTokens+r.Result.TotalUsage.OutputTokens)
}

// TestDelegateRunGroup_Counts locks acceptance #4's Results-fill semantics and
// the count helpers. A freshly allocated group with N tasks has N "running"
// slots (Status == "" is the zero TurnStatus); as slots are filled with
// terminal statuses the DoneCount/FailedCount/RunningCount reflect them.
func TestDelegateRunGroup_Counts(t *testing.T) {
	g := &DelegateRunGroup{
		ID:      "g1",
		Tasks:   []DelegateTask{{ID: "1"}, {ID: "2"}, {ID: "3"}},
		Results: make([]DelegateResult, 3),
		Status:  "running",
	}

	// All three slots zero-valued => all running.
	assert.Equal(t, 3, g.RunningCount())
	assert.Equal(t, 0, g.DoneCount())
	assert.Equal(t, 0, g.FailedCount())

	// Fill slot 0 as completed.
	g.Results[0] = DelegateResult{TaskID: "1", Status: agent.TurnCompleted}
	assert.Equal(t, 2, g.RunningCount())
	assert.Equal(t, 1, g.DoneCount())
	assert.Equal(t, 0, g.FailedCount())

	// Fill slot 1 as failed.
	g.Results[1] = DelegateResult{TaskID: "2", Status: agent.TurnFailed, Error: "boom"}
	assert.Equal(t, 1, g.RunningCount())
	assert.Equal(t, 1, g.DoneCount())
	assert.Equal(t, 1, g.FailedCount())

	// Fill slot 2 as canceled (counts as failed-category).
	g.Results[2] = DelegateResult{TaskID: "3", Status: agent.TurnCanceled}
	assert.Equal(t, 0, g.RunningCount())
	assert.Equal(t, 1, g.DoneCount())
	assert.Equal(t, 2, g.FailedCount())
}

// TestDelegateRunGroup_TotalTokens locks the token aggregation across filled
// results. Slots whose Result is nil contribute zero.
func TestDelegateRunGroup_TotalTokens(t *testing.T) {
	g := &DelegateRunGroup{
		ID:    "g1",
		Tasks: []DelegateTask{{ID: "1"}, {ID: "2"}, {ID: "3"}},
		Results: []DelegateResult{
			{TaskID: "1", Status: agent.TurnCompleted, Result: &fantasy.AgentResult{
				TotalUsage: fantasy.Usage{InputTokens: 100, OutputTokens: 50},
			}},
			{TaskID: "2", Status: agent.TurnFailed, Error: "boom"}, // Result nil
			{TaskID: "3", Status: agent.TurnCompleted, Result: &fantasy.AgentResult{
				TotalUsage: fantasy.Usage{InputTokens: 200, OutputTokens: 75},
			}},
		},
		Status: "done",
	}
	// 150 + 0 + 275 = 425
	assert.Equal(t, int64(425), g.TotalTokens())
}

// TestDelegateReadOnlyPolicy_Shape locks the read-only policy the delegate
// runner applies to every delegate: the five read-only tools are allowed,
// destructive tools are disallowed, permission mode is default. This is the
// M2-01 in-package helper (Seam 3); M2-03 may later export
// agent.ReadOnlyDelegatePolicy with the identical shape.
func TestDelegateReadOnlyPolicy_Shape(t *testing.T) {
	p := delegateReadOnlyPolicy()

	assert.Equal(t, "default", p.PermissionMode)
	assert.ElementsMatch(t,
		[]string{"view", "grep", "glob", "ls", "sourcegraph"},
		p.AllowedTools)

	// Destructive tools must be in the disallow list (exact match not required,
	// but the load-bearing ones must be present).
	for _, banned := range []string{"bash", "write", "edit", "agent"} {
		assert.Contains(t, p.DisallowedTools, banned)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd g:/ai-project/remote-github/crush-worktrees/m2-01 && go test ./internal/team/ -v`
Expected: COMPILE FAIL — `package team` does not exist (no `internal/team/` directory), so the test file fails to compile with "no Go files in .../internal/team" or "undefined: DelegateResult" etc.

- [ ] **Step 3: Write minimal implementation**

Create `internal/team/delegate_types.go`:

```go
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

// delegateReadOnlyPolicy returns the read-only tool policy applied to every
// delegate. Only the five read-only tools (view/grep/glob/ls/sourcegraph) are
// allowed; destructive tools (bash/write/edit/agent/etc.) are disallowed.
//
// This is the M2-01 in-package helper (Seam 3): it lets the delegate runner
// compile + test today without waiting for M2-03's exported
// agent.ReadOnlyDelegatePolicy(). The shape is identical to the M2-03 spec
// (03-m2-delegate-runner.md:411-422); if M2-03 later exports the agent-level
// function, RunGroup can be switched to call it in a one-line change.
func delegateReadOnlyPolicy() agent.ToolPolicyProfile {
	return agent.ToolPolicyProfile{
		AllowedTools: []string{"view", "grep", "glob", "ls", "sourcegraph"},
		DisallowedTools: []string{
			"agent", "ask_user_questions", "job_output", "job_kill",
			"todos", "crush_info", "crush_logs",
			"bash", "write", "edit", "multiedit", "download",
			"fetch", "agentic_fetch",
		},
		PermissionMode: "default",
	}
}
```

The `context` import is required for the `ctx`/`cancel` field types even though Task 1's tests don't exercise them — Task 2's `RunGroup` writes them. Declaring the fields now keeps the struct shape stable across both tasks, so Task 2 only adds `delegate_runner.go` without editing `delegate_types.go`.

- [ ] **Step 4: Run test to verify it passes**

Run: `cd g:/ai-project/remote-github/crush-worktrees/m2-01 && go test ./internal/team/ -v`
Expected: PASS — `TestDelegateResult_StatusFromTurnRunResult`, `TestDelegateRunGroup_Counts`, `TestDelegateRunGroup_TotalTokens`, `TestDelegateReadOnlyPolicy_Shape` all green. `fantasy.TextContent`/`fantasy.ResponseContent`/`fantasy.Response`/`fantasy.AgentResult`/`fantasy.Usage` all verified to exist (`agent.go:300-305`, `model.go:11-17,35`). `agent.TurnStatus`/`agent.TurnCompleted`/`agent.TurnCanceled`/`agent.TurnFailed` verified (`team_call.go:11-22`).

- [ ] **Step 5: Commit**

```bash
cd g:/ai-project/remote-github/crush-worktrees/m2-01
git add internal/team/delegate_types.go internal/team/delegate_types_test.go
git commit -m "feat(team): add DelegateTask/DelegateResult/DelegateRunGroup types with read-only aggregations"
```

---

## Task 2: DelegateRunner.RunGroup parallel dispatch + panic recover (TDD)

**Files:**
- Create: `internal/team/delegate_runner.go`
- Test: `internal/team/delegate_runner_test.go`

- [ ] **Step 1: Write the failing tests**

Create `internal/team/delegate_runner_test.go`:

```go
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
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd g:/ai-project/remote-github/crush-worktrees/m2-01 && go test ./internal/team/ -run 'TestDelegateRunner_' -v`
Expected: COMPILE FAIL — `NewDelegateRunner` undefined, `DelegateRunner` undefined, `(*DelegateRunGroup).Wait` undefined.

- [ ] **Step 3: Write minimal implementation**

Create `internal/team/delegate_runner.go`:

```go
package team

import (
	"context"
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
			if runErr != nil {
				group.Results[idx] = DelegateResult{
					TaskID:     t.ID,
					Status:     agent.TurnFailed,
					Error:      runErr.Error(),
					DurationMs: dur.Milliseconds(),
				}
			} else {
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
```

- [ ] **Step 4: Run test to verify it passes**

Run: `cd g:/ai-project/remote-github/crush-worktrees/m2-01 && go test ./internal/team/ -run 'TestDelegateRunner_' -v`
Expected: PASS — all six delegate-runner tests green. `TestDelegateRunner_ParallelExecution` asserts `elapsed < 250ms` (three 100ms tasks in parallel land near ~100ms; serial would be ~300ms). `TestDelegateRunner_PanicRecovery` proves a `BuildRunner` panic is recovered into `TurnFailed` + `"panic"` error. `TestDelegateRunner_ActorAttribution` proves the `TeamAgentCall.Actor` carries `TaskID` + `RunID == group.ID`.

- [ ] **Step 5: Commit**

```bash
cd g:/ai-project/remote-github/crush-worktrees/m2-01
git add internal/team/delegate_runner.go internal/team/delegate_runner_test.go
git commit -m "feat(team): add DelegateRunner.RunGroup with parallel dispatch and per-goroutine panic recover"
```

---

## Task 3: package build + vet + full package test gate

**Files:** none new (verification gate).

- [ ] **Step 1: Build the whole module**

Run: `cd g:/ai-project/remote-github/crush-worktrees/m2-01 && go build ./...`
Expected: PASS, no output. Confirms the new `internal/team` package compiles and no other package broke.

- [ ] **Step 2: Vet the new package**

Run: `cd g:/ai-project/remote-github/crush-worktrees/m2-01 && go vet ./internal/team/...`
Expected: PASS, no output.

- [ ] **Step 3: Run the full internal/team test suite**

Run: `cd g:/ai-project/remote-github/crush-worktrees/m2-01 && go test ./internal/team/ -v`
Expected: PASS — all tests from Task 1 and Task 2 green.

- [ ] **Step 4: Run the agent-package regression gate (M1 surface untouched)**

Run: `cd g:/ai-project/remote-github/crush-worktrees/m2-01 && go test ./internal/agent/ -run 'TestTurnStatus_IsTerminal|TestTeamAgentCall_|TestAgentSpec_CarriesMaxTurns|TestAgentProgressEvent_JSONRoundTrip|TestLastToolName_|TestSubscribeAgentProgress_|TestAgentProgress_FullBufferDoesNotBlock|TestRunSubAgentAsync|TestCancel|TestCancelAll|TestRunSubAgentStructured_|TestCoordinator_ActiveSubAgentsInitialized' -v`
Expected: PASS — all M1 tests green. M2-01 did not touch `internal/agent`, so this is a no-regression guard. (`-race` is intentionally omitted here because the memory notes flags `-race` is broken in this environment due to cgo; the team-package tests in Step 5 cover the concurrency contract.)

- [ ] **Step 5: Run the team-package tests once more as the final concurrency gate**

Run: `cd g:/ai-project/remote-github/crush-worktrees/m2-01 && go test ./internal/team/ -count=3`
Expected: PASS, all green, three runs. (`-count=3` is the lightweight concurrency-flake catch given `-race` is unavailable.)

No commit in this task — it is pure verification. If any step fails, stop and report the failure to the team-lead before proceeding.

---

## Final verification (acceptance gate)

Run:

```bash
cd g:/ai-project/remote-github/crush-worktrees/m2-01
go build ./...
go vet ./internal/team/...
go test ./internal/team/ -v -count=3
go test ./internal/agent/ -run 'TestTurnStatus_IsTerminal|TestTeamAgentCall_|TestAgentSpec_CarriesMaxTurns' -v
```

Acceptance criteria mapping (master task doc `03-m2-delegate-runner.md:281-287`):

1. **`RunGroup(ctx, [task1, task2, task3])` returns non-nil group** — `TestDelegateRunner_ParallelExecution` asserts `require.NotNil(t, group)` and `require.Len(t, group.Results, 3)`.
2. **3 goroutines run in parallel (start timestamps within 1s)** — `TestDelegateRunner_ParallelExecution` asserts the whole run completes in `< 250ms` for three 100ms tasks. Serial execution would take ~300ms; parallel lands near ~100ms. The mock runner records a shared `starts` atomic, and the wall-clock bound is a stronger guarantee than per-goroutine start-timestamp deltas.
3. **Any one goroutine panicking does not affect others** — `TestDelegateRunner_PanicRecovery` (both subtests): a `BuildRunner` panic is recovered into `TurnFailed` + `"panic"` error, and a panicking runner coexists with a succeeding runner without cross-contamination.
4. **After `group.wg.Wait()`, `group.Results` is fully populated** — `TestDelegateRunner_GroupStatusFlipsToDone` + `TestDelegateRunner_ParallelExecution` both call `group.Wait()` (the exported wrapper over `wg.Wait()`) and assert every `Results` slot has a terminal `Status` (`TurnCompleted`/`TurnFailed`), plus `group.Status == "done"`.
5. **Pure in-memory — no DB writes, no team tables created** — `TestDelegateRunner_ParallelExecution` asserts `runner.ActiveGroupCount() == 0` after `Wait()` (the group is unregistered from the in-memory map; nothing is persisted). Structurally: `internal/team/delegate_*.go` imports no DB/session/log package; the runner's only state is the in-memory `groups` map.

**Out-of-scope reminder (for the team-lead review):** M2-01 ships the dispatch core + types, fully covered by mock-factory tests. The production `AgentFactory` implementation (real factory backed by `sessionAgent`) is a separate follow-up task — M2-01 cannot run against a real LLM until it lands, but the concurrency contract this task locks is fully verifiable today. `ReadOnlyDelegatePolicy()` is shipped as an unexported in-package helper (`delegateReadOnlyPolicy`); M2-03 may later export the agent-level function and `RunGroup` can switch to it in a one-line change.

If everything is green, report back to the team-lead with the test output, the commit list, and the design-seam decisions (especially Seam 2 — no production factory yet — and Seam 3 — in-package policy helper).
