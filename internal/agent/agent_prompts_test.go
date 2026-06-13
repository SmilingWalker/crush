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
