# M1-04 TurnRunner + AgentFactory Interface Definition Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Define the `TurnRunner` and `AgentFactory` interfaces plus their supporting types in a new `internal/agent/team_call.go`, giving the team/delegate/member runtime a stable contract for invoking agent turns without touching existing `agent.go` / `coordinator.go` types.

**Architecture:** A single new file `internal/agent/team_call.go` in the existing `agent` package adds purely declarative types and two interfaces. It depends on the M1-03 `internal/actor` package (for `actor.ActorContext`) and the existing `charm.land/fantasy` package (for `*fantasy.AgentResult`). No behavior is implemented — these are interface/type definitions only. The distinct `TeamAgentCall` name avoids collision with the pre-existing `SessionAgentCall` (`internal/agent/agent.go:72`).

**Tech Stack:** Go 1.26.3, module `github.com/charmbracelet/crush`. `charm.land/fantasy v0.26.0` (already in go.mod, exports `AgentResult`). `github.com/charmbracelet/crush/internal/actor` (built in M1-03, on this branch). `github.com/stretchr/testify/assert` for tests.

**Spec source:** `docs/agent-team-mode/plan/tasks/02-m1-subagent-foundation.md` section `## M1-04` (lines 621-740). Code (lines 631-713), 4 acceptance criteria, 2 test cases are taken from that approved design.

**Spec verification done before planning:**
- `charm.land/fantasy v0.26.0` is in `go.mod`; `go doc charm.land/fantasy` confirms `type AgentResult struct{ ... }`.
- `SessionAgentCall` already exists at `internal/agent/agent.go:72` and is consumed by `SessionAgent.Run` (`agent.go:87`) and `messageQueue` (`agent.go:123`). `TeamAgentCall` is a distinct name — no collision (acceptance criterion 1).
- The doc's test cases reference `TurnCompleted` / `TurnRunning` unqualified, so tests must live in package `agent` (internal test file), not an external `agent_test` package.

**Spec wording note (honest):** Acceptance criterion 2 says "completed/queued/canceled/failed 四种终态" (four terminal states), but the doc's own `IsTerminal()` returns true for only completed/canceled/failed — `TurnQueued` is explicitly non-terminal (comment "已入队，尚未执行") and `TurnRunning` is "仅用于状态查询" (status-query only). The doc's `TestTurnStatus_IsTerminal` asserts `TurnQueued` and `TurnRunning` are NOT terminal. So criterion 2's "four" refers to the four *distinct status values* while only three are terminal; the code and tests are self-consistent. Implement verbatim; do not "fix" `IsTerminal` to include `TurnQueued`.

---

## File Structure

**Files:**
- Create: `internal/agent/team_call.go` — package `agent` (internal, same package as `agent.go`). Imports `context`, `charm.land/fantasy`, `github.com/charmbracelet/crush/internal/actor`. Contains: `TurnStatus` type + 5 constants, `IsTerminal` method, `TeamAgentCall` struct, `ToolPolicyProfile` struct, `TurnRunResult` struct, `TurnRunner` interface, `AgentSpec` struct, `AgentFactory` interface. No function bodies beyond `IsTerminal`.
- Create: `internal/agent/team_call_test.go` — package `agent` (internal test, so unqualified `TurnCompleted` etc. resolve). The 2 doc test cases verbatim.

**Out of scope (do NOT touch):** `internal/agent/agent.go`, `internal/agent/coordinator.go`, any other existing file. No modifications to `SessionAgentCall` or any existing type. No merge to `agent-team` (team-lead handles merge after review).

---

## Task 1: Failing tests for TurnStatus.IsTerminal and TeamAgentCall type independence

**Files:**
- Create: `internal/agent/team_call_test.go`

- [ ] **Step 1: Write the failing tests (the 2 doc test cases verbatim)**

Create `internal/agent/team_call_test.go`:

