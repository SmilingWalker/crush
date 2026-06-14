package agent

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// TestReadOnlyDelegatePolicy_Shape locks acceptance #1 and #2 from the master
// task doc (03-m2-delegate-runner.md:427-428): the delegate read-only policy
// allows exactly the five read-only tools and disallows the destructive ones.
// The five allowlist entries are verified against real tool registrations
// (view.go:75, grep.go:89, glob.go:21, ls.go:52, sourcegraph.go:30).
func TestReadOnlyDelegatePolicy_Shape(t *testing.T) {
	p := ReadOnlyDelegatePolicy()

	assert.Equal(t, "default", p.PermissionMode)

	// Allowlist: exactly the five read-only tools (order-independent).
	assert.ElementsMatch(t,
		[]string{"view", "grep", "glob", "ls", "sourcegraph"},
		p.AllowedTools,
		"delegate allowlist must be exactly the five read-only tools")

	// Destructive tools must be in the disallow list. The load-bearing ones
	// (acceptance #2): bash/write/edit/agent must never be reachable.
	for _, banned := range []string{"bash", "write", "edit", "agent"} {
		assert.Contains(t, p.DisallowedTools, banned,
			"destructive tool %q must be in the disallow list", banned)
	}
}

// TestReadOnlyDelegatePolicy_AllowAndDisallowDisjoint locks a consistency
// invariant: no tool appears in both AllowedTools and DisallowedTools. A tool
// in both would be a contradiction (allowed by Layer 1 of the coordinator
// filter, then stripped by Layer 2 — confusing and a sign of a copy-paste bug).
func TestReadOnlyDelegatePolicy_AllowAndDisallowDisjoint(t *testing.T) {
	p := ReadOnlyDelegatePolicy()

	allowed := map[string]bool{}
	for _, a := range p.AllowedTools {
		allowed[a] = true
	}
	for _, d := range p.DisallowedTools {
		assert.False(t, allowed[d],
			"tool %q appears in BOTH AllowedTools and DisallowedTools — contradiction", d)
	}
}

// TestReadOnlyDelegatePolicy_NoSharedMutableState locks that the function is a
// pure literal: every call returns a fresh, independent value. A caller must
// not be able to mutate one delegate's policy and affect another's. This guards
// against a future "optimization" that returns a package-level var by reference.
func TestReadOnlyDelegatePolicy_NoSharedMutableState(t *testing.T) {
	p1 := ReadOnlyDelegatePolicy()
	p2 := ReadOnlyDelegatePolicy()

	// Mutate p1's allowlist.
	p1.AllowedTools[0] = "MUTATED"

	// p2 must be unaffected.
	assert.NotContains(t, p2.AllowedTools, "MUTATED",
		"ReadOnlyDelegatePolicy returned shared mutable state — calls are not independent")
	assert.Equal(t, "view", p2.AllowedTools[0],
		"mutating one policy's allowlist corrupted another call's result")
}
