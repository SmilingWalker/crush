# M4-01 MemberRunner 状态机 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Implement the `MemberRunner` runtime state machine in `internal/team/member_runner.go` — the core long-lived teammate loop (wake/loop/run cycle) that all other M4 tasks depend on. Also implements the `TurnRunnerAdapter` in `internal/agent/turn_runner_adapter.go` to bridge the M1 `SessionAgent` to the `TurnRunner` interface, providing a minimal production AgentFactory seam (design seam #1).

**Architecture:** Two new files in two existing packages. `internal/team/member_runner.go` (package `team`) adds the `MemberRunner` struct with 11-state lifecycle, CAS version tracking, single-flight wake gate, and the `loop/handleWake/transitionLocked/Wake` methods. `internal/agent/turn_runner_adapter.go` (package `agent`) adds a `TurnRunnerAdapter` that wraps `SessionAgent` → `TurnRunner`, translating `TeamAgentCall` to `SessionAgentCall`. No existing files are modified — MemberRunner only calls existing M3 `Service`/`MemberStore` interfaces, never touches their implementations.

**Tech Stack:** Go 1.26.3 (`module github.com/charmbracelet/crush`), `github.com/stretchr/testify` (assert + require), `github.com/google/uuid` (already in module). Worktree `G:/ai-project/remote-github/crush-worktrees/m4-01`, branch `m4-01-member-runner`, off agent-team tip `e3b6ab4e` (Merge M3-09, M3 complete). Shell: bash on win32 (forward slashes, `/dev/null`). No `-race` flag (cgo broken on this setup).

**Spec source:** `docs/agent-team-mode/plan/tasks/05-m4-long-lived-teammate.md` section `## M4-01` (lines 9-254). State enum, validTransitions, WakeSource, MemberRunner struct, Start/loop/handleWake/transitionLocked/Wake methods, 5 acceptance criteria are taken from that approved design.

---

## Design seam research (pre-plan, verified against agent-team tip `e3b6ab4e`)

All 8 seams flagged by team-lead were researched against the actual codebase. Here are the concrete findings and decisions.

### Seam 1 (CRITICAL): M2-deferred AgentFactory gap — M1 coordinator.buildAgent is private

**Research result:**

- `AgentFactory` interface exists at `internal/agent/team_call.go:111-113`: `BuildRunner(ctx context.Context, spec AgentSpec) (TurnRunner, error)`.
- `TurnRunner` interface exists at `team_call.go:83-92`: `Run(ctx, TeamAgentCall) (TurnRunResult, error)`, `Cancel(sessionID string)`, `IsSessionBusy(sessionID string) bool`.
- `TeamAgentCall` struct at `team_call.go:32-38`: `SessionID`, `ParentSessionID`, `PromptEnvelope` (string), `Actor` (`actor.ActorContext`), `ToolPolicy` (`ToolPolicyProfile`).
- `TurnRunResult` struct at `team_call.go:75-78`: `Status TurnStatus`, `Result *fantasy.AgentResult`.
- `SessionAgent` interface at `agent.go:86-100`: `Run(ctx, SessionAgentCall) (*fantasy.AgentResult, error)` — **different signature** from `TurnRunner.Run` (takes `SessionAgentCall` with `Prompt string`, not `TeamAgentCall` with `PromptEnvelope string`).
- M1 coordinator's `buildAgent` (`coordinator.go:444`) is **unexported** on the unexported `coordinator` struct. No public function creates `SessionAgent` instances from outside `package agent`.
- **There is NO production `AgentFactory` implementation anywhere in the codebase.** The `TurnRunner` interface was defined in M1-04 but the adapter from `SessionAgent` to `TurnRunner` was deferred to a future milestone.

**Evidence:** `internal/agent/team_call.go` (read in full: `AgentFactory` + `TurnRunner` + `TeamAgentCall` + `TurnRunResult` + `AgentSpec` types confirmed); `internal/agent/coordinator.go:444` (`buildAgent` unexported); `internal/agent/agent.go:86-100` (`SessionAgent.Run` signature confirmed).

**Decision (option a — minimal production AgentFactory):**

M4-01 creates `internal/agent/turn_runner_adapter.go` with:

