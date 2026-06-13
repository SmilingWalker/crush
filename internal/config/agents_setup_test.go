package config

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
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
// config JSON with no "agents" key unmarshals cleanly and yields the builtins
// after SetupAgents().
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
