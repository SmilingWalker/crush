# M1-08 Agent Prompt Templates Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Ship three built-in sub-agent prompt templates (general-purpose, explore, plan) and a Go loader (`DefaultTemplates()`) that embeds them at compile time, providing the default `system_prompt` source for the built-in agents registered in M1-01.

**Architecture:** Three standalone Markdown files are embedded via `//go:embed` into one new Go file in package `agent`. `DefaultTemplates()` returns a `map[string]string` keyed by agent name. Each template is fully independent — editing one never touches the others — and each embed variable is a distinct `string`, so a future caller (or a `crush.json` override) can address them individually or collectively.

**Tech Stack:** Go 1.24, `embed` (standard library), `github.com/stretchr/testify` (assert/require). Module `github.com/charmbracelet/crush`. Shell: bash on win32 (forward slashes, `/dev/null`).

---

## PREREQ-0: Template directory + package placement — DECIDED (option B)

**Team-lead decision (2026-06-14): option B — loader in package `agent`, templates in the existing `internal/agent/templates/` dir.**

The M1-08 spec section (`docs/agent-team-mode/plan/tasks/02-m1-subagent-foundation.md`, section `## M1-08`) shows sample loader code in `package prompt`:

```go
// internal/agent/prompt/agent_prompts.go
package prompt
//go:embed templates/general_purpose.md
var GeneralPurposeTemplate string
```

That sample did **not** account for two realities:

1. **`go:embed` forbids `..` path segments.** A loader in `internal/agent/prompt/` cannot reach a `templates/` dir that lives in `internal/agent/templates/` — there is no valid embed path between them. (This is what blocked the first attempt; team-lead confirmed.)
2. **The repo's established pattern is package `agent` embedding from `internal/agent/templates/`.** Existing proof: `internal/agent/prompts.go` does `//go:embed templates/coder.md.tpl` (package `agent`), and `internal/agent/agent_tool.go` does `//go:embed templates/agent_tool.md` (package `agent`). Both are package-`agent` files embedding from the same `internal/agent/templates/` dir.

**Decision: option B** — match the established pattern:

- Create `internal/agent/templates/general_purpose.md`
- Create `internal/agent/templates/explore.md`
- Create `internal/agent/templates/plan.md`
- Create `internal/agent/agent_prompts.go` (**package `agent`**, NOT `prompt`) with `//go:embed templates/general_purpose.md` etc. (paths resolve to `internal/agent/templates/*`), exposing `DefaultTemplates()` as **`agent.DefaultTemplates()`**.

**Spec deviation (recorded):** the loader lives in package `agent` (not `prompt`) to satisfy `//go:embed` co-location and match the existing `coder.md.tpl`/`agent_tool.md` embed pattern. `DefaultTemplates()` is exposed as `agent.DefaultTemplates()`; future consumers call it from package `agent`. Option A (a second `internal/agent/prompt/templates/` dir) was rejected because it fragments where agent prompts live — option B keeps **all** agent prompts in one dir.

**Collision pre-check (done):** grepped `internal/` for `DefaultTemplates` / `GeneralPurposeTemplate` / `ExploreTemplate` / `PlanTemplate` — no pre-existing matches. Names are free for package `agent`.

**Note:** the existing `internal/agent/templates/` dir (coder/task/summary/title/agent_tool/agentic_fetch prompts) is **appended to**, not modified — the three new files are additive and do not touch the existing main-agent templates.

---

## File Structure

**Files touched (scope is STRICT):**
- Create: `internal/agent/templates/general_purpose.md` — general-purpose autonomous sub-agent prompt.
- Create: `internal/agent/templates/explore.md` — read-only file-search specialist prompt.
- Create: `internal/agent/templates/plan.md` — read-only planning/architect specialist prompt.
- Create: `internal/agent/agent_prompts.go` — package `agent`, `//go:embed` the three templates + `DefaultTemplates()`.
- Create: `internal/agent/agent_prompts_test.go` — test for `DefaultTemplates()` (package `agent` test file).

