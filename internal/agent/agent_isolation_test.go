package agent

import (
	"context"
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
