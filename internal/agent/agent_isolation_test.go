package agent

import (
	"context"
	"reflect"
	"testing"

	"charm.land/fantasy"
	"github.com/charmbracelet/crush/internal/agent/tools"
	"github.com/charmbracelet/crush/internal/config"
	"github.com/stretchr/testify/require"
)

// TestSessionAgentInstancesAreIsolated locks the invariant that each
// NewSessionAgent call yields an independent instance: mutating one instance's
// tools / models / messageQueue / activeRequests must NOT leak into another.
//
// This is the M1 fatal-risk gate (see
// docs/superpowers/specs/2026-06-14-m0-5-sessionagent-isolation-spike-design.md).
// It is a regression test for an invariant that holds by construction today
// (NewSessionAgent allocates fresh csync containers per instance); a failure
// here means the isolation assumption is broken and M1 must not proceed.
func TestSessionAgentInstancesAreIsolated(t *testing.T) {
	a1 := NewSessionAgent(SessionAgentOptions{}).(*sessionAgent)
	a2 := NewSessionAgent(SessionAgentOptions{}).(*sessionAgent)

	// Two distinct pointers — truly separate instances.
	require.NotSame(t, a1, a2, "NewSessionAgent must return distinct instances")

	// Sentinel models distinguishable via ModelCfg.Provider; no real LLM needed.
	largeA := Model{ModelCfg: config.SelectedModel{Provider: "iso-large-A"}}
	smallA := Model{ModelCfg: config.SelectedModel{Provider: "iso-small-A"}}

	// Mutate ONLY a1 across all four container kinds.
	a1.SetModels(largeA, smallA)
	a1.SetTools([]fantasy.AgentTool{tools.NewJobOutputTool()})
	a1.messageQueue.Set("sess-x", []SessionAgentCall{{Prompt: "probe"}})
	a1.activeRequests.Set("sess-x", context.CancelFunc(func() {}))

	// Confirm the mutation landed on a1 (sanity, so a silent no-op is caught).
	require.Equal(t, 1, a1.tools.Len(), "a1 tools should hold the one tool we set")
	require.Equal(t, "iso-large-A", a1.Model().ModelCfg.Provider)

	// a2 must be completely unaffected across all four containers.
	require.Equal(t, 0, a2.tools.Len(), "a1.SetTools leaked into a2's tools")
	require.Equal(t, "", a2.Model().ModelCfg.Provider, "a1.SetModels leaked into a2's large model")
	require.Equal(t, "", a2.smallModel.Get().ModelCfg.Provider, "a1.SetModels leaked into a2's small model")

	_, mqOK := a2.messageQueue.Get("sess-x")
	require.False(t, mqOK, "a1.messageQueue.Set leaked into a2's message queue")

	_, arOK := a2.activeRequests.Get("sess-x")
	require.False(t, arOK, "a1.activeRequests.Set leaked into a2's active requests")
}

// sameTool reports whether two fantasy.AgentTool interface values reference the
// same underlying object. It compares pointer addresses for pointer-kind
// dynamic types; for any non-pointer kind it returns false (a freshly-allocated
// value type is not aliased mutable state in the sense M1 must guard against).
// It never panics, regardless of comparability.
func sameTool(a, b fantasy.AgentTool) bool {
	va, vb := reflect.ValueOf(a), reflect.ValueOf(b)
	if va.Kind() != reflect.Pointer || vb.Kind() != reflect.Pointer {
		return false
	}
	return va.Pointer() == vb.Pointer()
}

// toolsByName indexes a tool slice by tool name for cross-call comparison.
func toolsByName(ts []fantasy.AgentTool) map[string]fantasy.AgentTool {
	m := make(map[string]fantasy.AgentTool, len(ts))
	for _, t := range ts {
		m[t.Info().Name] = t
	}
	return m
}

