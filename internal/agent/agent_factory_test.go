package agent

import (
	"context"
	"errors"
	"testing"

	"charm.land/fantasy"
	"github.com/charmbracelet/crush/internal/actor"
	"github.com/charmbracelet/crush/internal/message"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// stubBuildCoordinator implements Coordinator with a controllable BuildSessionAgent.
type stubBuildCoordinator struct {
	buildSessionAgentFn func(ctx context.Context, spec AgentSpec) (SessionAgent, error)

	// Fields that may be inspected by tests after calls.
	lastSpec AgentSpec
}

func (c *stubBuildCoordinator) Run(ctx context.Context, sessionID, prompt string, attachments ...message.Attachment) (*fantasy.AgentResult, error) {
	return nil, nil
}
func (c *stubBuildCoordinator) RunAccepted(ctx context.Context, accept *AcceptedRun, sessionID, prompt string, attachments ...message.Attachment) (*fantasy.AgentResult, error) {
	return nil, nil
}
func (c *stubBuildCoordinator) BeginAccepted(sessionID string) *AcceptedRun { return nil }
func (c *stubBuildCoordinator) GenerateTitle(context.Context, string, string) {}
func (c *stubBuildCoordinator) Cancel(string)                       {}
func (c *stubBuildCoordinator) CancelAll()                          {}
func (c *stubBuildCoordinator) IsSessionBusy(string) bool           { return false }
func (c *stubBuildCoordinator) IsBusy() bool                        { return false }
func (c *stubBuildCoordinator) QueuedPrompts(string) int            { return 0 }
func (c *stubBuildCoordinator) QueuedPromptsList(string) []string   { return nil }
func (c *stubBuildCoordinator) AppendTools([]fantasy.AgentTool) {}

func (c *stubBuildCoordinator) ClearQueue(string)                   {}
func (c *stubBuildCoordinator) Summarize(context.Context, string) error { return nil }
func (c *stubBuildCoordinator) Model() Model                        { return Model{} }
func (c *stubBuildCoordinator) UpdateModels(context.Context) error     { return nil }
func (c *stubBuildCoordinator) BuildSessionAgent(ctx context.Context, spec AgentSpec) (SessionAgent, error) {
	c.lastSpec = spec
	if c.buildSessionAgentFn != nil {
		return c.buildSessionAgentFn(ctx, spec)
	}
	return nil, errors.New("BuildSessionAgent not configured")
}

func TestCoordinatorAgentFactory_BuildRunner_Success(t *testing.T) {
	mockSA := &trackableSA{}
	mockSA.runFunc = func(ctx context.Context, call SessionAgentCall) (*fantasy.AgentResult, error) {
		return &fantasy.AgentResult{
			TotalUsage: fantasy.Usage{InputTokens: 100, OutputTokens: 50},
		}, nil
	}

	coord := &stubBuildCoordinator{
		buildSessionAgentFn: func(ctx context.Context, spec AgentSpec) (SessionAgent, error) {
			return mockSA, nil
		},
	}

	factory := &CoordinatorAgentFactory{coordinator: coord}
	runner, err := factory.BuildRunner(context.Background(), AgentSpec{
		AgentType: "general-purpose",
	})
	require.NoError(t, err)
	require.NotNil(t, runner)

	// The runner should be a TurnRunnerAdapter wrapping our mock session agent.
	result, err := runner.Run(context.Background(), TeamAgentCall{
		SessionID:      "sess-1",
		PromptEnvelope: "hello",
		Actor:          actor.ActorContext{SessionID: "sess-1", TeamID: "t1", MemberID: "m1"},
	})
	require.NoError(t, err)
	assert.Equal(t, TurnCompleted, result.Status)
	assert.Equal(t, int64(100), result.Result.TotalUsage.InputTokens)
}

func TestCoordinatorAgentFactory_BuildRunner_PassesSpec(t *testing.T) {
	mockSA := &trackableSA{}
	var capturedSpec AgentSpec

	coord := &stubBuildCoordinator{
		buildSessionAgentFn: func(ctx context.Context, spec AgentSpec) (SessionAgent, error) {
			capturedSpec = spec
			return mockSA, nil
		},
	}

	factory := &CoordinatorAgentFactory{coordinator: coord}
	_, err := factory.BuildRunner(context.Background(), AgentSpec{
		AgentType:      "general-purpose",
		SystemPrompt:   "You are a helpful assistant",
		ModelType:      "large",
		PermissionMode: "bypassPermissions",
		ToolPolicy: ToolPolicyProfile{
			AllowedTools:    []string{"view", "grep"},
			DisallowedTools: []string{"bash", "write"},
		},
	})
	require.NoError(t, err)

	assert.Equal(t, "general-purpose", capturedSpec.AgentType)
	assert.Equal(t, "You are a helpful assistant", capturedSpec.SystemPrompt)
	assert.Equal(t, "large", capturedSpec.ModelType)
	assert.Equal(t, "bypassPermissions", capturedSpec.PermissionMode)
	assert.Equal(t, []string{"view", "grep"}, capturedSpec.ToolPolicy.AllowedTools)
	assert.Equal(t, []string{"bash", "write"}, capturedSpec.ToolPolicy.DisallowedTools)
}

func TestCoordinatorAgentFactory_BuildRunner_EmptySpec(t *testing.T) {
	mockSA := &trackableSA{}

	coord := &stubBuildCoordinator{
		buildSessionAgentFn: func(ctx context.Context, spec AgentSpec) (SessionAgent, error) {
			return mockSA, nil
		},
	}

	factory := &CoordinatorAgentFactory{coordinator: coord}
	runner, err := factory.BuildRunner(context.Background(), AgentSpec{})
	require.NoError(t, err)
	require.NotNil(t, runner)
}

func TestCoordinatorAgentFactory_BuildRunner_Error(t *testing.T) {
	coord := &stubBuildCoordinator{
		buildSessionAgentFn: func(ctx context.Context, spec AgentSpec) (SessionAgent, error) {
			return nil, errors.New("model not configured")
		},
	}

	factory := &CoordinatorAgentFactory{coordinator: coord}
	runner, err := factory.BuildRunner(context.Background(), AgentSpec{
		AgentType: "general-purpose",
	})
	assert.Error(t, err)
	assert.Nil(t, runner)
	assert.Contains(t, err.Error(), "build session agent")
	assert.Contains(t, err.Error(), "model not configured")
}

func TestCoordinatorAgentFactory_BuildRunner_TwoCallsCreateSeparateRunners(t *testing.T) {
	var callCount int
	coord := &stubBuildCoordinator{
		buildSessionAgentFn: func(ctx context.Context, spec AgentSpec) (SessionAgent, error) {
			callCount++
			return &trackableSA{}, nil
		},
	}

	factory := &CoordinatorAgentFactory{coordinator: coord}
	r1, err := factory.BuildRunner(context.Background(), AgentSpec{})
	require.NoError(t, err)
	r2, err := factory.BuildRunner(context.Background(), AgentSpec{})
	require.NoError(t, err)

	assert.NotSame(t, r1, r2, "each BuildRunner call should create a distinct TurnRunner")
	assert.Equal(t, 2, callCount)
}
