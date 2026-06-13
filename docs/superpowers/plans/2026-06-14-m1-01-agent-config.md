# M1-01 Agent Config Struct Extension Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Make `Config.Agents` serializable, extend the `Agent` struct with M1 fields (DisallowedTools/SystemPrompt/PermissionMode) plus M2-M4 reserved fields, register four built-in agents (coder/general-purpose/explore/plan) with user-override merging, and add Agent ID + PermissionMode constants.

**Architecture:** All changes live in `internal/config/config.go`. The `Agent` struct gains fields; `SetupAgents()` is rewritten to seed 4 (or 5, see PREREQ) builtins then merge user-supplied `c.Agents` overrides via non-zero-field semantics (new keys become custom agents). Existing tests in `internal/config/` that assert on the old builtin set are updated to the new reality — never weakened.

**Tech Stack:** Go 1.24, `encoding/json`, `github.com/stretchr/testify` (assert/require). Module `github.com/charmbracelet/crush`. Shell: bash on win32 (forward slashes, `/dev/null`).

---

## PREREQ-0: AgentTask registration — DECIDED (option B)

**Team-lead decision (2026-06-14): option (B) — keep `task` as a 5th compat builtin, unchanged.**

The M1-01 spec section (`docs/agent-team-mode/plan/tasks/02-m1-subagent-foundation.md:78-189`) registers ONLY coder/general-purpose/explore/plan and drops the existing `task` agent. We deviate from the literal spec by keeping `task` registered. **Why (recorded so it isn't lost):**

- `internal/agent/agent_tool.go:27` reads `c.cfg.Config().Agents[config.AgentTask]` and returns `errors.New("task agent not configured")` if absent — the live, user-facing `agent` tool, owned by programmer-b / reworked in M1-02 (out of this task's config-only scope).
- Current `SetupAgents()` (`internal/config/config.go:724`) ALREADY registers `AgentTask`. The spec's literal 4-builtin map is a regression — it would drop a currently-registered agent.
- `internal/config/agent_id_test.go:25` and `internal/config/load_test.go:689,711,734` assert `Agents[AgentTask]` is present. Acceptance criterion 6 requires these to keep passing; criterion 7 requires nothing to break.
- Criterion 4 says SetupAgents must **包含** (contain) coder/general-purpose/explore/plan — "contain", not "only" — so a 5th entry satisfies it.

**Carry-forward to M1-02:** retiring `task` (migrating the `agent` tool to general-purpose) is deferred to M1-02, where `agent_tool.go` is reworked. Tracked on the team task board by team-lead.

**Implementation constraints from team-lead:**
- Leave the existing `AgentTask` builtin entry **byte-for-byte unchanged** (Name/Description/Model/AllowedTools pinned by `load_test.go`).
- Preserve `coder`'s existing behavior — keep current Name "Coder" and Description "An agent that helps with executing coding tasks." (do NOT adopt the spec's differing coder Description). Task 4 Step 3 uses these exact strings.
- Apply the user-override merge to **all** builtins including `task` (the merge loop iterates `c.Agents` generically — automatic).

---

## File Structure

**Files touched (scope is STRICT):**
- Modify: `internal/config/config.go` (Agent struct ~L496-517, Agents json tag ~L590, Agent ID constants ~L59-62, SetupAgents ~L711-736, new PermissionMode constants block).
- Modify: `internal/config/agent_id_test.go` (extend builtin-presence assertions).
- Modify: `internal/config/load_test.go` (3 SetupAgents tests: update `task` assertions / add new builtin assertions).
- Create: `internal/config/agents_setup_test.go` (new tests from spec: builtins-always-present, user-override-builtin, user-adds-custom, disabled-filtering, round-trip serialization).

**Files NOT touched (programmer-b / other tasks):** `internal/agent/*`, `internal/config/load.go`, `internal/config/store.go` (no behavior change needed there — `SetupAgents` is already called by load/store and reads `c.Options` which load.go guarantees non-nil).

---

## Task 1: Extend the Agent struct with M1 + reserved fields

**Files:**
- Modify: `internal/config/config.go:496-517`

- [ ] **Step 1: Write a failing test that asserts the new struct fields exist**

Create `internal/config/agents_setup_test.go` with the first test:

```go
package config

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// TestAgent_StructFieldsExist guards the M1-01 struct extension: each new
// field must be present, exported, and tagged for JSON serialization.
func TestAgent_StructFieldsExist(t *testing.T) {
	a := Agent{
		DisallowedTools: []string{"agent", "bash"},
		SystemPrompt:    "you are a reviewer",
		PermissionMode:  "plan",
		MaxTurns:        5,
		Skills:          []string{"code-review"},
		McpServers:      []string{"github"},
	}
	assert.Equal(t, []string{"agent", "bash"}, a.DisallowedTools)
	assert.Equal(t, "you are a reviewer", a.SystemPrompt)
	assert.Equal(t, "plan", a.PermissionMode)
	assert.Equal(t, 5, a.MaxTurns)
	assert.Equal(t, []string{"code-review"}, a.Skills)
	assert.Equal(t, []string{"github"}, a.McpServers)
}
```

- [ ] **Step 2: Run test to verify it fails (red)**

Run: `go test ./internal/config/ -run TestAgent_StructFieldsExist -v`
Expected: FAIL — compile error `unknown field 'DisallowedTools'` (struct fields don't exist yet).

- [ ] **Step 3: Extend the Agent struct (minimal impl)**

In `internal/config/config.go`, replace the `Agent` struct (lines ~496-517) with:

```go
type Agent struct {
	ID          string `json:"id,omitempty"`
	Name        string `json:"name,omitempty"`
	Description string `json:"description,omitempty"`
	// This is the id of the system prompt used by the agent
	Disabled bool `json:"disabled,omitempty"`

	Model SelectedModelType `json:"model" jsonschema:"required,description=The model type to use for this agent,enum=large,enum=small,default=large"`

	// The available tools for the agent
	//  if this is nil, all tools are available
	AllowedTools []string `json:"allowed_tools,omitempty"`

	// this tells us which MCPs are available for this agent
	//  if this is empty all mcps are available
	//  the string array is the list of tools from the AllowedMCP the agent has available
	//  if the string array is nil, all tools from the AllowedMCP are available
	AllowedMCP map[string][]string `json:"allowed_mcp,omitempty"`

	// Overrides the context paths for this agent
	ContextPaths []string `json:"context_paths,omitempty"`

	// --- M1 new fields ---

	// Per-agent tool denylist.
	DisallowedTools []string `json:"disallowed_tools,omitempty"`

	// Per-agent system prompt template.
	SystemPrompt string `json:"system_prompt,omitempty"`

	// Permission mode: default | acceptEdits | plan | bypassPermissions
	PermissionMode string `json:"permission_mode,omitempty" jsonschema:"description=Permission mode for this agent,enum=default,enum=acceptEdits,enum=plan,enum=bypassPermissions"`

	// --- M2-M4 reserved fields (M1 only defines the schema, not used yet) ---
	MaxTurns   int      `json:"max_turns,omitempty" jsonschema:"description=Maximum number of agentic turns before stopping"`
	Skills     []string `json:"skills,omitempty" jsonschema:"description=Skill names to preload for this agent"`
	McpServers []string `json:"mcp_servers,omitempty" jsonschema:"description=MCP server names available to this agent"`
}
```

- [ ] **Step 4: Run test to verify it passes (green)**

Run: `go test ./internal/config/ -run TestAgent_StructFieldsExist -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/config/config.go internal/config/agents_setup_test.go
git commit -m "feat(config): extend Agent struct with M1 fields and reserved schema"
```

---

## Task 2: Make Config.Agents serializable + add Agent ID & PermissionMode constants

**Files:**
- Modify: `internal/config/config.go:590` (Agents json tag)
- Modify: `internal/config/config.go:59-62` (Agent ID constants)
- Modify: `internal/config/config.go` (append PermissionMode constants near other consts)

- [ ] **Step 1: Write a failing test for the new constants and json tag**

Append to `internal/config/agents_setup_test.go`:

```go
// TestConfig_AgentIDConstants locks the M1-01 Agent ID constants.
func TestConfig_AgentIDConstants(t *testing.T) {
	assert.Equal(t, "coder", AgentCoder)
	assert.Equal(t, "task", AgentTask)
	assert.Equal(t, "general-purpose", AgentGeneralPurpose)
	assert.Equal(t, "explore", AgentExplore)
	assert.Equal(t, "plan", AgentPlan)
}

// TestConfig_PermissionModeConstants locks the M1-01 permission-mode constants.
func TestConfig_PermissionModeConstants(t *testing.T) {
	assert.Equal(t, "default", PermissionModeDefault)
	assert.Equal(t, "acceptEdits", PermissionModeAcceptEdits)
	assert.Equal(t, "plan", PermissionModePlan)
	assert.Equal(t, "bypassPermissions", PermissionModeBypassPermissions)
}
```

- [ ] **Step 2: Run tests to verify they fail (red)**

Run: `go test ./internal/config/ -run 'TestConfig_AgentIDConstants|TestConfig_PermissionModeConstants' -v`
Expected: FAIL — `undefined: AgentGeneralPurpose` etc.

- [ ] **Step 3: Add the constants (minimal impl)**

In `internal/config/config.go`, replace the Agent ID const block (lines ~59-62):

```go
const (
	AgentCoder          string = "coder"
	AgentTask           string = "task"            // kept for backward compatibility
	AgentGeneralPurpose string = "general-purpose" // M1 new
	AgentExplore        string = "explore"         // M1 new
	AgentPlan           string = "plan"            // M1 new
)
```

Add a new const block immediately after the `SelectedModelType` consts (after line ~57, before the Agent ID consts, OR right after the Agent ID consts — keep it grouped):

```go
// Permission modes usable by an Agent (PermissionMode field).
const (
	PermissionModeDefault           = "default"
	PermissionModeAcceptEdits       = "acceptEdits"
	PermissionModePlan              = "plan"
	PermissionModeBypassPermissions = "bypassPermissions"
)
```

Then change the `Agents` json tag at line ~590:

```go
	Agents map[string]Agent `json:"agents,omitempty"`
```

(Rationale for `,omitempty`: when `Agents` is nil/empty the key is omitted, so old config files without `agents` and zero-value `Config{}` marshal identically to before — satisfies acceptance criterion 7.)

- [ ] **Step 4: Run tests to verify they pass (green)**

Run: `go test ./internal/config/ -run 'TestConfig_AgentIDConstants|TestConfig_PermissionModeConstants' -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/config/config.go internal/config/agents_setup_test.go
git commit -m "feat(config): serialize Agents and add Agent ID + PermissionMode constants"
```

---

## Task 3: Write failing SetupAgents behavior tests (red, before rewriting SetupAgents)

**Files:**
- Test: `internal/config/agents_setup_test.go` (append)

These tests assert the NEW behavior. They will fail against the current SetupAgents (which only registers coder+task).

- [ ] **Step 1: Append the failing behavior tests**

```go
// TestSetupAgents_BuiltinsAlwaysPresent — criterion 4: the four built-in
// agents are always present after SetupAgents().
func TestSetupAgents_BuiltinsAlwaysPresent(t *testing.T) {
	cfg := &Config{Options: &Options{}}
	cfg.SetupAgents()
	assert.Contains(t, cfg.Agents, AgentCoder)
	assert.Contains(t, cfg.Agents, AgentGeneralPurpose)
	assert.Contains(t, cfg.Agents, AgentExplore)
	assert.Contains(t, cfg.Agents, AgentPlan)
}

// TestSetupAgents_UserOverrideBuiltin — criterion 2: user-supplied non-zero
// fields override the builtin; zero fields keep the builtin default.
func TestSetupAgents_UserOverrideBuiltin(t *testing.T) {
	cfg := &Config{
		Options: &Options{},
		Agents: map[string]Agent{
			AgentGeneralPurpose: {
				AllowedTools: []string{"view", "grep"},
			},
		},
	}
	cfg.SetupAgents()
	gp := cfg.Agents[AgentGeneralPurpose]
	assert.Equal(t, []string{"view", "grep"}, gp.AllowedTools)
	// Untouched fields keep builtin defaults.
	assert.Equal(t, SelectedModelTypeLarge, gp.Model)
	assert.Equal(t, "General Purpose", gp.Name)
}

// TestSetupAgents_UserAddsCustomAgent — criterion 3: a new user key becomes a
// custom agent in c.Agents.
func TestSetupAgents_UserAddsCustomAgent(t *testing.T) {
	cfg := &Config{
		Options: &Options{},
		Agents: map[string]Agent{
			"code-reviewer": {
				Name:         "Code Reviewer",
				Model:        SelectedModelTypeSmall,
				AllowedTools: []string{"view", "grep", "glob"},
			},
		},
	}
	cfg.SetupAgents()
	assert.Contains(t, cfg.Agents, "code-reviewer")
	assert.Equal(t, "Code Reviewer", cfg.Agents["code-reviewer"].Name)
}

// TestSetupAgents_DisabledBuiltinExcludedFromCandidates — criterion 5: a
// builtin with Disabled=true is dropped from the candidate map.
func TestSetupAgents_DisabledBuiltinExcludedFromCandidates(t *testing.T) {
	cfg := &Config{
		Options: &Options{},
		Agents: map[string]Agent{
			AgentExplore: {Disabled: true},
		},
	}
	cfg.SetupAgents()
	_, present := cfg.Agents[AgentExplore]
	assert.False(t, present, "disabled builtin must not appear in c.Agents")
}

// TestSetupAgents_AgentsSerializeRoundTrip — criterion 1: agents map survives
// a marshal/unmarshal round trip.
func TestSetupAgents_AgentsSerializeRoundTrip(t *testing.T) {
	cfg := &Config{
		Options: &Options{},
		Agents: map[string]Agent{
			"code-reviewer": {
				Name:            "Reviewer",
				Model:           SelectedModelTypeSmall,
				AllowedTools:    []string{"view"},
				DisallowedTools: []string{"bash"},
				SystemPrompt:    "review carefully",
				PermissionMode:  PermissionModeAcceptEdits,
			},
		},
	}
	cfg.SetupAgents()

	data, err := json.Marshal(cfg)
	require.NoError(t, err)

	var round Config
	require.NoError(t, json.Unmarshal(data, &round))
	require.Contains(t, round.Agents, "code-reviewer")
	got := round.Agents["code-reviewer"]
	assert.Equal(t, "Reviewer", got.Name)
	assert.Equal(t, []string{"bash"}, got.DisallowedTools)
	assert.Equal(t, "review carefully", got.SystemPrompt)
	assert.Equal(t, PermissionModeAcceptEdits, got.PermissionMode)
}

// TestSetupAgents_OldConfigWithoutAgentsFieldDoesNotCrash — criterion 7: a
// config JSON with no "agents" key unmarshals cleanly and yields only the
// builtins after SetupAgents().
func TestSetupAgents_OldConfigWithoutAgentsFieldDoesNotCrash(t *testing.T) {
	oldJSON := `{"options":{}}`
	var cfg Config
	require.NoError(t, json.Unmarshal([]byte(oldJSON), &cfg))
	cfg.Options = &Options{}
	cfg.SetupAgents()
	assert.Contains(t, cfg.Agents, AgentCoder)
	assert.Contains(t, cfg.Agents, AgentGeneralPurpose)
	assert.Contains(t, cfg.Agents, AgentExplore)
	assert.Contains(t, cfg.Agents, AgentPlan)
}
```

Add `"encoding/json"` to the test file's imports and `"github.com/stretchr/testify/require"`.

- [ ] **Step 2: Run tests to verify they fail (red)**

Run: `go test ./internal/config/ -run TestSetupAgents_ -v`
Expected: FAIL — builtins general-purpose/explore/plan absent; override/custom/disabled/round-trip tests fail because SetupAgents still overwrites `c.Agents` with only coder+task.

---

## Task 4: Rewrite SetupAgents() (green)

**Files:**
- Modify: `internal/config/config.go:711-736`

- [ ] **Step 1: Replace SetupAgents with the new implementation**

> NOTE: This is the **reading (B)** version. If team-lead chose (A), delete the `AgentTask:` block from the map.

Replace lines 711-736 with:

```go
func (c *Config) SetupAgents() {
	allowedTools := resolveAllowedTools(allToolNames(), c.Options.DisabledTools)

	// 1. Built-in default agents.
	builtins := map[string]Agent{
		AgentCoder: {
			ID:           AgentCoder,
			Name:         "Coder",
			Description:  "An agent that helps with executing coding tasks.",
			Model:        SelectedModelTypeLarge,
			ContextPaths: c.Options.ContextPaths,
			AllowedTools: allowedTools,
			// coder has no DisallowedTools — it is the main agent with full tool access.
		},
		AgentGeneralPurpose: {
			ID:              AgentGeneralPurpose,
			Name:            "General Purpose",
			Description:     "General-purpose sub-agent for searching code, analyzing files, and running multi-step tasks.",
			Model:           SelectedModelTypeLarge,
			ContextPaths:    c.Options.ContextPaths,
			AllowedTools:    []string{"view", "grep", "glob", "ls", "bash", "write"},
			DisallowedTools: []string{"agent", "ask_user_questions", "job_output", "job_kill", "todos", "crush_info", "crush_logs"},
			AllowedMCP:      map[string][]string{}, // empty map = no MCPs
		},
		AgentExplore: {
			ID:              AgentExplore,
			Name:            "Explore",
			Description:     "Fast search agent for file discovery and code exploration. Read-only.",
			Model:           SelectedModelTypeSmall,
			ContextPaths:    c.Options.ContextPaths,
			AllowedTools:    []string{"view", "grep", "glob", "ls", "sourcegraph"},
			DisallowedTools: []string{"agent", "ask_user_questions", "job_output", "job_kill", "todos", "crush_info", "crush_logs", "bash", "write", "edit", "multiedit", "download"},
			PermissionMode:  PermissionModePlan,
			AllowedMCP:      map[string][]string{},
		},
		AgentPlan: {
			ID:              AgentPlan,
			Name:            "Plan",
			Description:     "Planning agent for architecture analysis and implementation design. Read-only.",
			Model:           SelectedModelTypeSmall,
			ContextPaths:    c.Options.ContextPaths,
			AllowedTools:    []string{"view", "grep", "glob", "ls"},
			DisallowedTools: []string{"agent", "ask_user_questions", "job_output", "job_kill", "todos", "crush_info", "crush_logs", "bash", "write", "edit", "multiedit", "download", "sourcegraph", "fetch", "agentic_fetch"},
			PermissionMode:  PermissionModePlan,
			AllowedMCP:      map[string][]string{},
		},
		// AgentTask is kept registered as a backward-compatibility alias so the
		// existing `agent` tool (internal/agent/agent_tool.go) keeps working
		// until M1-02 reworks the agent-coordination layer.
		AgentTask: {
			ID:           AgentTask,
			Name:         "Task",
			Description:  "An agent that helps with searching for context and finding implementation details.",
			Model:        SelectedModelTypeLarge,
			ContextPaths: c.Options.ContextPaths,
			AllowedTools: resolveReadOnlyTools(allowedTools),
			AllowedMCP:   map[string][]string{},
		},
	}

	// 2. Merge user-supplied agents from c.Agents (populated from "agents" in
	// crush.json during Load). For built-in keys, non-zero user fields override
	// the builtin defaults; zero fields are preserved. New keys become custom
	// agents. Disabled built-ins are dropped entirely.
	for key, userAgent := range c.Agents {
		if existing, ok := builtins[key]; ok {
			if userAgent.Name != "" {
				existing.Name = userAgent.Name
			}
			if userAgent.Description != "" {
				existing.Description = userAgent.Description
			}
			if userAgent.Model != "" {
				existing.Model = userAgent.Model
			}
			if userAgent.AllowedTools != nil {
				existing.AllowedTools = userAgent.AllowedTools
			}
			if userAgent.DisallowedTools != nil {
				existing.DisallowedTools = userAgent.DisallowedTools
			}
			if userAgent.SystemPrompt != "" {
				existing.SystemPrompt = userAgent.SystemPrompt
			}
			if userAgent.PermissionMode != "" {
				existing.PermissionMode = userAgent.PermissionMode
			}
			if userAgent.AllowedMCP != nil {
				existing.AllowedMCP = userAgent.AllowedMCP
			}
			if userAgent.ContextPaths != nil {
				existing.ContextPaths = userAgent.ContextPaths
			}
			if userAgent.Disabled {
				delete(builtins, key)
				continue
			}
			builtins[key] = existing
		} else {
			if userAgent.Disabled {
				continue
			}
			builtins[key] = userAgent
		}
	}

	c.Agents = builtins
}
```

- [ ] **Step 2: Run the SetupAgents tests to verify they pass (green)**

Run: `go test ./internal/config/ -run TestSetupAgents_ -v`
Expected: all PASS.

- [ ] **Step 3: Commit**

```bash
git add internal/config/config.go internal/config/agents_setup_test.go
git commit -m "feat(config): register four built-in agents with user overrides"
```

---

## Task 5: Update existing in-scope tests to the new builtin set

**Files:**
- Modify: `internal/config/agent_id_test.go`
- Modify: `internal/config/load_test.go:677-737`

> Under reading (B) these tests still pass as-is for `AgentTask`; we ADD assertions for the new builtins rather than removing the `task` checks. Under reading (A), remove the `task` subtests/blocks.

- [ ] **Step 1: Extend agent_id_test.go to cover all builtins**

Rewrite `internal/config/agent_id_test.go` so it asserts presence + correct ID for every builtin:

```go
package config

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestConfig_AgentIDs(t *testing.T) {
	cfg := &Config{
		Options: &Options{
			DisabledTools: []string{},
		},
	}
	cfg.SetupAgents()

	builtins := []string{AgentCoder, AgentTask, AgentGeneralPurpose, AgentExplore, AgentPlan}
	for _, id := range builtins {
		t.Run(id+" agent should have correct ID", func(t *testing.T) {
			a, ok := cfg.Agents[id]
			require.True(t, ok, "expected builtin agent %q to be registered", id)
			assert.Equal(t, id, a.ID, "agent %q ID mismatch", id)
		})
	}
}
```

- [ ] **Step 2: Update the three SetupAgents tests in load_test.go**

The three tests (`TestConfig_setupAgentsWithNoDisabledTools`, `TestConfig_setupAgentsWithDisabledTools`, `TestConfig_setupAgentsWithEveryReadOnlyToolDisabled`) currently assert coder's full allowed-tools list and task's read-only tools. Those assertions stay valid for the `coder` and `task` builtins under reading (B). ADD a presence check for the new builtins at the end of each test so they also lock criterion 4. Append this once-per-test block (after the existing `taskAgent` assertions):

```go
	// M1-01: the three new built-ins are also registered.
	assert.Contains(t, cfg.Agents, AgentGeneralPurpose)
	assert.Contains(t, cfg.Agents, AgentExplore)
	assert.Contains(t, cfg.Agents, AgentPlan)
```

(Do NOT change the existing coder/task `assert.Equal` lines — they remain correct under reading (B).)

- [ ] **Step 3: Run the full config test suite**

Run: `go test ./internal/config/ -v`
Expected: all PASS.

- [ ] **Step 4: Commit**

```bash
git add internal/config/agent_id_test.go internal/config/load_test.go
git commit -m "test(config): cover new built-in agents in setup tests"
```

---

## Task 6: Verification & self-review

- [ ] **Step 1: Run the entire config package test suite verbosely**

Run: `go test ./internal/config/ -v`
Expected: PASS — every test, including the new SetupAgents/struct/round-trip tests and the updated load/agent_id tests. Paste the full output as evidence.

- [ ] **Step 2: Run go vet**

Run: `go vet ./internal/config/`
Expected: no diagnostics.

- [ ] **Step 3: Build the whole module to confirm no compile breakage outside config**

Run: `go build ./...`
Expected: success. (Confirms `internal/agent/agent_tool.go` still compiles — its `Agents[config.AgentTask]` read is unchanged and `task` is still registered under reading B.)

- [ ] **Step 4: Self-review checklist**

- Scope: only `internal/config/config.go` + config `*_test.go` touched? YES.
- All 5 spec changes implemented (json tag, struct fields, ID consts, SetupAgents rewrite, PermissionMode consts)? YES.
- Acceptance criteria 1-7 each covered by a green test? (1=round-trip, 2=override, 3=custom, 4=builtins-present, 5=disabled-excluded, 6=full suite green, 7=old-config-no-crash) YES.
- YAGNI: M2-M4 fields defined but unused — correct, schema-only per spec. No speculative code added.
- No weakened assertions: existing `assert.Equal` lines on coder/task kept intact.

- [ ] **Step 5: Final commit if any cleanup**

Only if steps 1-3 surfaced fixups. Otherwise skip.

---

## Acceptance-criteria → test mapping (self-review aid)

| Criterion | Test |
|---|---|
| 1. agents serialize/deserialize | `TestSetupAgents_AgentsSerializeRoundTrip` |
| 2. user override of builtin AllowedTools | `TestSetupAgents_UserOverrideBuiltin` |
| 3. user custom agent appears | `TestSetupAgents_UserAddsCustomAgent` |
| 4. four builtins always present | `TestSetupAgents_BuiltinsAlwaysPresent` + load_test additions |
| 5. Disabled builtin excluded | `TestSetupAgents_DisabledBuiltinExcludedFromCandidates` |
| 6. golden/existing tests pass | full `go test ./internal/config/` green |
| 7. old config without agents doesn't crash | `TestSetupAgents_OldConfigWithoutAgentsFieldDoesNotCrash` |
