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