```go
package agent

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestTurnStatus_IsTerminal(t *testing.T) {
	assert.True(t, TurnCompleted.IsTerminal())
	assert.True(t, TurnCanceled.IsTerminal())
	assert.True(t, TurnFailed.IsTerminal())
	assert.False(t, TurnQueued.IsTerminal())
	assert.False(t, TurnRunning.IsTerminal())
}

func TestTeamAgentCall_NotConfusableWithSessionAgentCall(t *testing.T) {
	// 编译时类型检查：TeamAgentCall 和 SessionAgentCall 是不同的类型
	var tc TeamAgentCall
	// 这行如果编译通过，说明两个类型确实独立
	_ = tc.SessionID
	_ = tc.ToolPolicy
}
```

Note: tests are in package `agent` (not `agent_test`) because the doc test references `TurnCompleted` etc. without a package qualifier, and `TeamAgentCall` is unexported-aware within the package. The existing `internal/agent/agent_test.go` already uses package `agent` for its internal tests, so this follows the established pattern.

- [ ] **Step 2: Run tests to verify they fail (red)**

Run: `go test ./internal/agent/ -run 'TestTurnStatus_IsTerminal|TestTeamAgentCall_NotConfusableWithSessionAgentCall'`
Expected: FAIL / build error — `TurnStatus`, `TurnCompleted`, `TurnQueued`, `TurnCanceled`, `TurnFailed`, `TurnRunning`, `TeamAgentCall` undefined (type `team_call.go` does not exist yet).

---

## Task 2: Implement team_call.go to make Task 1 tests pass (green)

**Files:**
- Create: `internal/agent/team_call.go`

- [ ] **Step 1: Write the implementation (verbatim from the doc)**

Create `internal/agent/team_call.go`:

```go
// internal/agent/team_call.go
package agent

import (
	"context"

	"charm.land/fantasy"
	"github.com/charmbracelet/crush/internal/actor"
)

// TurnStatus 表示一轮 agent turn 的终态
type TurnStatus string

const (
	TurnCompleted TurnStatus = "completed" // 正常完成
	TurnQueued    TurnStatus = "queued"    // 已入队，尚未执行
	TurnCanceled  TurnStatus = "canceled"  // 被取消
	TurnFailed    TurnStatus = "failed"    // 执行失败

	// TurnRunning 不是终态，仅用于状态查询
	TurnRunning TurnStatus = "running"
)

// IsTerminal 判断是否为终态
func (s TurnStatus) IsTerminal() bool {
	return s == TurnCompleted || s == TurnCanceled || s == TurnFailed
}

// TeamAgentCall 是 team/delegate/member runtime 调用 agent 的描述符。
// 它与 SessionAgentCall 是独立类型——SessionAgentCall 被 SessionAgent.Run 直接消费，
// TeamAgentCall 用于 TurnRunner 接口，两个不能混淆。
type TeamAgentCall struct {
	SessionID       string
	ParentSessionID string
	PromptEnvelope  string             // 组装后的完整提示词
	Actor           actor.ActorContext // 执行者身份
	ToolPolicy      ToolPolicyProfile  // 工具策略
}

// ToolPolicyProfile 定义工具使用策略
type ToolPolicyProfile struct {
	AllowedTools    []string // 白名单（nil = 全部允许）
	DisallowedTools []string // 黑名单（额外禁止）
	PermissionMode  string   // default | acceptEdits | plan | bypassPermissions
}

// TurnRunResult 是一轮 agent turn 的结果
// Status 为 queued 时 Result 为 nil（表示已入队但尚未执行）
type TurnRunResult struct {
	Status TurnStatus
	Result *fantasy.AgentResult
}

// TurnRunner 是 agent turn 的执行接口。
// M1 用于单个子Agent，M2 DelegateRunner 用于并行搜索，
// M4 MemberRunner 用于长期队员。三层共用同一接口。
type TurnRunner interface {
	// Run 执行一轮 agent turn
	Run(ctx context.Context, call TeamAgentCall) (TurnRunResult, error)

	// Cancel 取消指定 session 的当前 turn
	Cancel(sessionID string)

	// IsSessionBusy 检查 session 是否有活跃的 turn
	IsSessionBusy(sessionID string) bool
}

// AgentSpec 描述创建一个 TurnRunner 所需的参数
type AgentSpec struct {
	AgentType      string            // agent 类型标识 (如 "general-purpose")
	SystemPrompt   string            // 系统提示词
	ModelType      string            // 模型选择 ("large" | "small" | "inherit")
	PermissionMode string            // 权限模式
	ToolPolicy     ToolPolicyProfile // 工具策略
}

// AgentFactory 创建独立 SessionAgent 实例的工厂。
// 每个 runner 持有独立实例——不复用 Coordinator.currentAgent，
// 确保 coder 的 SetTools/SetModels 不影响 delegate/member runner。
type AgentFactory interface {
	BuildRunner(ctx context.Context, spec AgentSpec) (TurnRunner, error)
}
```