// TestBuildToolsReturnsDistinctObjects probes whether two consecutive
// buildTools calls on the same coordinator hand back the SAME tool object
// pointers (aliasing) or distinct ones.
//
// Why this matters for M1: even though Test 1 proves each SessionAgent's tool
// SLICE is independent (SetTools copies the slice), the slice ELEMENTS are
// interface values holding pointers — copying the slice copies the pointer, so
// two agents could still point at the same underlying tool object. If that
// object carries per-agent mutable state, agents would interfere.
//
// Approach: construct a coordinator rich enough to run buildTools (via the
// existing testEnv fixtures + config.Init), call buildTools twice with a
// sub-agent config, and assert every same-named tool is a distinct object.
// "agent" and "agentic_fetch" are intentionally excluded from AllowedTools so
// buildTools does not invoke c.agentTool / c.agenticFetchTool (which need more
// setup); this matches real sub-agents. isSubAgent=true makes
// wrapToolsWithHooks short-circuit, so a nil hookRunner is never dereferenced.
//
// Prediction (from reading buildTools: each tools.New*() mints a fresh object,
// no memoization for non-"agent" tools): all distinct -> green. A failure here
// is the spike's payoff: it surfaces aliasing M1 must handle by rebuilding
// tools per-runner (the net M1 contract regardless of outcome).
func TestBuildToolsReturnsDistinctObjects(t *testing.T) {
	env := testEnv(t)

	cfg, err := config.Init(env.workingDir, "", false)
	require.NoError(t, err)

	coord := &coordinator{
		cfg:         cfg,
		sessions:    env.sessions,
		messages:    env.messages,
		permissions: env.permissions,
		questions:   env.questions,
		history:     env.history,
		filetracker: *env.filetracker,
		// lspManager, allSkills, activeSkills, skillTracker left nil —
		// tool constructors tolerate nil (proven by coderAgent in common_test.go).
	}

	// A representative sub-agent toolset. Deliberately excludes "agent" and
	// "agentic_fetch" (see comment above). NOTE: this AllowedTools list is a
	// snapshot — keep it in sync with the production sub-agent definition when
	// it lands (M1-04); drift would silently shrink aliasing coverage. The
	// probed-tool count is logged below so shrinkage is visible in -v output.
	subAgentCfg := config.Agent{
		Name: "iso-probe",
		AllowedTools: []string{
			tools.BashToolName,
			tools.EditToolName,
			tools.MultiEditToolName,
			tools.GlobToolName,
			tools.GrepToolName,
			tools.ViewToolName,
			tools.WriteToolName,
			tools.AskUserQuestionsToolName,
			tools.DownloadToolName,
			tools.FetchToolName,
			tools.TodosToolName,
			tools.CrushInfoToolName,
			tools.CrushLogsToolName,
			tools.JobOutputToolName,
			tools.JobKillToolName,
		},
	}

	ctx := t.Context()
	tools1, err := coord.buildTools(ctx, subAgentCfg, true)
	require.NoError(t, err)
	tools2, err := coord.buildTools(ctx, subAgentCfg, true)
	require.NoError(t, err)

	require.NotEmpty(t, tools1, "buildTools returned no tools — AllowedTools filtering left nothing to compare")

	first := toolsByName(tools1)
	second := toolsByName(tools2)
	t.Logf("probed %d distinct tool names for aliasing", len(first))

	// Alias table (logged for the gate artifact; visible with -v).
	t.Log("buildTools aliasing table (name -> same object across two calls?):")
	var shared []string
	for name, t1 := range first {
		t2, ok := second[name]
		require.True(t, ok, "tool %q present in first buildTools call but absent in second", name)
		aliased := sameTool(t1, t2)
		t.Logf("  %s: %v", name, aliased)
		if aliased {
			shared = append(shared, name)
		}
	}

	require.Empty(t, shared,
		"aliasing detected — these tools are the SAME object across two buildTools "+
			"calls: %v. See spec section 五; M1 AgentFactory must rebuild tools "+
			"per-runner regardless (the net contract), and any tool here that carries "+
			"per-agent mutable state must be documented before M1-04/M1-05.", shared)
}