**Files NOT touched (other tasks / out of scope):** `internal/config/` (M1-01 owns agent registration and the `system_prompt` override plumbing; acceptance criterion 2 — user `system_prompt` override — is enforced there, not here), `internal/actor/` (M1-03), `internal/agent/coordinator.go` (M1-05/06), `internal/agent/agent_tool.go` (M1-02), `internal/agent/prompts.go` and `internal/agent/agent.go` (existing embed-bearing files — no change), `internal/agent/prompt/prompt.go` (no change to existing prompt builder), the existing entries inside `internal/agent/templates/` (only additive new files).

---

## Task 1: Write the failing test for `DefaultTemplates()`

**Files:**
- Create: `internal/agent/agent_prompts_test.go`

- [ ] **Step 1: Write the failing test**

Create `internal/agent/agent_prompts_test.go`:

```go
package agent

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDefaultTemplates_ReturnsAllThreeKeys(t *testing.T) {
	tmpls := DefaultTemplates()

	require.Contains(t, tmpls, "general-purpose", "general-purpose template must be registered")
	require.Contains(t, tmpls, "explore", "explore template must be registered")
	require.Contains(t, tmpls, "plan", "plan template must be registered")
	assert.Len(t, tmpls, 3, "DefaultTemplates must register exactly the three built-in agents")
}

func TestDefaultTemplates_EachNonEmpty(t *testing.T) {
	for name, body := range DefaultTemplates() {
		body := body
		t.Run(name, func(t *testing.T) {
			assert.NotEmpty(t, body, "template %q must not be empty", name)
			// Sanity: every template starts with its role sentence, not a stray
			// empty line, so the embed picked up the right file.
			assert.False(t, strings.HasPrefix(body, "\n"), "template %q must not start with a blank line", name)
		})
	}
}

func TestDefaultTemplates_RoleMarkersPresent(t *testing.T) {
	tmpls := DefaultTemplates()

	assert.Contains(t, tmpls["general-purpose"], "general-purpose agent",
		"general-purpose template must describe its role")
	assert.Contains(t, tmpls["explore"], "search",
		"explore template must describe its read-only search role")
	assert.Contains(t, tmpls["plan"], "plan",
		"plan template must describe its planning role")
}

func TestDefaultTemplates_ReadOnlyTemplatesForbidMutations(t *testing.T) {
	// Acceptance criterion 3: explore/plan templates must not instruct the
	// agent to write/edit/bash — they are read-only specialists.
	// The general-purpose template legitimately uses write/bash, so it is
	// excluded from this assertion.
	tmpls := DefaultTemplates()

	for _, name := range []string{"explore", "plan"} {
		body := strings.ToLower(tmpls[name])
		assert.NotContains(t, body, "use `write`",
			"read-only template %q must not contain a write-tool usage line", name)
		assert.NotContains(t, body, "use `edit`",
			"read-only template %q must not contain an edit-tool usage line", name)
		assert.NotContains(t, body, "use `bash`",
			"read-only template %q must not contain a bash-tool usage line", name)
	}
}

func TestDefaultTemplates_TemplatesAreIndependent(t *testing.T) {
	// Acceptance criterion 4: each template is a distinct string; editing one
	// cannot affect another. We assert the three bodies are pairwise unequal.
	tmpls := DefaultTemplates()
	values := []string{tmpls["general-purpose"], tmpls["explore"], tmpls["plan"]}

	for i := 0; i < len(values); i++ {
		for j := i + 1; j < len(values); j++ {
			assert.NotEqual(t, values[i], values[j],
				"templates at index %d and %d must be distinct", i, j)
		}
	}
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `go test ./internal/agent/ -run TestDefaultTemplates -v`
Expected: compile error — `undefined: DefaultTemplates` (function not yet defined). This is the red state.

---

## Task 2: Create the three template files

**Files:**
- Create: `internal/agent/templates/general_purpose.md`
- Create: `internal/agent/templates/explore.md`
- Create: `internal/agent/templates/plan.md`

- [ ] **Step 1: Create `general_purpose.md`**

Create `internal/agent/templates/general_purpose.md` with EXACTLY this content (verbatim from the spec — no leading/trailing blank lines beyond what is shown, single trailing newline):

````markdown
You are a general-purpose agent for Crush. Given the user's prompt, use the tools
available to complete the task autonomously.

## Your strengths
- Searching for code, configurations, and patterns across large codebases
- Analyzing multiple files to understand system architecture
- Investigating complex questions that require exploring many files
- Performing multi-step research tasks
- Executing bash commands to run tests and verify code

## Guidelines
- Be thorough: Check multiple locations, consider different naming conventions
- NEVER create files unless absolutely necessary for achieving your goal
- NEVER proactively create documentation files (*.md) or README files
- Return absolute file paths in your responses
- Be concise and direct in your final response
- If you need to search broadly, use multiple parallel tool calls

## Tool usage
- Use `glob` for file pattern matching
- Use `grep` for searching file contents with regex
- Use `view` when you know the specific file path
- Use `ls` for directory listing
- Use `bash` for running commands (tests, git operations)
- Use `write` for creating or modifying files

## Important constraints
- You are a sub-agent: your output goes back to the main agent, not directly to the user
- Do NOT call the `agent` tool (recursive sub-agents are disabled)
- Do NOT use `ask_user_questions` (you cannot interact with the user)
- Complete your task fully before responding
````

- [ ] **Step 2: Create `explore.md`**

Create `internal/agent/templates/explore.md` with EXACTLY this content:

````markdown
You are a fast file search specialist for Crush. Your role is exclusively to search
and explore code.

=== CRITICAL: READ-ONLY MODE ===
You are STRICTLY PROHIBITED from:
- Creating new files (no write, touch, or file creation)
- Modifying existing files (no edit operations)
- Deleting files (no rm or deletion)
- Running ANY commands that change system state
- Calling other agents

Your strengths:
- Rapidly finding files using glob patterns
- Searching code with powerful regex patterns via grep
- Reading and analyzing file contents

Guidelines:
- Make efficient use of tools: be smart about how you search
- Spawn multiple parallel tool calls for grepping and reading files where possible
- Return results as quickly as possible
- Adapt search thoroughness based on the caller's instructions
````

- [ ] **Step 3: Create `plan.md`**

Create `internal/agent/templates/plan.md` with EXACTLY this content:

````markdown
You are a software architect and planning specialist for Crush. Your role is to
explore the codebase and design implementation plans.

=== CRITICAL: READ-ONLY MODE ===
You are STRICTLY PROHIBITED from:
- Creating, modifying, or deleting files
- Running commands that change system state
- Calling other agents

Your process:
1. Read any files provided in the initial prompt
2. Find existing patterns and conventions
3. Understand the current architecture
4. Identify similar features as reference
5. Design the implementation approach
6. Provide step-by-step implementation strategy
7. Identify dependencies and potential challenges

Required output: End with "### Critical Files for Implementation" listing 3-5 most critical files.
````

- [ ] **Step 4: Verify all three files exist and are non-empty**

Run: `ls -l internal/agent/templates/general_purpose.md internal/agent/templates/explore.md internal/agent/templates/plan.md`
Expected: three files, each > 0 bytes.

---

## Task 3: Implement the loader (`agent_prompts.go`)

**Files:**
- Create: `internal/agent/agent_prompts.go`

- [ ] **Step 1: Write the loader**

Create `internal/agent/agent_prompts.go`:

```go
package agent

