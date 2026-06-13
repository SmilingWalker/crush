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
			require.True(t, ok, "expected built-in agent %q to be registered", id)
			assert.Equal(t, id, a.ID, "agent %q ID mismatch", id)
		})
	}
}
