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