import _ "embed"

// GeneralPurposeTemplate is the system prompt for the general-purpose
// sub-agent: an autonomous worker with read/write/bash tool access. It is the
// default system_prompt baseline for the built-in "general-purpose" agent
// registered in internal/config (M1-01); a user-supplied system_prompt in
// crush.json takes precedence over this value.
//
//go:embed templates/general_purpose.md
var GeneralPurposeTemplate string

// ExploreTemplate is the system prompt for the explore sub-agent: a
// read-only file-search specialist.
//
//go:embed templates/explore.md
var ExploreTemplate string

// PlanTemplate is the system prompt for the plan sub-agent: a read-only
// software-architecture / planning specialist.
//
//go:embed templates/plan.md
var PlanTemplate string

// DefaultTemplates returns the built-in agent default template map keyed by
// agent name. The returned map is freshly allocated on each call so callers
// may mutate it without affecting subsequent calls or package state. The keys
// match the built-in agent names (general-purpose / explore / plan) registered
// in internal/config (M1-01).
func DefaultTemplates() map[string]string {
	return map[string]string{
		"general-purpose": GeneralPurposeTemplate,
		"explore":         ExploreTemplate,
		"plan":            PlanTemplate,
	}
}
```

- [ ] **Step 2: Run the test to verify it now passes (green)**

Run: `go test ./internal/agent/ -run TestDefaultTemplates -v`
Expected: all five `TestDefaultTemplates*` tests PASS.

---

## Task 4: Verification before completion

**Files:** none modified.

- [ ] **Step 1: Targeted template tests, verbose**

Run: `go test ./internal/agent/ -run TestDefaultTemplates -v`
Expected: all five `TestDefaultTemplates*` tests PASS.

- [ ] **Step 2: Whole agent package test (no regressions)**

Run: `go test ./internal/agent/`
Expected: PASS — existing agent tests unaffected (we only added a new file + new test file; no existing symbol changed).

- [ ] **Step 3: Whole-tree build**

Run: `go build ./internal/...`
Expected: no output, exit 0.

- [ ] **Step 4: Vet**

Run: `go vet ./internal/agent/`
Expected: no output, exit 0.

- [ ] **Step 5: Confirm embed actually captured file contents (not empty)**

Run: `go test ./internal/agent/ -run TestDefaultTemplates_EachNonEmpty -v`
Expected: PASS — proves the `//go:embed` directives resolved to non-empty file bodies (a wrong embed path would compile but yield an empty string, failing this test).

