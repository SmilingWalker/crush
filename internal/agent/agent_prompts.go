package agent

import _ "embed"

// GeneralPurposeTemplate is the system prompt for the general-purpose
// sub-agent: an autonomous worker with read/write/bash tool access. It is the
// default system_prompt baseline for the built-in "general-purpose" agent
// registered in internal/config (M1-01); a user-supplied system_prompt in
// crush.json takes precedence over this value.
//
//go:embed templates/general_purpose.md
var GeneralPurposeTemplate string

// ExploreTemplate is the system prompt for the explore sub-agent: a
// read-only file-search specialist.
//
//go:embed templates/explore.md
var ExploreTemplate string

// PlanTemplate is the system prompt for the plan sub-agent: a read-only
// software-architecture / planning specialist.
//
//go:embed templates/plan.md
var PlanTemplate string

// DefaultTemplates returns the built-in agent default template map keyed by
// agent name. The returned map is freshly allocated on each call so callers
// may mutate it without affecting subsequent calls or package state. The keys
// match the built-in agent names (general-purpose / explore / plan) registered
// in internal/config (M1-01).
func DefaultTemplates() map[string]string {
	return map[string]string{
		"general-purpose": GeneralPurposeTemplate,
		"explore":         ExploreTemplate,
		"plan":            PlanTemplate,
	}
}