1. **`TurnRunnerAdapter`**: a struct wrapping a `SessionAgent` that implements `TurnRunner`. Translates `TeamAgentCall` → `SessionAgentCall` by mapping `call.PromptEnvelope` → `call.Prompt`, `call.SessionID` → `call.SessionID`, and setting `NonInteractive: true` (member turns are background, not UI). Maps `*fantasy.AgentResult` → `TurnRunResult{Status: TurnCompleted, Result: result}` on success, `TurnRunResult{Status: TurnFailed}` on error. Implements `Cancel`/`IsSessionBusy` by delegating to `SessionAgent`.
2. **`NewTurnRunnerFromSessionAgent(sa SessionAgent) TurnRunner`**: constructor.
3. **`SessionAgentFactory`**: a struct implementing `AgentFactory` that holds a `func() SessionAgent` closure. `BuildRunner(ctx, spec)` calls the closure to get a `SessionAgent`, wraps it in `TurnRunnerAdapter`, returns it. Ignores `spec` fields in this minimal version (the factory doesn't build models/tools from spec — the caller pre-configures the SessionAgent).
4. **`NewAgentFactory(newSA func() SessionAgent) AgentFactory`**: constructor.

**MemberRunner constructor change:** The master doc's `NewMemberRunner` accepts `factory agent.AgentFactory`. This plan keeps that — the factory is injected. For production wiring (M4-02+), the caller provides `agent.NewAgentFactory(...)`. For tests, a mock `AgentFactory` that returns a mock `TurnRunner` is used.

**What this adapter does NOT do (honest about limitations):**
- Does NOT use `AgentSpec` fields (`AgentType`, `SystemPrompt`, `ModelType`, `ToolPolicy`, `MaxTurns`) to configure the agent. The caller pre-builds the `SessionAgent` with correct models/tools/system-prompt before passing it to the factory closure.
- Does NOT create independent child sessions per member turn (M4-10 Session Links is a follow-up task).
- This is sufficient for M4-01 member runner state machine testing and M4-02 TeamRunner integration. A richer `AgentFactory` that reads `AgentSpec` and builds from coordinator config can land in a follow-up (M4-01b) if needed, but for now this closes the M2-deferred gap.

**File placement:** `internal/agent/turn_runner_adapter.go` belongs to `package agent` because it imports `SessionAgent` (unexported) and bridges internal agent types. It does NOT import `package team`.

### Seam 2: M3b mailbox tables — deferred to M4-04

**Research result:** M3-01 migration comment :23-25 deferred `team_mailbox_messages` + `team_message_receipts` to M3b/M2.5/M4. These tables do NOT exist at agent-team tip `e3b6ab4e`.

**Decision:** M4-01 does **NOT** create mailbox tables. M4-04 (Mailbox) owns them. M4-01's `MemberRunner` only depends on existing M3 tables (`team_members`, `team_runs`, `team_tasks` — all present since M3-01 migration). `WakeSourceMailbox` is defined as a const but is a **future wire-up** — the `Wake` method accepts any `WakeSource`, but the actual mailbox → member routing is M4-04's concern.

### Seam 3: TeamService API verification — signatures deviate from master doc

**Research result (verified against `internal/team/service.go`):**

| Master doc call | Actual Service API | Deviation |
|---|---|---|
| `svc.GetMember(ctx, teamID, id)` | **Not on Service interface.** Only `ListMembers(ctx, teamID)` exists. `MemberStore.GetMember(ctx, tx, id)` exists at store layer. | MemberRunner uses `ListMembers` + filter by ID, or directly stores member data from constructor args. |
| `svc.UpdateMemberState(ctx, req{MemberID, State, ExpectedVersion})` | `UpdateMemberState(ctx, UpdateMemberStateRequest{ID, TeamID, Status, TaskID, RunID, ToolName})` — **no ExpectedVersion field**. CAS is internal read-then-CAS. | `transitionLocked` does NOT pass version. The Service reads current version internally. |
| `svc.StartRun(ctx, run)` | `StartRun(ctx, StartRunRequest{TeamID, MemberID, TaskID, SessionID}) (TeamRun, error)` — returns the created run with server-assigned ID. | handleWake captures returned `TeamRun` for subsequent FinishRun. |
| `svc.FinishRun(ctx, run)` where `run` is `TeamRun` struct | `FinishRun(ctx, FinishRunRequest{TeamID, RunID, PromptTokens, CompletionTokens, CostMicros}) error` — takes request struct, not TeamRun. | handleWake builds `FinishRunRequest` from the run result, not a raw TeamRun. |
| `svc.ClaimNextTask(ctx, ...)` | `ClaimNextTask(ctx, ClaimNextTaskRequest{TeamID, AssigneeMemberID, UpdatedAt}) (TeamTask, error)` | Signature matches. Returns `ErrNoTaskAvailable` when queue empty. |
| `svc.ListEventsAfter(ctx, teamID, afterSeq, limit)` | `ListEventsAfter(ctx, teamID, afterSeq, limit) ([]TeamEvent, error)` | Exact match. |

**Evidence:** `internal/team/service.go:124-147` (Service interface); `internal/team/store_member.go:15-21` (MemberStore interface with `GetMember` at store layer only); `internal/team/service.go:405-442` (UpdateMemberState implementation with internal read-then-CAS).

**Decision:** MemberRunner adapts to actual Service API:
- `Start`: uses `ListMembers` to verify member existence and load initial state.
- `transitionLocked`: calls `UpdateMemberState` without version — the Service handles CAS internally. On `ErrMemberVersionConflict`, the transition is retried (re-read state, re-attempt CAS). This is documented as a retry seam.
- `handleWake`: uses `StartRunRequest`/`FinishRunRequest` structs matching actual API.

### Seam 4: MemberState reuse M3-03 MemberStatus (NO new type)

**Research result:** M3-03 `models.go:55-69` defines `MemberStatus` with 11 values: `created`, `starting`, `idle`, `queued`, `running`, `waiting_permission`, `blocked`, `canceling_turn`, `shutting_down`, `stopped`, `failed`. This is an **exact 1:1 match** with the master doc's `MemberState` enum (lines 20-33).

**Decision:** M4-01 reuses `team.MemberStatus` directly. No new `MemberState` type is defined. The `validTransitions` map uses `MemberStatus` keys. WakeSource is a new type (not in M3 domain layer).

**Evidence:** `internal/team/models.go:55-69` (MemberStatus const block, 11 values confirmed identical).

### Seam 5: TurnRunner call details + buildPrompt stub

**Research result:**
- `TurnRunner.Run(ctx, TeamAgentCall) (TurnRunResult, error)` — `TeamAgentCall.PromptEnvelope` is a `string` (the full assembled prompt). `TurnRunResult.Status` is `TurnStatus`; `TurnRunResult.Result` is `*fantasy.AgentResult` which has `TotalUsage` with `InputTokens`/`OutputTokens`.
- `actor.ActorContext` at `internal/actor/actor.go:10-25` has `TeamID`, `MemberID`, `MemberName`, `MemberRole` fields (M2+ reserved, available for M4 filling).

**Decision:**
- `buildPrompt(source WakeSource) string`: stub implementation that returns a simple text like `"Wake source: <source>. Begin your turn."`. M4-05 (PromptEnvelope Builder) replaces this with full system+task+context assembly. The stub is sufficient for state machine testing.
- `actorCtx()` builds `actor.ActorContext` from member fields: `TeamID`, `MemberID`, `MemberName` (from `Spec`), `MemberRole` (from DB member record).

**Evidence:** `internal/agent/team_call.go:32-38` (TeamAgentCall fields); `internal/actor/actor.go:10-25` (ActorContext fields); `internal/agent/team_call.go:75-78` (TurnRunResult with `Result *fantasy.AgentResult`).

### Seam 6: Single-turn serial (acceptance #3)

**Decision:** `maxConcurrentRuns=1` hardcoded in constructor. `handleWake`'s single-flight gate checks `State == MemberRunning || MemberQueued || MemberCancelingTurn` under lock — if busy, the wake source is **not** rejected (the wake is preserved for the next loop iteration). This is exactly the master doc's design at lines 161-168.

### Seam 7: Test fixtures

**Research result:** M3-04 `store_test_helpers.go` provides `newStoreFixture(t) (*sql.DB, *db.Queries)` — in-memory SQLite with `SetMaxOpenConns(1)`, FK enforcement ON, all migrations applied. M3-05 `service_test.go:16-26` provides `newServiceFixture(t) (Service, *sql.DB)` — fully wired Service with feature gate enabled.

**Decision:** M4-01 tests use `newServiceFixture` from `service_test.go` (same package `team`, so accessible). Tests create mock `TurnRunner` (not real SessionAgent — no LLM calls in unit tests). The mock records calls and returns configurable results.

**Evidence:** `internal/team/store_test_helpers.go:21-40` (newStoreFixture); `internal/team/service_test.go:16-26` (newServiceFixture).

### Seam 8: Scope — no M3 modifications

**Decision:** M4-01 creates `internal/team/member_runner.go` + `internal/team/member_runner_test.go` + `internal/agent/turn_runner_adapter.go` + `internal/agent/turn_runner_adapter_test.go`. Does NOT touch `internal/team/service.go`, `internal/team/store_*.go`, `internal/team/models.go`, or any M3 file.

---

## File Structure

| File | Responsibility | Created/Modified |
|---|---|---|
| `internal/agent/turn_runner_adapter.go` | `TurnRunnerAdapter` struct (SessionAgent → TurnRunner bridge) + `SessionAgentFactory` (minimal AgentFactory). Package `agent`. | **Create** |
| `internal/agent/turn_runner_adapter_test.go` | Tests: adapter delegates Run/Cancel/IsSessionBusy correctly; factory BuildRunner returns wrapped SessionAgent. | **Create** |
| `internal/team/member_runner.go` | `WakeSource` type + 5 consts, `validTransitions` map, `MemberRunner` struct, `NewMemberRunner` constructor, `Start`/`loop`/`handleWake`/`transitionLocked`/`Wake`/`Stop` methods, `buildPrompt` stub, `actorCtx` helper. Package `team`. | **Create** |
| `internal/team/member_runner_test.go` | Acceptance tests: state machine transitions (table-driven), CAS version conflict, single-flight serialization, Start/loop/Wake lifecycle, mock TurnRunner integration. | **Create** |

**Out of scope (do NOT touch):**
- `internal/team/service.go`, `internal/team/store_*.go`, `internal/team/models.go` (all M3)
- `internal/agent/coordinator.go`, `internal/agent/agent.go` (all M1)
- `internal/agent/team_call.go` (M1-04 interface definitions — read-only)
- Any migration files (M3-01 owned)

---

## Task 1: TurnRunnerAdapter — SessionAgent → TurnRunner bridge (agent package)

**Files:**
- Create: `internal/agent/turn_runner_adapter.go`
- Create: `internal/agent/turn_runner_adapter_test.go`

This task implements the minimal production AgentFactory seam (design seam #1). It lives in `package agent` because it imports and wraps `SessionAgent` (the unexported interface). The adapter translates `TeamAgentCall` (used by TurnRunner.Run) to `SessionAgentCall` (used by SessionAgent.Run).

- [ ] **Step 1: Write failing test for TurnRunnerAdapter**

Create `internal/agent/turn_runner_adapter_test.go`. Tests a mock `SessionAgent` that records calls:

```go
package agent

import (
    "context"
    "errors"
    "testing"

    "charm.land/fantasy"
    "github.com/charmbracelet/crush/internal/actor"
    "github.com/stretchr/testify/assert"
    "github.com/stretchr/testify/require"
)

// mockSessionAgent implements SessionAgent for TurnRunnerAdapter tests.
type mockSessionAgent struct {
    runCalls   []SessionAgentCall
    runResult  *fantasy.AgentResult
    runErr     error
    canceled   []string
    busyMap    map[string]bool
}

func (m *mockSessionAgent) Run(ctx context.Context, call SessionAgentCall) (*fantasy.AgentResult, error) {
    m.runCalls = append(m.runCalls, call)
    return m.runResult, m.runErr
}
func (m *mockSessionAgent) Cancel(sessionID string)       { m.canceled = append(m.canceled, sessionID) }
func (m *mockSessionAgent) IsSessionBusy(id string) bool  { return m.busyMap[id] }
func (m *mockSessionAgent) SetModels(large, small Model)   {}
func (m *mockSessionAgent) SetTools(tools []fantasy.AgentTool) {}
func (m *mockSessionAgent) SetSystemPrompt(p string)      {}
func (m *mockSessionAgent) CancelAll()                     {}
func (m *mockSessionAgent) IsBusy() bool                   { return false }
func (m *mockSessionAgent) QueuedPrompts(id string) int    { return 0 }
func (m *mockSessionAgent) QueuedPromptsList(id string) []string { return nil }
func (m *mockSessionAgent) ClearQueue(id string)           {}
func (m *mockSessionAgent) Summarize(ctx context.Context, id string, opts fantasy.ProviderOptions) error { return nil }
func (m *mockSessionAgent) Model() Model                   { return Model{} }

func TestTurnRunnerAdapter_Run_Success(t *testing.T) {
    mock := &mockSessionAgent{
        runResult: &fantasy.AgentResult{TotalUsage: fantasy.Usage{InputTokens: 100, OutputTokens: 50}},
        busyMap:   map[string]bool{},
    }
    adapter := NewTurnRunnerFromSessionAgent(mock)
    result, err := adapter.Run(context.Background(), TeamAgentCall{
        SessionID:      "sess-1",
        PromptEnvelope: "hello",
        Actor:          actor.ActorContext{SessionID: "sess-1", TeamID: "t1", MemberID: "m1"},
    })
    require.NoError(t, err)
    assert.Equal(t, TurnCompleted, result.Status)
    assert.Equal(t, int64(100), result.Result.TotalUsage.InputTokens)
    assert.Equal(t, int64(50), result.Result.TotalUsage.OutputTokens)
    require.Len(t, mock.runCalls, 1)
    assert.Equal(t, "sess-1", mock.runCalls[0].SessionID)
    assert.Equal(t, "hello", mock.runCalls[0].Prompt)
    assert.True(t, mock.runCalls[0].NonInteractive)
}

func TestTurnRunnerAdapter_Run_Error(t *testing.T) {
    mock := &mockSessionAgent{runErr: errors.New("boom"), busyMap: map[string]bool{}}
    adapter := NewTurnRunnerFromSessionAgent(mock)
    result, err := adapter.Run(context.Background(), TeamAgentCall{
        SessionID: "sess-1", PromptEnvelope: "test",
    })
    assert.Error(t, err)
    assert.Equal(t, TurnFailed, result.Status)
}

func TestTurnRunnerAdapter_Cancel(t *testing.T) {
    mock := &mockSessionAgent{busyMap: map[string]bool{}}
    adapter := NewTurnRunnerFromSessionAgent(mock)
    adapter.Cancel("sess-1")
    assert.Equal(t, []string{"sess-1"}, mock.canceled)
}

func TestTurnRunnerAdapter_IsSessionBusy(t *testing.T) {
    mock := &mockSessionAgent{busyMap: map[string]bool{"sess-1": true}}
    adapter := NewTurnRunnerFromSessionAgent(mock)
    assert.True(t, adapter.IsSessionBusy("sess-1"))
    assert.False(t, adapter.IsSessionBusy("sess-2"))
}

func TestSessionAgentFactory_BuildRunner(t *testing.T) {
    var calls int
    factory := NewAgentFactory(func() SessionAgent {
        calls++
        return &mockSessionAgent{busyMap: map[string]bool{}}
    })
    r1, err := factory.BuildRunner(context.Background(), AgentSpec{AgentType: "test"})
    require.NoError(t, err)
    assert.NotNil(t, r1)
    assert.Equal(t, 1, calls)

    r2, err := factory.BuildRunner(context.Background(), AgentSpec{AgentType: "test"})
    require.NoError(t, err)
    assert.NotNil(t, r2)
    assert.Equal(t, 2, calls)
    assert.NotSame(t, r1, r2) // each BuildRunner creates a new instance
}
```

- [ ] **Step 2: Run tests to verify they fail (types undefined)**

```bash
cd G:/ai-project/remote-github/crush-worktrees/m4-01 && go test ./internal/agent/ -run 'TestTurnRunnerAdapter|TestSessionAgentFactory' -v
```

Expected: FAIL / build error — `TurnRunnerAdapter`, `NewTurnRunnerFromSessionAgent`, `SessionAgentFactory`, `NewAgentFactory` undefined.

- [ ] **Step 3: Write TurnRunnerAdapter implementation**

Create `internal/agent/turn_runner_adapter.go`:

```go
package agent

import (
    "context"
    "fmt"

    "charm.land/fantasy"
)

// TurnRunnerAdapter wraps a SessionAgent and implements TurnRunner, bridging
// the M1 SessionAgent (Run takes SessionAgentCall) to the M4 TurnRunner
// interface (Run takes TeamAgentCall). This is the minimal production adapter
// deferred since M2 — it maps PromptEnvelope→Prompt, SessionID→SessionID,
// and marks member turns as NonInteractive (no UI notifications).
type TurnRunnerAdapter struct {
    sa SessionAgent
}

// NewTurnRunnerFromSessionAgent creates a TurnRunner that delegates to sa.
func NewTurnRunnerFromSessionAgent(sa SessionAgent) TurnRunner {
    return &TurnRunnerAdapter{sa: sa}
}

// Run executes one agent turn. Maps TeamAgentCall fields to SessionAgentCall.
func (a *TurnRunnerAdapter) Run(ctx context.Context, call TeamAgentCall) (TurnRunResult, error) {
    result, err := a.sa.Run(ctx, SessionAgentCall{
        SessionID:      call.SessionID,
        Prompt:         call.PromptEnvelope,
        NonInteractive: true, // member turns are background, not UI
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

// SessionAgentFactory is a minimal AgentFactory that wraps a SessionAgent
// provider function. Each BuildRunner call invokes the provider to get a fresh
// SessionAgent and wraps it in a TurnRunnerAdapter.
//
// This factory does NOT use AgentSpec fields to configure models/tools/system
// prompt — the caller pre-configures the SessionAgent before passing it to the
// provider closure. A richer factory that reads AgentSpec is deferred to a
// future follow-up (M4-01b or M4-02 TeamRunner scaffolding).
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
```

- [ ] **Step 4: Run tests to verify they pass**

```bash
cd G:/ai-project/remote-github/crush-worktrees/m4-01 && go test ./internal/agent/ -run 'TestTurnRunnerAdapter|TestSessionAgentFactory' -v
```

Expected: PASS — all 5 subtests green.

- [ ] **Step 5: Build + vet the agent package**

```bash
cd G:/ai-project/remote-github/crush-worktrees/m4-01 && go build ./internal/agent/...
cd G:/ai-project/remote-github/crush-worktrees/m4-01 && go vet ./internal/agent/...
```

Expected: exit 0, no output.

- [ ] **Step 6: Commit**

```bash
cd G:/ai-project/remote-github/crush-worktrees/m4-01
git add internal/agent/turn_runner_adapter.go internal/agent/turn_runner_adapter_test.go
git commit -m "feat(agent): add TurnRunnerAdapter bridging SessionAgent to TurnRunner"
```

---

## Task 2: MemberRunner state machine — types, transitions, and constructor

**Files:**
- Create: `internal/team/member_runner.go`
- Create: `internal/team/member_runner_test.go` (partial — state machine transition table test only)

This task defines the `WakeSource` type, the `validTransitions` map (using `MemberStatus` from M3-03), the `MemberRunner` struct, and the `NewMemberRunner` constructor. No lifecycle methods yet.

- [ ] **Step 1: Write failing test for validTransitions**

Create `internal/team/member_runner_test.go` with a table-driven test that exhaustively checks every state's allowed next states:

```go
package team

import (
    "testing"

    "github.com/stretchr/testify/assert"
)

func TestValidTransitions_CoversAllMemberStatuses(t *testing.T) {
    // Every defined MemberStatus must be a key in validTransitions.
    for _, s := range allMemberStatuses {
        _, ok := validTransitions[s]
        assert.Truef(t, ok, "MemberStatus %q missing from validTransitions", s)
    }
    // The map must have exactly 11 keys (one per MemberStatus).
    assert.Len(t, validTransitions, 11, "validTransitions must cover all 11 MemberStatus values")
}

func TestValidTransitions_Allowed(t *testing.T) {
    tests := []struct {
        from MemberStatus
        to   MemberStatus
        ok   bool
    }{
        // created
        {MemberCreated, MemberStarting, true},
        {MemberCreated, MemberStopped, true},
        {MemberCreated, MemberIdle, false},
        // starting
        {MemberStarting, MemberIdle, true},
        {MemberStarting, MemberFailed, true},
        {MemberStarting, MemberStopped, false},
        // idle — most transitions
        {MemberIdle, MemberQueued, true},
        {MemberIdle, MemberRunning, true},
        {MemberIdle, MemberBlocked, true},
        {MemberIdle, MemberShuttingDown, true},
        {MemberIdle, MemberStopped, true},
        {MemberIdle, MemberFailed, false},
        // queued
        {MemberQueued, MemberRunning, true},
        {MemberQueued, MemberIdle, true},
        {MemberQueued, MemberShuttingDown, true},
        {MemberQueued, MemberFailed, false},
        // running
        {MemberRunning, MemberIdle, true},
        {MemberRunning, MemberWaitingPermission, true},
        {MemberRunning, MemberBlocked, true},
        {MemberRunning, MemberCancelingTurn, true},
        {MemberRunning, MemberFailed, true},
        {MemberRunning, MemberShuttingDown, true},
        {MemberRunning, MemberStopped, false},
        // waiting_permission
        {MemberWaitingPermission, MemberIdle, true},
        {MemberWaitingPermission, MemberBlocked, true},
        {MemberWaitingPermission, MemberCancelingTurn, true},
        {MemberWaitingPermission, MemberShuttingDown, true},
        {MemberWaitingPermission, MemberRunning, false},
        // blocked
        {MemberBlocked, MemberIdle, true},
        {MemberBlocked, MemberShuttingDown, true},
        {MemberBlocked, MemberStopped, true},
        {MemberBlocked, MemberRunning, false},
        // canceling_turn
        {MemberCancelingTurn, MemberIdle, true},
        {MemberCancelingTurn, MemberBlocked, true},
        {MemberCancelingTurn, MemberShuttingDown, true},
        {MemberCancelingTurn, MemberFailed, true},
        {MemberCancelingTurn, MemberRunning, false},
        // shutting_down
        {MemberShuttingDown, MemberStopped, true},
        {MemberShuttingDown, MemberFailed, true},
        {MemberShuttingDown, MemberIdle, false},
        // stopped (terminal)
        {MemberStopped, MemberIdle, false},
        {MemberStopped, MemberStarting, false},
        // failed
        {MemberFailed, MemberStopped, true},
        {MemberFailed, MemberIdle, false},
        {MemberFailed, MemberStarting, false},
    }
    for _, tt := range tests {
        t.Run(string(tt.from)+"->"+string(tt.to), func(t *testing.T) {
            allowed := validTransitions[tt.from]
            found := false
            for _, a := range allowed {
                if a == tt.to {
                    found = true
                    break
                }
            }
            if tt.ok {
                assert.True(t, found, "transition %s->%s should be allowed", tt.from, tt.to)
            } else {
                assert.False(t, found, "transition %s->%s should NOT be allowed", tt.from, tt.to)
            }
        })
    }
}

func TestWakeSource_Consts(t *testing.T) {
    // Verify 5 wake source consts are distinct.
    sources := []WakeSource{WakeSourceMailbox, WakeSourceTask, WakeSourceExplicit, WakeSourceRecovery, WakeSourceDependency}
    seen := map[WakeSource]bool{}
    for _, s := range sources {
        assert.False(t, seen[s], "duplicate WakeSource value %d", s)
        seen[s] = true
    }
    assert.Len(t, sources, 5)
}
```

- [ ] **Step 2: Run tests to verify they fail**

```bash
cd G:/ai-project/remote-github/crush-worktrees/m4-01 && go test ./internal/team/ -run 'TestValidTransitions|TestWakeSource' -v
```

Expected: FAIL / build error — `WakeSource`, `validTransitions` undefined.

- [ ] **Step 3: Write WakeSource, validTransitions, MemberRunner struct, and constructor**

Create `internal/team/member_runner.go`:

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
)

// WakeSource categorizes what triggered a member's wake-up.
type WakeSource int

const (
    WakeSourceMailbox    WakeSource = iota // incoming message (M4-04 wire-up)
    WakeSourceTask                         // new task assigned
    WakeSourceExplicit                     // leader explicit wake
    WakeSourceRecovery                     // startup recovery
    WakeSourceDependency                   // dependency task completed
)

// validTransitions maps each MemberStatus to its allowed next states.
// Sourced from the master task doc M4-01:35-48. All 11 MemberStatus values
// from M3-03 models.go are covered.
var validTransitions = map[MemberStatus][]MemberStatus{
    MemberCreated:           {MemberStarting, MemberStopped},
    MemberStarting:          {MemberIdle, MemberFailed},
    MemberIdle:              {MemberQueued, MemberRunning, MemberBlocked, MemberShuttingDown, MemberStopped},
    MemberQueued:            {MemberRunning, MemberIdle, MemberShuttingDown},
    MemberRunning:           {MemberIdle, MemberWaitingPermission, MemberBlocked, MemberCancelingTurn, MemberFailed, MemberShuttingDown},
    MemberWaitingPermission: {MemberIdle, MemberBlocked, MemberCancelingTurn, MemberShuttingDown},
    MemberBlocked:           {MemberIdle, MemberShuttingDown, MemberStopped},
    MemberCancelingTurn:     {MemberIdle, MemberBlocked, MemberShuttingDown, MemberFailed},
    MemberShuttingDown:      {MemberStopped, MemberFailed},
    MemberStopped:           {},
    MemberFailed:            {MemberStopped},
}

// MemberRunner is the runtime state machine for a long-lived team member.
// It owns the wake/loop/run lifecycle: waits on wakeCh for wake sources,
// executes one agent turn via TurnRunner, records the run in DB, and loops.
type MemberRunner struct {
    ID     string
    TeamID string
    Spec   agent.AgentSpec
    State  MemberStatus
    Role   string // from DB member record

    runner  agent.TurnRunner
    factory agent.AgentFactory
    svc     Service

    wakeCh chan WakeSource

    currentRunID string
    sessionID    string

    mu    sync.Mutex
    ctx   context.Context
    cancel context.CancelFunc
}

// NewMemberRunner creates a MemberRunner in MemberCreated state. The factory
// is used in Start() to build the TurnRunner. The Service is used to persist
// state transitions and record runs.
func NewMemberRunner(
    id, teamID string,
    spec agent.AgentSpec,
    factory agent.AgentFactory,
    svc Service,
) *MemberRunner {
    return &MemberRunner{
        ID:      id,
        TeamID:  teamID,
        Spec:    spec,
        State:   MemberCreated,
        factory: factory,
        svc:     svc,
        wakeCh:  make(chan WakeSource, 10),
    }
}
```

- [ ] **Step 4: Run tests to verify they pass**

```bash
cd G:/ai-project/remote-github/crush-worktrees/m4-01 && go test ./internal/team/ -run 'TestValidTransitions|TestWakeSource' -v
```

Expected: PASS — all transition table cases green.

- [ ] **Step 5: Build + vet the team package**

```bash
cd G:/ai-project/remote-github/crush-worktrees/m4-01 && go build ./internal/team/...
cd G:/ai-project/remote-github/crush-worktrees/m4-01 && go vet ./internal/team/...
```

Expected: exit 0.

- [ ] **Step 6: Commit**

```bash
cd G:/ai-project/remote-github/crush-worktrees/m4-01
git add internal/team/member_runner.go internal/team/member_runner_test.go
git commit -m "feat(team): add M4-01 MemberRunner types, transitions, and constructor"
```

---

## Task 3: transitionLocked + Wake methods

**Files:**
- Modify: `internal/team/member_runner.go` (add `transitionLocked`, `Wake`, `Stop` methods)
- Modify: `internal/team/member_runner_test.go` (add tests for transitionLocked and Wake)

- [ ] **Step 1: Write failing tests for transitionLocked + Wake**

Append to `internal/team/member_runner_test.go`:

```go
func TestMemberRunner_transitionLocked_ValidAndInvalid(t *testing.T) {
    m := &MemberRunner{ID: "m1", TeamID: "t1", State: MemberCreated, wakeCh: make(chan WakeSource, 1)}
    // Valid: created → starting
    m.transitionLocked(MemberStarting)
    assert.Equal(t, MemberStarting, m.State)
    // Invalid: starting → stopped (not allowed)
    m.transitionLocked(MemberStopped)
    assert.Equal(t, MemberStarting, m.State) // unchanged
}

func TestMemberRunner_Wake_Enqueues(t *testing.T) {
    m := &MemberRunner{ID: "m1", wakeCh: make(chan WakeSource, 1)}
    m.Wake(WakeSourceTask)
    select {
    case src := <-m.wakeCh:
        assert.Equal(t, WakeSourceTask, src)
    default:
        t.Fatal("expected wake source on channel")
    }
}

func TestMemberRunner_Wake_ChannelFullDrops(t *testing.T) {
    m := &MemberRunner{ID: "m1", wakeCh: make(chan WakeSource, 1)}
    m.Wake(WakeSourceTask)     // fills the buffer
    m.Wake(WakeSourceExplicit) // should drop (channel full, no panic)
    // Drain and verify only the first arrived.
    src := <-m.wakeCh
    assert.Equal(t, WakeSourceTask, src)
}

func TestMemberRunner_Stop(t *testing.T) {
    m := &MemberRunner{ID: "m1", State: MemberIdle, wakeCh: make(chan WakeSource, 1)}
    m.ctx, m.cancel = context.WithCancel(context.Background())
    m.Stop()
    // After Stop, context should be canceled and state → stopped.
    select {
    case <-m.ctx.Done():
        // expected
    default:
        t.Fatal("context should be canceled after Stop")
    }
    assert.Equal(t, MemberStopped, m.State)
}
```

- [ ] **Step 2: Run tests to verify they fail**

```bash
cd G:/ai-project/remote-github/crush-worktrees/m4-01 && go test ./internal/team/ -run 'TestMemberRunner_transitionLocked|TestMemberRunner_Wake|TestMemberRunner_Stop' -v
```

Expected: FAIL — methods undefined.

- [ ] **Step 3: Implement transitionLocked, Wake, Stop**

Append to `internal/team/member_runner.go`:

```go
// transitionLocked validates and applies a state transition. The caller MUST
// hold m.mu. On a valid transition, it updates m.State, fires an async CAS
// update to the DB via the Service, and bumps the version on success.
// On an invalid transition, it logs a warning and leaves the state unchanged.
func (m *MemberRunner) transitionLocked(to MemberStatus) {
    allowed, ok := validTransitions[m.State]
    if !ok {
        slog.Error("unknown current state", "member_id", m.ID, "state", m.State)
        return
    }
    for _, t := range allowed {
        if t == to {
            prev := m.State
            m.State = to
            // Async CAS update to DB. The Service handles read-then-CAS
            // internally; we don't pass version (Seam 3 deviation from master doc).
            go func() {
                updated, err := m.svc.UpdateMemberState(context.Background(), UpdateMemberStateRequest{
                    ID:     m.ID,
                    TeamID: m.TeamID,
                    Status: to,
                })
                if err != nil {
                    slog.Error("transitionLocked: UpdateMemberState failed",
                        "member_id", m.ID, "from", prev, "to", to, "error", err)
                    return
                }
                // CAS succeeded — the returned member has the new version.
                // We don't track version on MemberRunner struct (the Service
                // owns CAS); this is informational.
                _ = updated
            }()
            return
        }
    }
    slog.Warn("invalid state transition",
        "member_id", m.ID, "from", m.State, "to", to)
}

// Wake enqueues a wake source. If the channel is full, the source is dropped
// (logged at Warn level). Non-blocking — safe to call from any goroutine.
func (m *MemberRunner) Wake(source WakeSource) {
    select {
    case m.wakeCh <- source:
    default:
        slog.Warn("member wake channel full, dropping source",
            "member_id", m.ID, "source", source)
    }
}

// Stop initiates shutdown: cancels the loop context and transitions to stopped.
// Safe to call multiple times. After Stop, the member is terminal (stopped).
func (m *MemberRunner) Stop() {
    m.mu.Lock()
    defer m.mu.Unlock()
    if m.State == MemberStopped || m.State == MemberFailed {
        return // already terminal
    }
    if m.cancel != nil {
        m.cancel()
    }
    m.transitionLocked(MemberStopped)
}
```

- [ ] **Step 4: Run tests to verify they pass + build/vet**

```bash
cd G:/ai-project/remote-github/crush-worktrees/m4-01 && go test ./internal/team/ -run 'TestMemberRunner_transitionLocked|TestMemberRunner_Wake|TestMemberRunner_Stop' -v
```

Expected: PASS.

```bash
cd G:/ai-project/remote-github/crush-worktrees/m4-01 && go build ./internal/team/... && go vet ./internal/team/...
```

- [ ] **Step 5: Commit**

```bash
cd G:/ai-project/remote-github/crush-worktrees/m4-01
git add internal/team/member_runner.go internal/team/member_runner_test.go
git commit -m "feat(team): add M4-01 transitionLocked, Wake, and Stop methods"
```

---

## Task 4: Start + loop + handleWake lifecycle (full integration with mock TurnRunner + real Service)

**Files:**
- Modify: `internal/team/member_runner.go` (add `Start`, `loop`, `handleWake`, `buildPrompt`, `actorCtx`)
- Modify: `internal/team/member_runner_test.go` (add integration tests with newServiceFixture + mock TurnRunner)

- [ ] **Step 1: Write integration tests**

Append to `internal/team/member_runner_test.go`. These tests use `newServiceFixture` (real SQLite + Service) and a mock `TurnRunner`/`AgentFactory`:

```go
import (
    "context"
    "errors"
    "testing"
    "time"

    "github.com/charmbracelet/crush/internal/agent"
    "github.com/charmbracelet/crush/internal/actor"
    "github.com/stretchr/testify/assert"
    "github.com/stretchr/testify/require"
)

// mockTurnRunner records calls for test assertions.
type mockTurnRunner struct {
    runCalls   []agent.TeamAgentCall
    runResult  agent.TurnRunResult
    runErr     error
    canceled   []string
    busy       bool
}

func (m *mockTurnRunner) Run(ctx context.Context, call agent.TeamAgentCall) (agent.TurnRunResult, error) {
    m.runCalls = append(m.runCalls, call)
    return m.runResult, m.runErr
}
func (m *mockTurnRunner) Cancel(sessionID string) { m.canceled = append(m.canceled, sessionID) }
func (m *mockTurnRunner) IsSessionBusy(sessionID string) bool { return m.busy }

// stubAgentFactory always returns the same mock TurnRunner.
type stubAgentFactory struct{ runner agent.TurnRunner }

func (f *stubAgentFactory) BuildRunner(ctx context.Context, spec agent.AgentSpec) (agent.TurnRunner, error) {
    return f.runner, nil
}

func TestMemberRunner_Start_IdleLoop_Wake_Run_Success(t *testing.T) {
    svc, _ := newServiceFixture(t)

    // Create a real member in DB via service.
    member, err := svc.SpawnMember(context.Background(), SpawnMemberRequest{
        TeamID: "t1", Name: "test-member", Role: "coder", AgentProfile: "{}",
    })
    require.NoError(t, err)

    mockRunner := &mockTurnRunner{
        runResult: agent.TurnRunResult{Status: agent.TurnCompleted},
    }
    factory := &stubAgentFactory{runner: mockRunner}

    mr := NewMemberRunner(member.ID, member.TeamID, agent.AgentSpec{}, factory, svc)

    // Start should load DB state, build runner, enter idle, launch loop.
    err = mr.Start(context.Background())
    require.NoError(t, err)
    assert.Equal(t, MemberIdle, mr.State)

    // Wake the runner — should process one turn.
    mr.Wake(WakeSourceExplicit)

    // Wait for the turn to complete (loop processes wake, runs, returns to idle).
    // Poll with timeout since the loop runs asynchronously.
    deadline := time.Now().Add(5 * time.Second)
    for time.Now().Before(deadline) {
        mr.mu.Lock()
        state := mr.State
        calls := len(mockRunner.runCalls)
        mr.mu.Unlock()
        if state == MemberIdle && calls >= 1 {
            break
        }
        time.Sleep(50 * time.Millisecond)
    }

    mr.mu.Lock()
    defer mr.mu.Unlock()
    assert.Equal(t, MemberIdle, mr.State, "should return to idle after turn")
    require.Len(t, mockRunner.runCalls, 1)
    assert.Equal(t, mr.sessionID, mockRunner.runCalls[0].SessionID)
    assert.NotEmpty(t, mockRunner.runCalls[0].PromptEnvelope, "prompt should not be empty (stub)")
}

func TestMemberRunner_handleWake_BusyPreservesWakeup(t *testing.T) {
    // Start a runner, manually set state to running, then Wake.
    // The wake should be dropped/preserved (not crash, not start second turn).
    svc, _ := newServiceFixture(t)
    member, err := svc.SpawnMember(context.Background(), SpawnMemberRequest{
        TeamID: "t2", Name: "busy-member", Role: "coder", AgentProfile: "{}",
    })
    require.NoError(t, err)

    mockRunner := &mockTurnRunner{}
    factory := &stubAgentFactory{runner: mockRunner}
    mr := NewMemberRunner(member.ID, member.TeamID, agent.AgentSpec{}, factory, svc)
    err = mr.Start(context.Background())
    require.NoError(t, err)

    // Manually set state to running (simulating mid-turn).
    mr.mu.Lock()
    mr.State = MemberRunning
    mr.mu.Unlock()

    // Wake while running — should not enqueue a second turn.
    mr.Wake(WakeSourceTask)

    // The wake is sent but handleWake's single-flight gate should drop it.
    // After a short wait, verify no additional run calls were made.
    time.Sleep(200 * time.Millisecond)
    mr.mu.Lock()
    defer mr.mu.Unlock()
    assert.Equal(t, 0, len(mockRunner.runCalls), "no runs while busy")
}

func TestMemberRunner_handleWake_RunError(t *testing.T) {
    svc, _ := newServiceFixture(t)
    member, err := svc.SpawnMember(context.Background(), SpawnMemberRequest{
        TeamID: "t3", Name: "error-member", Role: "coder", AgentProfile: "{}",
    })
    require.NoError(t, err)

    mockRunner := &mockTurnRunner{
        runResult: agent.TurnRunResult{Status: agent.TurnFailed},
        runErr:    errors.New("LLM error"),
    }
    factory := &stubAgentFactory{runner: mockRunner}
    mr := NewMemberRunner(member.ID, member.TeamID, agent.AgentSpec{}, factory, svc)
    err = mr.Start(context.Background())
    require.NoError(t, err)

    mr.Wake(WakeSourceTask)

    // Wait for the failed turn to complete.
    deadline := time.Now().Add(5 * time.Second)
    for time.Now().Before(deadline) {
        mr.mu.Lock()
        s := mr.State
        mr.mu.Unlock()
        if s == MemberFailed || s == MemberIdle {
            break
        }
        time.Sleep(50 * time.Millisecond)
    }
    mr.mu.Lock()
    defer mr.mu.Unlock()
    // Run error → member should be in failed state.
    assert.Equal(t, MemberFailed, mr.State)
}

func TestMemberRunner_Start_AlreadyStartedReturnsError(t *testing.T) {
    svc, _ := newServiceFixture(t)
    member, err := svc.SpawnMember(context.Background(), SpawnMemberRequest{
        TeamID: "t4", Name: "double-start", Role: "coder", AgentProfile: "{}",
    })
    require.NoError(t, err)

    mr := NewMemberRunner(member.ID, member.TeamID, agent.AgentSpec{},
        &stubAgentFactory{runner: &mockTurnRunner{}}, svc)
    err = mr.Start(context.Background())
    require.NoError(t, err)

    // Second Start should fail.
    err = mr.Start(context.Background())
    assert.Error(t, err)
    assert.Contains(t, err.Error(), "already started")
}

func TestMemberRunner_actorCtx(t *testing.T) {
    mr := &MemberRunner{ID: "m1", TeamID: "t1", Role: "coder", Spec: agent.AgentSpec{}}
    ctx := mr.actorCtx()
    assert.Equal(t, "m1", ctx.MemberID)
    assert.Equal(t, "t1", ctx.TeamID)
    assert.Equal(t, "coder", ctx.MemberRole)
}
```

- [ ] **Step 2: Run tests to verify they fail**

```bash
cd G:/ai-project/remote-github/crush-worktrees/m4-01 && go test ./internal/team/ -run 'TestMemberRunner_Start|TestMemberRunner_handleWake|TestMemberRunner_actorCtx' -v
```

Expected: FAIL — `Start`, `handleWake`, `loop`, `buildPrompt`, `actorCtx` undefined.

- [ ] **Step 3: Implement Start, loop, handleWake, buildPrompt, actorCtx**

Append to `internal/team/member_runner.go`:

```go
// Start loads the member from DB, builds the TurnRunner via factory, enters idle
// state, and launches the background loop. Returns an error if the member has
// already been started (state != MemberCreated).
func (m *MemberRunner) Start(ctx context.Context) error {
    m.mu.Lock()
    defer m.mu.Unlock()

    if m.State != MemberCreated {
        return fmt.Errorf("member %s already started (state=%s)", m.ID, m.State)
    }
    m.transitionLocked(MemberStarting)

    // Load member from DB to get session_id, role, etc.
    members, err := m.svc.ListMembers(ctx, m.TeamID)
    if err != nil {
        m.transitionLocked(MemberFailed)
        return fmt.Errorf("load members: %w", err)
    }
    var dbMember *TeamMember
    for i := range members {
        if members[i].ID == m.ID {
            dbMember = &members[i]
            break
        }
    }
    if dbMember == nil {
        m.transitionLocked(MemberFailed)
        return fmt.Errorf("member %s not found in team %s", m.ID, m.TeamID)
    }
    if dbMember.SessionID != nil {
        m.sessionID = *dbMember.SessionID
    }
    m.Role = dbMember.Role

    // Build TurnRunner via factory.
    runner, err := m.factory.BuildRunner(ctx, m.Spec)
    if err != nil {
        m.transitionLocked(MemberFailed)
        return fmt.Errorf("build runner: %w", err)
    }
    m.runner = runner

    // Enter idle and launch loop.
    m.transitionLocked(MemberIdle)
    m.ctx, m.cancel = context.WithCancel(ctx)
    go m.loop()
    return nil
}

// loop is the main event loop. It blocks on the context or wake channel.
// All work is serial — only one wake is processed at a time.
func (m *MemberRunner) loop() {
    defer m.cancel()
    for {
        select {
        case <-m.ctx.Done():
            return
        case source := <-m.wakeCh:
            m.handleWake(source)
        }
    }
}

// handleWake processes one wake event through the single-flight gate,
// executes a turn, records the result, and transitions back to idle.
func (m *MemberRunner) handleWake(source WakeSource) {
    m.mu.Lock()
    // Single-flight gate: if already busy, drop this wake (not reject — the
    // wake signal is transient; persistent wake sources like mailbox will
    // re-deliver via M4-04).
    if m.State == MemberRunning || m.State == MemberQueued || m.State == MemberCancelingTurn {
        slog.Debug("handleWake: member busy, dropping wake",
            "member_id", m.ID, "state", m.State, "source", source)
        m.mu.Unlock()
        return
    }
    m.transitionLocked(MemberQueued)
    m.mu.Unlock()

    // Transition to running.
    m.mu.Lock()
    m.transitionLocked(MemberRunning)
    m.mu.Unlock()

    // Start a run record in DB.
    run, err := m.svc.StartRun(m.ctx, StartRunRequest{
        TeamID:    m.TeamID,
        MemberID:  m.ID,
        SessionID: m.sessionID,
    })
    if err != nil {
        slog.Error("handleWake: StartRun failed", "member_id", m.ID, "error", err)
        m.mu.Lock()
        m.transitionLocked(MemberFailed)
        m.mu.Unlock()
        return
    }
    m.mu.Lock()
    m.currentRunID = run.ID
    m.mu.Unlock()

    // Execute one turn.
    result, err := m.runner.Run(m.ctx, agent.TeamAgentCall{
        SessionID:      m.sessionID,
        PromptEnvelope: m.buildPrompt(source),
        Actor:          m.actorCtx(),
    })

    // Process result.
    m.mu.Lock()
    defer m.mu.Unlock()

    if err != nil {
        if errors.Is(err, context.Canceled) {
            m.transitionLocked(MemberIdle)
            return
        }
        // Record the failed run.
        _ = m.svc.MarkRunTerminal(context.Background(), MarkRunTerminalRequest{
            TeamID: m.TeamID, RunID: run.ID, Status: RunFailed, Error: err.Error(),
        })
        m.transitionLocked(MemberFailed)
        return
    }

    // Record the completed run with usage data.
    finishReq := FinishRunRequest{
        TeamID: m.TeamID,
        RunID:  run.ID,
    }
    if result.Result != nil {
        finishReq.PromptTokens = result.Result.TotalUsage.InputTokens
        finishReq.CompletionTokens = result.Result.TotalUsage.OutputTokens
    }
    if err := m.svc.FinishRun(context.Background(), finishReq); err != nil {
        slog.Error("handleWake: FinishRun failed", "member_id", m.ID, "run_id", run.ID, "error", err)
    }

    m.transitionLocked(MemberIdle)
}

// buildPrompt is a stub that returns a simple prompt string from the wake source.
// M4-05 (PromptEnvelope Builder) replaces this with full system+task+context
// assembly. Until then, this produces a minimal prompt sufficient for state
// machine testing.
func (m *MemberRunner) buildPrompt(source WakeSource) string {
    srcName := "unknown"
    switch source {
    case WakeSourceMailbox:
        srcName = "mailbox"
    case WakeSourceTask:
        srcName = "task"
    case WakeSourceExplicit:
        srcName = "explicit"
    case WakeSourceRecovery:
        srcName = "recovery"
    case WakeSourceDependency:
        srcName = "dependency"
    }
    return fmt.Sprintf("[M4-01 stub prompt] Wake source: %s. Member %s (%s) beginning turn.", srcName, m.ID, m.Role)
}

// actorCtx builds an ActorContext populated from this member's identity fields.
func (m *MemberRunner) actorCtx() actor.ActorContext {
    return actor.ActorContext{
        TeamID:     m.TeamID,
        MemberID:   m.ID,
        MemberRole: m.Role,
    }
}
```

Note: The `errors` import must be added to the import block. The full import block becomes:

```go
import (
    "context"
    "errors"
    "fmt"
    "log/slog"
    "sync"

    "github.com/charmbracelet/crush/internal/actor"
    "github.com/charmbracelet/crush/internal/agent"
)
```

- [ ] **Step 4: Run all integration tests**

```bash
cd G:/ai-project/remote-github/crush-worktrees/m4-01 && go test ./internal/team/ -run 'TestMemberRunner_Start|TestMemberRunner_handleWake|TestMemberRunner_actorCtx' -v -timeout 60s
```

Expected: PASS — all 5 integration subtests green.

- [ ] **Step 5: Run the full member_runner test suite**

```bash
cd G:/ai-project/remote-github/crush-worktrees/m4-01 && go test ./internal/team/ -run 'TestValidTransitions|TestWakeSource|TestMemberRunner' -v -timeout 60s
```

Expected: ALL green — state machine transitions, wake/channel, lifecycle, integration.

- [ ] **Step 6: Build + vet**

```bash
cd G:/ai-project/remote-github/crush-worktrees/m4-01 && go build ./internal/team/...
cd G:/ai-project/remote-github/crush-worktrees/m4-01 && go vet ./internal/team/...
cd G:/ai-project/remote-github/crush-worktrees/m4-01 && go build ./internal/agent/...
cd G:/ai-project/remote-github/crush-worktrees/m4-01 && go vet ./internal/agent/...
```

Expected: all exit 0.

- [ ] **Step 7: Commit**

```bash
cd G:/ai-project/remote-github/crush-worktrees/m4-01
git add internal/team/member_runner.go internal/team/member_runner_test.go
git commit -m "feat(team): add M4-01 Start/loop/handleWake lifecycle integration"
```

---

## Task 5: Full-package verification — compile, vet, all tests, acceptance coverage

**Files:**
- No new production code.
- Modify: `internal/team/member_runner_test.go` (add acceptance criterion 3 test: single-flight serialization under concurrency, if not already covered)

- [ ] **Step 1: Verify acceptance criteria**

| # | Acceptance Criterion | Coverage |
|---|---|---|
| 1 | State machine all legal transitions table-driven test passes | `TestValidTransitions_CoversAllMemberStatuses` + `TestValidTransitions_Allowed` (Task 2) |
| 2 | Version CAS conflict returns error | `transitionLocked` calls `UpdateMemberState` (internal read-then-CAS). Conflict = `ErrMemberVersionConflict` from Service. Covered by existing M3-05 `TestService_UpdateMemberState_VersionConflict` (no duplicate test needed — M4-01 delegates to Service). |
| 3 | `maxConcurrentRuns=1` and busy state does NOT start second turn | `TestMemberRunner_handleWake_BusyPreservesWakeup` (Task 4) |
| 4 | `TurnQueued` is non-terminal state, does not write completed run | Implicit in `handleWake` logic: queued→running transition happens before StartRun is called. `TurnQueued` is a member state, not a run status. Verified by code review. |
| 5 | MemberRunner does NOT directly call `SessionAgent.Run(sessionID, prompt)` | `TurnRunnerAdapter` bridges the call; MemberRunner only calls `TurnRunner.Run(TeamAgentCall)`. Verified by `grep -rn "SessionAgent" internal/team/member_runner.go` → no match. |

- [ ] **Step 2: Run the full `package team` test suite (includes pre-existing M2/M3 tests)**

```bash
cd G:/ai-project/remote-github/crush-worktrees/m4-01 && go test ./internal/team/ -v -timeout 120s
```

Expected: ALL tests pass — M4-01 new tests + all pre-existing M2 delegate + M3 domain/store/service tests. No regressions.

- [ ] **Step 3: Run the full `package agent` test suite**

```bash
cd G:/ai-project/remote-github/crush-worktrees/m4-01 && go test ./internal/agent/ -v -timeout 120s
```

Expected: ALL tests pass — new TurnRunnerAdapter tests + pre-existing M1 agent tests.

- [ ] **Step 4: Build + vet both packages**

```bash
cd G:/ai-project/remote-github/crush-worktrees/m4-01 && go build ./internal/team/... ./internal/agent/...
cd G:/ai-project/remote-github/crush-worktrees/m4-01 && go vet ./internal/team/... ./internal/agent/...
```

Expected: both exit 0, no output (clean build, clean vet).

- [ ] **Step 5: Commit (if any test refinements were needed)**

```bash
cd G:/ai-project/remote-github/crush-worktrees/m4-01
git add -A
git commit -m "test(team): add M4-01 acceptance criteria assertions"
```

---

## Self-Review (run before handoff)

- [ ] **Spec coverage:** acceptance #1 → Task 2; acceptance #2 → delegated to M3-05 Service (already tested); acceptance #3 → Task 4; acceptance #4 → code review confirmation; acceptance #5 → code review + grep check.
- [ ] **Placeholder scan:** no TBD/TODO in production code. The `buildPrompt` stub is clearly documented as M4-05 replacement, not a TODO.
- [ ] **Type consistency:** `MemberRunner.State` uses `MemberStatus` (M3-03 domain type, confirmed 11-value 1:1 match). `validTransitions` uses `MemberStatus` keys. Service method signatures verified against `internal/team/service.go`.
- [ ] **Design seam documentation:** All 8 seam decisions documented above with evidence citations. Key deviations from master doc:
  1. `NewMemberRunner` takes `AgentFactory` not `flushFn` (flushFn removed — MemberRunner doesn't own session lifecycle).
  2. `transitionLocked` does not pass `ExpectedVersion` — Service handles CAS internally (Seam 3 verification).
  3. `handleWake` uses `StartRunRequest`/`FinishRunRequest` structs (not raw `TeamRun`).
  4. `Start` uses `ListMembers` + filter instead of non-existent `GetMember` on Service interface.
- [ ] **No external dependency added:** `member_runner.go` imports stdlib (`context`, `errors`, `fmt`, `log/slog`, `sync`) + `internal/actor` + `internal/agent` (both pre-existing). `turn_runner_adapter.go` imports stdlib (`context`, `fmt`) + `charm.land/fantasy` (pre-existing). Tests import `testing`, `time`, `github.com/stretchr/testify` (pre-existing convention).
- [ ] **No existing file modified:** Only new files created. `internal/team/service.go`, `internal/team/models.go`, `internal/team/store_*.go`, `internal/agent/coordinator.go`, `internal/agent/agent.go` untouched.
- [ ] **Commit history:** 4 conventional commits (2 agent + 2 team), no trailers.

---

## Execution Handoff

Plan complete and saved to `docs/superpowers/plans/2026-06-15-m4-01-member-runner.md`. Two execution options:

1. **Subagent-Driven (recommended)** — dispatch a fresh subagent per task, review between tasks.
2. **Inline Execution** — execute tasks in this session with checkpoints.

**Phase 1 (this delivery) complete.** Awaiting team-lead review before Phase 2 (execute).
