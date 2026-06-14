
package team

import (
	"testing"

	"charm.land/fantasy"
	"github.com/charmbracelet/crush/internal/agent"
	"github.com/stretchr/testify/assert"
)

// TestDelegateResult_StatusFromTurnRunResult locks the DelegateResult shape:
// the Status field carries a TurnStatus (completed/canceled/failed) and the
// Result carries the *fantasy.AgentResult the runner produced. This is the
// contract the delegate runner writes into Results[idx] under the group mutex.
func TestDelegateResult_StatusFromTurnRunResult(t *testing.T) {
	ar := &fantasy.AgentResult{
		Response:   fantasy.Response{Content: fantasy.ResponseContent{fantasy.TextContent{Text: "done"}}},
		TotalUsage: fantasy.Usage{InputTokens: 100, OutputTokens: 50},
	}
	r := DelegateResult{
		TaskID:     "t1",
		Status:     agent.TurnCompleted,
		Result:     ar,
		DurationMs: 42,
	}
	assert.Equal(t, agent.TurnCompleted, r.Status)
	assert.Equal(t, int64(150), r.Result.TotalUsage.InputTokens+r.Result.TotalUsage.OutputTokens)
}

// TestDelegateRunGroup_Counts locks acceptance #4's Results-fill semantics and
// the count helpers. A freshly allocated group with N tasks has N "running"
// slots (Status == "" is the zero TurnStatus); as slots are filled with
// terminal statuses the DoneCount/FailedCount/RunningCount reflect them.
func TestDelegateRunGroup_Counts(t *testing.T) {
	g := &DelegateRunGroup{
		ID:      "g1",
		Tasks:   []DelegateTask{{ID: "1"}, {ID: "2"}, {ID: "3"}},
		Results: make([]DelegateResult, 3),
		Status:  "running",
	}

	// All three slots zero-valued => all running.
	assert.Equal(t, 3, g.RunningCount())
	assert.Equal(t, 0, g.DoneCount())
	assert.Equal(t, 0, g.FailedCount())

	// Fill slot 0 as completed.
	g.Results[0] = DelegateResult{TaskID: "1", Status: agent.TurnCompleted}
	assert.Equal(t, 2, g.RunningCount())
	assert.Equal(t, 1, g.DoneCount())
	assert.Equal(t, 0, g.FailedCount())

	// Fill slot 1 as failed.
	g.Results[1] = DelegateResult{TaskID: "2", Status: agent.TurnFailed, Error: "boom"}
	assert.Equal(t, 1, g.RunningCount())
	assert.Equal(t, 1, g.DoneCount())
	assert.Equal(t, 1, g.FailedCount())

	// Fill slot 2 as canceled (counts as failed-category).
	g.Results[2] = DelegateResult{TaskID: "3", Status: agent.TurnCanceled}
	assert.Equal(t, 0, g.RunningCount())
	assert.Equal(t, 1, g.DoneCount())
	assert.Equal(t, 2, g.FailedCount())
}

// TestDelegateRunGroup_TotalTokens locks the token aggregation across filled
// results. Slots whose Result is nil contribute zero.
func TestDelegateRunGroup_TotalTokens(t *testing.T) {
	g := &DelegateRunGroup{
		ID:    "g1",
		Tasks: []DelegateTask{{ID: "1"}, {ID: "2"}, {ID: "3"}},
		Results: []DelegateResult{
			{TaskID: "1", Status: agent.TurnCompleted, Result: &fantasy.AgentResult{
				TotalUsage: fantasy.Usage{InputTokens: 100, OutputTokens: 50},
			}},
			{TaskID: "2", Status: agent.TurnFailed, Error: "boom"}, // Result nil
			{TaskID: "3", Status: agent.TurnCompleted, Result: &fantasy.AgentResult{
				TotalUsage: fantasy.Usage{InputTokens: 200, OutputTokens: 75},
			}},
		},
		Status: "done",
	}
	// 150 + 0 + 275 = 425
	assert.Equal(t, int64(425), g.TotalTokens())
}

// TestReadOnlyDelegatePolicy_FromAgentPackage locks that the team-package
// delegate runner consumes the EXPORTED agent.ReadOnlyDelegatePolicy (the M2-03
// consolidation: M2-01's in-package delegateReadOnlyPolicy() was deleted and
// both call sites re-pointed at agent.ReadOnlyDelegatePolicy). The shape is
// asserted in full at the agent level (team_call_policy_test.go); this test
// exists to lock the dependency direction — team must source the policy from
// agent, not define its own copy.
func TestReadOnlyDelegatePolicy_FromAgentPackage(t *testing.T) {
	p := agent.ReadOnlyDelegatePolicy()

	assert.Equal(t, "default", p.PermissionMode)
	assert.ElementsMatch(t,
		[]string{"view", "grep", "glob", "ls", "sourcegraph"},
		p.AllowedTools)

	// Destructive tools must be in the disallow list (exact match not required,
	// but the load-bearing ones must be present).
	for _, banned := range []string{"bash", "write", "edit", "agent"} {
		assert.Contains(t, p.DisallowedTools, banned)
	}
}
