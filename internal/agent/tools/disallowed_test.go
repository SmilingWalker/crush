package tools

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// TestSubAgentDisallowedTools_ContainsExpected locks the M1-02 global
// sub-agent denylist: exactly these 7 tool names are always forbidden for
// sub-agents, and common safe tools are not.
func TestSubAgentDisallowedTools_ContainsExpected(t *testing.T) {
	expected := []string{
		"agent",
		"ask_user_questions",
		"job_output",
		"job_kill",
		"todos",
		"crush_info",
		"crush_logs",
	}
	for _, name := range expected {
		assert.True(t, IsSubAgentDisallowed(name), "tool %q should be disallowed for sub-agents", name)
	}

	// Common safe tools must NOT be on the denylist.
	assert.False(t, IsSubAgentDisallowed("view"))
	assert.False(t, IsSubAgentDisallowed("grep"))
	assert.False(t, IsSubAgentDisallowed("glob"))
	assert.False(t, IsSubAgentDisallowed("bash"))
	assert.False(t, IsSubAgentDisallowed("write"))
	assert.False(t, IsSubAgentDisallowed("ls"))
}

// TestIsSubAgentDisallowed_EmptyAndUnknown covers the negative paths so the
// predicate does not panic or mis-classify on edge inputs.
func TestIsSubAgentDisallowed_EmptyAndUnknown(t *testing.T) {
	assert.False(t, IsSubAgentDisallowed(""))
	assert.False(t, IsSubAgentDisallowed("definitely-not-a-real-tool"))
}
