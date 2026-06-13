package agent

import (
	"context"
	"testing"

	"charm.land/fantasy"
	"github.com/charmbracelet/crush/internal/agent/tools"
	"github.com/charmbracelet/crush/internal/config"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// extractToolNames returns the Info().Name values of a tool slice.
func extractToolNames(ts []fantasy.AgentTool) []string {
	names := make([]string, 0, len(ts))
	for _, t := range ts {
		names = append(names, t.Info().Name)
	}
	return names
}

// TestBuildTools_SubAgentFiltersDisallowed locks the M1-02 two-layer filter:
// for isSubAgent=true the global denylist ∪ agent.DisallowedTools are removed
// from the whitelist result.
func TestBuildTools_SubAgentFiltersDisallowed(t *testing.T) {
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
	}

	// AllowedTools deliberately includes denylisted names so we can prove the
	// second layer strips them. DisallowedTools adds a per-agent ban on bash.
	subAgent := config.Agent{
		Name: "m1-02-probe",
		AllowedTools: []string{
			tools.ViewToolName,
			tools.GrepToolName,
			tools.BashToolName,
			AgentToolName,                  // global denylist
			tools.AskUserQuestionsToolName, // global denylist
			tools.TodosToolName,            // global denylist
		},
		DisallowedTools: []string{tools.BashToolName}, // per-agent ban
	}

	ctx := t.Context()
	got, err := coord.buildTools(ctx, subAgent, true /* isSubAgent */)
	require.NoError(t, err)
	names := extractToolNames(got)

	assert.Contains(t, names, tools.ViewToolName)
	assert.Contains(t, names, tools.GrepToolName)

	// Per-agent DisallowedTools strips bash.
	assert.NotContains(t, names, tools.BashToolName)
	// Global denylist strips these three.
	assert.NotContains(t, names, AgentToolName)
	assert.NotContains(t, names, tools.AskUserQuestionsToolName)
	assert.NotContains(t, names, tools.TodosToolName)
}

// TestBuildTools_CoderNotFiltered locks criterion 4: for isSubAgent=false the
// denylist + DisallowedTools layers are NOT applied (only the whitelist).
func TestBuildTools_CoderNotFiltered(t *testing.T) {
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
	}

	// A main-agent style config: denylisted names ARE present, DisallowedTools
	// lists a tool that is on the whitelist. isSubAgent=false must keep them.
	coder := config.Agent{
		Name: "coder-probe",
		AllowedTools: []string{
			tools.ViewToolName,
			tools.TodosToolName,            // globally denylisted for sub-agents
			tools.AskUserQuestionsToolName, // globally denylisted for sub-agents
			tools.BashToolName,
		},
		DisallowedTools: []string{tools.BashToolName},
	}

	ctx := t.Context()
	got, err := coord.buildTools(ctx, coder, false /* isSubAgent */)
	require.NoError(t, err)
	names := extractToolNames(got)

	// All whitelist members survive — sub-agent layers skipped for main agent.
	assert.Contains(t, names, tools.TodosToolName)
	assert.Contains(t, names, tools.AskUserQuestionsToolName)
	assert.Contains(t, names, tools.BashToolName) // DisallowedTools ignored for main agent
	assert.Contains(t, names, tools.ViewToolName)
}

// TestRecursionDepth_RoundTrip locks the context plumbing helpers: depth
// defaults to 0 when unset, and round-trips through withRecursionDepth.
func TestRecursionDepth_RoundTrip(t *testing.T) {
	ctx := context.Background()
	assert.Equal(t, 0, getRecursionDepth(ctx))

	ctx = withRecursionDepth(ctx, 2)
	assert.Equal(t, 2, getRecursionDepth(ctx))

	// Overwriting with 0 is a real value, not "unset".
	ctx = withRecursionDepth(ctx, 0)
	assert.Equal(t, 0, getRecursionDepth(ctx))
}

// TestMaxRecursionDepthConstant locks criterion 1's limit value.
func TestMaxRecursionDepthConstant(t *testing.T) {
	assert.Equal(t, 3, maxRecursionDepth)
}
