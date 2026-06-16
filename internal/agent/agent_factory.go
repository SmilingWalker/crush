// agent_factory.go implements the production AgentFactory — M2-deferred seam.
// CoordinatorAgentFactory wraps the M1 coordinator to create real TurnRunner
// instances from AgentSpec parameters. Each BuildRunner call builds a fresh
// SessionAgent configured with the specified model, system prompt, permission
// mode, and tool policy, then wraps it in a TurnRunnerAdapter.
//
// This replaces the temporary SessionAgentFactory (turn_runner_adapter.go)
// which ignored AgentSpec and relied on a pre-configured SessionAgent provider
// closure. The production agent_factory.go uses coordinator infrastructure
// (model selection, tool building, system prompt templates) to create properly
// configured agents for long-lived team members.

package agent

import (
	"context"
	"fmt"
)

// CoordinatorAgentFactory implements AgentFactory using the M1 coordinator's
// agent-building infrastructure. It is the production replacement for the
// temporary SessionAgentFactory.
type CoordinatorAgentFactory struct {
	coordinator Coordinator
}

// NewCoordinatorAgentFactory creates a production AgentFactory backed by the
// given coordinator.
func NewCoordinatorAgentFactory(c Coordinator) *CoordinatorAgentFactory {
	return &CoordinatorAgentFactory{coordinator: c}
}

// BuildRunner creates a TurnRunner from the given AgentSpec by delegating to
// the coordinator's BuildSessionAgent method and wrapping the resulting
// SessionAgent in a TurnRunnerAdapter.
func (f *CoordinatorAgentFactory) BuildRunner(ctx context.Context, spec AgentSpec) (TurnRunner, error) {
	sa, err := f.coordinator.BuildSessionAgent(ctx, spec)
	if err != nil {
		return nil, fmt.Errorf("build session agent: %w", err)
	}
	return NewTurnRunnerFromSessionAgent(sa), nil
}
