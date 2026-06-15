package config

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// TestOptions_IsAgentTeamEnabled locks M3-08 acceptance #3 (nil-safe gate):
// the method returns false when Options is nil, Experimental is nil, or
// AgentTeam is false, and true ONLY when Experimental.AgentTeam is explicitly
// true. Old config files without an experimental block are safe.
func TestOptions_IsAgentTeamEnabled(t *testing.T) {
	// nil receiver -- safe, returns false
	var nilOpts *Options
	assert.False(t, nilOpts.IsAgentTeamEnabled(), "nil Options")

	// nil Experimental -- safe, returns false
	assert.False(t, (&Options{}).IsAgentTeamEnabled(), "nil Experimental")

	// AgentTeam explicitly false
	assert.False(t, (&Options{
		Experimental: &ExperimentalOptions{AgentTeam: false},
	}).IsAgentTeamEnabled(), "AgentTeam=false")

	// AgentTeam explicitly true
	assert.True(t, (&Options{
		Experimental: &ExperimentalOptions{AgentTeam: true},
	}).IsAgentTeamEnabled(), "AgentTeam=true")

	// AgentTeamPreview true but AgentTeam false -- writes still disabled
	assert.False(t, (&Options{
		Experimental: &ExperimentalOptions{AgentTeamPreview: true, AgentTeam: false},
	}).IsAgentTeamEnabled(), "AgentTeamPreview without AgentTeam")
}
