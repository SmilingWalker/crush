package agent

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestCoordinator_ActiveSubAgentsInitialized locks the invariant that every
// constructed coordinator has a usable (non-nil) activeSubAgents map, so
// runSubAgentAsync can register without a nil-map panic.
func TestCoordinator_ActiveSubAgentsInitialized(t *testing.T) {
	env := testEnv(t)
	const providerID = "test-provider"
	providerCfg := setupProviderConfig(providerID)
	coord := newTestCoordinator(t, env, providerID, providerCfg)

	require.NotNil(t, coord.activeSubAgents, "activeSubAgents must be initialized")
	// Writing the nil-map would panic; prove it is safe to write.
	coord.activeSubAgents["probe"] = &activeSubAgent{cancel: func() {}, sessionID: "s1"}
	assert.Contains(t, coord.activeSubAgents, "probe")
}
