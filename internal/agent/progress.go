package agent

import (
	"context"

	"charm.land/fantasy"
	"github.com/charmbracelet/crush/internal/pubsub"
)

// AgentProgressEvent is a lifecycle progress observation for an async
// sub-agent run, published on the package-level progress broker
// (SubscribeAgentProgress). M1-07 publishes two events per run: one
// "running" event at start, and one terminal event (state done/error/
// canceled) at completion carrying the real tool-call count, token total,
// and elapsed time derived from the *fantasy.AgentResult.
//
// State is one of the subAgentState* constants (async.go). It lets a
// subscriber distinguish the start observation from the terminal one.
//
// Field names mirror the master task doc (02-m1-subagent-foundation.md
// :1156-1169); State is the one additive field (documented in the plan's
// Design seam corrections).
type AgentProgressEvent struct {
	RunID        string `json:"run_id"`
	AgentType    string `json:"agent_type"`
	State        string `json:"state"`
	ToolUseCount int    `json:"tool_use_count"`
	LastToolName string `json:"last_tool_name,omitempty"`
	TokenCount   int    `json:"token_count"`
	ActivityDesc string `json:"activity_desc,omitempty"`
	ElapsedMs    int64  `json:"elapsed_ms"`
}

// progressBroker is the package-level in-process broker for
// AgentProgressEvent, mirroring the pattern in
// internal/agent/tools/mcp/init.go. It uses the default lossy Publish
// (non-blocking; full subscriber buffers drop events with a warning and a
// DropCount bump) so progress publication never backpressures the
// publishing goroutine (acceptance #4).
var progressBroker = pubsub.NewBroker[AgentProgressEvent]()

// PublishAgentProgress publishes a progress observation to every active
// subscriber. Delivery is best-effort and non-blocking (see progressBroker).
func PublishAgentProgress(eventType pubsub.EventType, event AgentProgressEvent) {
	progressBroker.Publish(eventType, event)
}

// SubscribeAgentProgress returns a channel that receives every
// AgentProgressEvent published after the call, until ctx is canceled.
func SubscribeAgentProgress(ctx context.Context) <-chan pubsub.Event[AgentProgressEvent] {
	return progressBroker.Subscribe(ctx)
}

// lastToolName returns the name of the last tool the model called across
// all steps of the agent run, or "" if there were no tool calls or result
// is nil. Used to populate AgentProgressEvent.LastToolName on terminal
// events.
//
// ToolCalls() (fantasy ResponseContent.ToolCalls, model.go:95) returns the
// []ToolCallContent parts of a step's content; each ToolCallContent carries
// a flat ToolName field (content.go:431).
func lastToolName(result *fantasy.AgentResult) string {
	if result == nil {
		return ""
	}
	var name string
	for _, step := range result.Steps {
		for _, tc := range step.Content.ToolCalls() {
			name = tc.ToolName
		}
	}
	return name
}
