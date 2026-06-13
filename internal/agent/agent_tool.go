package agent

import (
	"context"
	_ "embed"
	"errors"
	"fmt"

	"charm.land/fantasy"

	"github.com/charmbracelet/crush/internal/agent/prompt"
	"github.com/charmbracelet/crush/internal/agent/tools"
	"github.com/charmbracelet/crush/internal/config"
)

//go:embed templates/agent_tool.md
var agentToolDescription string

type AgentParams struct {
	Prompt string `json:"prompt" description:"The task for the agent to perform"`
}

const (
	AgentToolName = "agent"
)

// recursionDepthKey is the context key carrying the current sub-agent nesting
// depth (0 at the top-level call of the agent tool).
type recursionDepthKey struct{}

// maxRecursionDepth is the maximum nesting depth for sub-agent calls. A call
// made AT this depth (i.e. depth == maxRecursionDepth) is rejected.
const maxRecursionDepth = 3

// withRecursionDepth returns a copy of ctx carrying the given depth.
func withRecursionDepth(ctx context.Context, depth int) context.Context {
	return context.WithValue(ctx, recursionDepthKey{}, depth)
}

// getRecursionDepth reads the nesting depth from ctx, defaulting to 0.
func getRecursionDepth(ctx context.Context) int {
	v, ok := ctx.Value(recursionDepthKey{}).(int)
	if !ok {
		return 0
	}
	return v
}

func (c *coordinator) agentTool(ctx context.Context) (fantasy.AgentTool, error) {
	agentCfg, ok := c.cfg.Config().Agents[config.AgentTask]
	if !ok {
		return nil, errors.New("task agent not configured")
	}
	prompt, err := taskPrompt(prompt.WithWorkingDir(c.cfg.WorkingDir()))
	if err != nil {
		return nil, err
	}

	agent, err := c.buildAgent(ctx, prompt, agentCfg, true)
	if err != nil {
		return nil, err
	}
	return fantasy.NewParallelAgentTool(
		AgentToolName,
		agentToolDescription,
		func(ctx context.Context, params AgentParams, call fantasy.ToolCall) (fantasy.ToolResponse, error) {
			if params.Prompt == "" {
				return fantasy.NewTextErrorResponse("prompt is required"), nil
			}

			// M1-02: enforce the sub-agent recursion-depth limit. A call
			// attempted at depth >= maxRecursionDepth is rejected; otherwise
			// we pass depth+1 down so nested calls can check themselves.
			depth := getRecursionDepth(ctx)
			if depth >= maxRecursionDepth {
				return fantasy.NewTextErrorResponse(
					fmt.Sprintf("max agent recursion depth exceeded (depth=%d, max=%d)", depth, maxRecursionDepth),
				), nil
			}
			ctx = withRecursionDepth(ctx, depth+1)

			sessionID := tools.GetSessionFromContext(ctx)
			if sessionID == "" {
				return fantasy.ToolResponse{}, errors.New("session id missing from context")
			}

			agentMessageID := tools.GetMessageFromContext(ctx)
			if agentMessageID == "" {
				return fantasy.ToolResponse{}, errors.New("agent message id missing from context")
			}

			return c.runSubAgent(ctx, subAgentParams{
				Agent:          agent,
				SessionID:      sessionID,
				AgentMessageID: agentMessageID,
				ToolCallID:     call.ID,
				Prompt:         params.Prompt,
				SessionTitle:   "New Agent Session",
			})
		},
	), nil
}
