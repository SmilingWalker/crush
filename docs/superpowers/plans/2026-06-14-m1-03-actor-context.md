# M1-03 ActorContext Identity System Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Create a new `internal/actor` package providing a unified `ActorContext` identity struct shared by tools, hooks, permission, and the team runtime — without depending on `internal/team`.

**Architecture:** A single-package identity system. `ActorContext` is a value type carried through `context.Context` via an unexported key. M1 populates the session/message/tool-call fields; M2+ team fields are reserved (declared, empty, `omitempty`). The package imports only the stdlib `context` package, guaranteeing no reverse dependency on the team domain.

**Tech Stack:** Go 1.26.3, module `github.com/charmbracelet/crush`, `github.com/stretchr/testify/assert` for tests (already a dependency, used in `internal/agent/agent_test.go`).

**Spec source:** `docs/agent-team-mode/plan/tasks/02-m1-subagent-foundation.md` section `## M1-03` (lines 504-617). Acceptance criteria 1-5 and 3 test cases are taken verbatim from that approved design.

---

## File Structure

**Files:**
- Create: `internal/actor/actor.go` — the `actor` package. Struct definition, context key, `WithContext`, `FromContext`, `ShortID`, `IsSubAgent`, `IsTeamMember`. Imports only `context`.
- Create: `internal/actor/actor_test.go` — package `actor_test` (external test package), `testify/assert`. The 3 doc test cases plus 2 additional tests covering acceptance criteria 3 (JSON omitempty) and 5 (`IsTeamMember`) that the doc specifies but does not give literal code for.

**Out of scope (do NOT touch):** `internal/config/` (owned by programmer-a). No merge to `agent-team`.

---

## Task 1: Failing tests for ActorContext round-trip, ShortID, IsSubAgent

**Files:**
- Create: `internal/actor/actor_test.go`

- [ ] **Step 1: Write the failing tests (the 3 doc test cases verbatim)**

Create `internal/actor/actor_test.go`:

```go
package actor_test

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/charmbracelet/crush/internal/actor"
	"github.com/stretchr/testify/assert"
)

func TestActorContext_RoundTrip(t *testing.T) {
	ctx := context.Background()
	_, ok := actor.FromContext(ctx)
	assert.False(t, ok)

	ac := actor.ActorContext{
		SessionID:       "sess_abc123",
		ParentSessionID: "sess_parent",
		MessageID:       "msg_456",
		ToolCallID:      "call_789",
	}
	ctx = ac.WithContext(ctx)

	got, ok := actor.FromContext(ctx)
	assert.True(t, ok)
	assert.Equal(t, "sess_abc123", got.SessionID)
	assert.Equal(t, "sess_parent", got.ParentSessionID)
}

func TestActorContext_ShortID(t *testing.T) {
	ac := actor.ActorContext{SessionID: "abcdefghijklmnop"}
	assert.Equal(t, "abcdefgh", ac.ShortID())

	ac2 := actor.ActorContext{SessionID: "short"}
	assert.Equal(t, "short", ac2.ShortID())
}

func TestActorContext_IsSubAgent(t *testing.T) {
	assert.True(t, actor.ActorContext{ParentSessionID: "parent"}.IsSubAgent())
	assert.False(t, actor.ActorContext{}.IsSubAgent())
}
```

- [ ] **Step 2: Run tests to verify they fail (red)**

Run: `go test ./internal/actor/ -v`
Expected: FAIL / build error — `internal/actor` package does not exist (`cannot find package "github.com/charmbracelet/crush/internal/actor"`).

---

## Task 2: Implement ActorContext to make Task 1 tests pass (green)

**Files:**
- Create: `internal/actor/actor.go`

- [ ] **Step 1: Write the minimal implementation (verbatim from the doc)**

Create `internal/actor/actor.go`:

```go
// Package actor 提供统一的Actor身份标识，供 tools、hooks、permission、team runtime 共享。
// 不依赖 internal/team，避免底层工具反向依赖 team domain。
package actor

import "context"

// ActorContext 表示当前操作的执行者身份。
// M1 使用 SessionID/ParentSessionID/MessageID/ToolCallID 四个核心字段。
// M2+ 逐步填充 TeamID/MemberID/TaskID/RunID 等团队相关字段。
type ActorContext struct {
	// --- M1 字段 ---
	SessionID       string `json:"session_id"`
	ParentSessionID string `json:"parent_session_id,omitempty"`
	MessageID       string `json:"message_id,omitempty"`
	ToolCallID      string `json:"tool_call_id,omitempty"`
	WorkspaceID     string `json:"workspace_id,omitempty"`

	// --- M2+ 预留字段（M1 定义，M2+ 填充）---
	TeamID     string `json:"team_id,omitempty"`
	MemberID   string `json:"member_id,omitempty"`
	MemberName string `json:"member_name,omitempty"`
	MemberRole string `json:"member_role,omitempty"`
	TaskID     string `json:"task_id,omitempty"`
	RunID      string `json:"run_id,omitempty"`
}

type actorContextKey struct{}

// WithContext 将 ActorContext 注入 context
func (a ActorContext) WithContext(ctx context.Context) context.Context {
	return context.WithValue(ctx, actorContextKey{}, a)
}

// FromContext 从 context 提取 ActorContext，不存在时返回 false
func FromContext(ctx context.Context) (ActorContext, bool) {
	v, ok := ctx.Value(actorContextKey{}).(ActorContext)
	return v, ok
}

// ShortID 返回截断的 SessionID（前8字符），用于日志
func (a ActorContext) ShortID() string {
	if len(a.SessionID) > 8 {
		return a.SessionID[:8]
	}
	return a.SessionID
}

// IsSubAgent 判断是否为子Agent（存在 ParentSessionID）
func (a ActorContext) IsSubAgent() bool {
	return a.ParentSessionID != ""
}

// IsTeamMember 判断是否为团队成员（M2+ 使用）
func (a ActorContext) IsTeamMember() bool {
	return a.TeamID != "" && a.MemberID != ""
}
```