---

## Task 5: Commit

- [ ] **Step 1: Stage and commit**

```bash
git add internal/agent/templates/general_purpose.md \
        internal/agent/templates/explore.md \
        internal/agent/templates/plan.md \
        internal/agent/agent_prompts.go \
        internal/agent/agent_prompts_test.go \
        docs/superpowers/plans/2026-06-14-m1-08-agent-templates.md
git commit -m "feat(agent): add built-in agent prompt templates"
```

(Repo convention: clean single-line conventional-commit message, NO body, NO trailer.)

---

## Self-Review

**1. Spec coverage — acceptance criteria from `## M1-08`:**

| Criterion | Covered by |
|---|---|
| 1. general-purpose system prompt contains the role description | `TestDefaultTemplates_RoleMarkersPresent` asserts `general-purpose` contains `"general-purpose agent"`; Task 2 Step 1 template line 1 satisfies it. |
| 2. user `system_prompt` in crush.json overrides the built-in | OUT OF SCOPE for this task — enforced in M1-01's `internal/config` merge (PREREQ in the M1-01 plan records the user-override merge). `DefaultTemplates()` is the *baseline* the override layers on top of. Documented in File Structure "NOT touched" + PREREQ-0. |
| 3. explore/plan templates contain no write/edit/bash usage lines | `TestDefaultTemplates_ReadOnlyTemplatesForbidMutations` asserts absence of `use \`write\``, `use \`edit\``, `use \`bash\`` in both; Task 2 Steps 2-3 templates omit them (they only mention read-only tools / prohibitions). |
| 4. three templates independent — modifying one doesn't affect others | `TestDefaultTemplates_TemplatesAreIndependent` asserts pairwise-unequal; structurally three separate files + three separate embed vars guarantee it. |

Criterion 2 is correctly delegated to M1-01/config — `DefaultTemplates()` returning a baseline map is the contract this task owns; the override precedence is a config-layer concern.

**2. Placeholder scan:** No TODO/TBD/"add error handling"/"similar to Task N". Every code step contains full, copy-pasteable content. PREREQ-0 resolves the only open decision with a concrete, team-lead-approved choice (option B).

**3. Type consistency:** `DefaultTemplates() map[string]string` signature is identical in the test (Task 1) and implementation (Task 3). Keys `"general-purpose"`, `"explore"`, `"plan"` are consistent across test, implementation, and the M1-01 built-in agent names (`AgentGeneralPurpose` / `AgentExplore` / `AgentPlan` per `internal/config/config.go:59-62`). Embed variable names `GeneralPurposeTemplate` / `ExploreTemplate` / `PlanTemplate` are consistent between declarations (Task 3) and usage in `DefaultTemplates()` (Task 3); the test goes through the map only, so no cross-task name drift. The package is `agent` throughout (test, loader), matching PREREQ-0 option B.