- [ ] **Step 2: Run the new tests to verify they pass (green)**

Run: `go test ./internal/agent/ -run 'TestTurnStatus_IsTerminal|TestTeamAgentCall_NotConfusableWithSessionAgentCall' -v`
Expected: PASS — both tests pass.

---

## Task 3: Verify no regression across the whole agent package + no import cycle

**Files:** none (verification only)

The new file is in the existing `agent` package, so the whole package must still compile and all existing tests must still pass.

- [ ] **Step 1: Build the whole internal tree**

Run: `go build ./internal/...`
Expected: exit 0, clean. Confirms `team_call.go` compiles within package `agent`, `actor` imports resolve, and no import cycle (agent → actor, actor imports only `context`).

- [ ] **Step 2: Run go vet on the agent package**

Run: `go vet ./internal/agent/`
Expected: exit 0, clean.

- [ ] **Step 3: Run the full agent package test suite**

Run: `go test ./internal/agent/ -count=1`
Expected: PASS — all existing agent tests plus the 2 new tests pass. `-count=1` disables test caching to force a real re-run.

If any pre-existing test fails, investigate: it must be unrelated to this change (team_call.go only adds declarations; it cannot alter behavior). If a failure is traced to the new file, fix it.

---

## Task 4: Self-review and commit

**Files:** none

- [ ] **Step 1: Self-review against the 4 acceptance criteria**

1. **Compiles, `TeamAgentCall` vs `SessionAgentCall` no collision** — `SessionAgentCall` lives at `internal/agent/agent.go:72`; `TeamAgentCall` is a distinct type in `team_call.go`. `go build ./internal/agent/` succeeds. `TestTeamAgentCall_NotConfusableWithSessionAgentCall` is a compile-time check that both exist independently. ✓
2. **`TurnRunResult` distinguishes completed/queued/canceled/failed** — `TurnRunResult.Status` is `TurnStatus`, whose constants cover all four (plus `TurnRunning`). ✓ (See spec-wording note above: only 3 of the 4 are terminal, which is the intended design.)
3. **`IsTerminal()` correct for all states** — `TestTurnStatus_IsTerminal` asserts completed/canceled/failed terminal, queued/running not. ✓
4. **No modification to `agent.go` / `coordinator.go` existing types** — only `git status` should show `internal/agent/team_call.go` and `internal/agent/team_call_test.go` as new files; nothing else modified. ✓

- [ ] **Step 2: Confirm scope via git status**

Run: `git status --short`
Expected: only `internal/agent/team_call.go` and `internal/agent/team_call_test.go` (plus this plan doc, untracked). No `agent.go` / `coordinator.go` modifications.

- [ ] **Step 3: Commit (single-line conventional message, no body, no trailer — repo convention)**

```bash
git add internal/agent/team_call.go internal/agent/team_call_test.go
git commit -m "feat(agent): add TurnRunner and AgentFactory interfaces"
```

- [ ] **Step 4: Report to team-lead**

Report status (DONE), paste actual build/vet/test output, the commit SHA, branch name (`m1-04-turnrunner`) and worktree path (`g:/ai-project/remote-github/crush-worktrees/m1-04`). Do NOT merge to agent-team.