- [ ] **Step 2: Run tests to verify they pass (green)**

Run: `go test ./internal/actor/ -v`
Expected: PASS — `TestActorContext_RoundTrip`, `TestActorContext_ShortID`, `TestActorContext_IsSubAgent` all pass.

---

## Task 3: Cover remaining acceptance criteria — JSON omitempty and IsTeamMember

**Files:**
- Modify: `internal/actor/actor_test.go` (append two tests)

Acceptance criterion 3 (JSON omitempty) and criterion 5 (`IsTeamMember`) are required by the spec but have no literal test code. Add explicit tests.

- [ ] **Step 1: Append the two additional failing tests**

Add to `internal/actor/actor_test.go`:

```go
func TestActorContext_JSONOmitempty(t *testing.T) {
	// M1 omitempty 字段为空时不应出现在 JSON 中；SessionID 无 omitempty，始终出现。
	ac := actor.ActorContext{SessionID: "sess_abc123"}
	data, err := json.Marshal(ac)
	assert.NoError(t, err)

	var decoded map[string]any
	assert.NoError(t, json.Unmarshal(data, &decoded))

	// SessionID 无 omitempty —— 必须出现
	assert.Contains(t, decoded, "session_id")
	assert.Equal(t, "sess_abc123", decoded["session_id"])

	// 所有 omitempty 字段为空 —— 不应出现
	for _, key := range []string{
		"parent_session_id", "message_id", "tool_call_id", "workspace_id",
		"team_id", "member_id", "member_name", "member_role", "task_id", "run_id",
	} {
		assert.NotContains(t, decoded, key, "empty field %s should be omitted", key)
	}
}

func TestActorContext_IsTeamMember(t *testing.T) {
	// 两个字段都填才是 team member
	assert.True(t, actor.ActorContext{TeamID: "team_1", MemberID: "member_1"}.IsTeamMember())
	// 缺一不可
	assert.False(t, actor.ActorContext{TeamID: "team_1"}.IsTeamMember())
	assert.False(t, actor.ActorContext{MemberID: "member_1"}.IsTeamMember())
	// 都空
	assert.False(t, actor.ActorContext{}.IsTeamMember())
}
```

- [ ] **Step 2: Run tests to verify they pass (green — implementation from Task 2 already covers these)**

Run: `go test ./internal/actor/ -v`
Expected: PASS — all 5 tests pass.

---

## Task 4: Verification — vet, build (no import cycle), full test

**Files:** none (verification only)

- [ ] **Step 1: Run go vet**

Run: `go vet ./internal/actor/`
Expected: no output (clean).

- [ ] **Step 2: Run go build on all internal packages to prove no import cycle**

Run: `go build ./internal/...`
Expected: no output (clean build). The `actor` package imports only `context`, so no cycle with `internal/team` or any other package is possible.

- [ ] **Step 3: Run the full test suite for the package, verbose**

Run: `go test ./internal/actor/ -v`
Expected: 5 tests, all PASS.

---

## Task 5: Self-review and commit

**Files:** none

- [ ] **Step 1: Self-review checklist**

Confirm against the spec's acceptance criteria:
1. Round-trip via `WithContext`/`FromContext` consistent — covered by `TestActorContext_RoundTrip`.
2. `FromContext` returns `(ActorContext{}, false)` when absent — covered (first assertion of `TestActorContext_RoundTrip`).
3. JSON omitempty drops empty fields — covered by `TestActorContext_JSONOmitempty`.
4. `ShortID()` truncates correctly — covered by `TestActorContext_ShortID`.
5. `IsSubAgent()` / `IsTeamMember()` correct — covered by their respective tests.

Confirm scope: only `internal/actor/actor.go` and `internal/actor/actor_test.go` created. `internal/config/` untouched. No merge to `agent-team`. Package imports only `context`.

- [ ] **Step 2: Commit (single-line conventional messages, no body, no trailer — repo convention)**

Two logical commits matching the test-first order, or a single commit if preferred. Repo uses single-line conventional commits (see `git log --oneline`):

```bash
git add internal/actor/actor.go internal/actor/actor_test.go
git commit -m "feat(actor): add ActorContext identity system"
```

If splitting implementation and tests into separate commits:

```bash
git add internal/actor/actor.go
git commit -m "feat(actor): add ActorContext identity system"
git add internal/actor/actor_test.go
git commit -m "test(actor): cover ActorContext round-trip and helpers"
```

Either is acceptable per the assignment; a single combined commit is simpler and the test file is the only consumer. Prefer the single combined commit.

- [ ] **Step 3: Report to team-lead**

Report status (DONE), paste actual test/vet/build output, the commit SHA, branch name, and worktree path.
