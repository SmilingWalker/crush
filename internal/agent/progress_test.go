package agent

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"charm.land/fantasy"
	"github.com/charmbracelet/crush/internal/pubsub"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestAgentProgressEvent_JSONRoundTrip locks the public shape: the event
// serializes to legal JSON whose fields survive a round-trip, and the new
// State field carries the lifecycle marker.
func TestAgentProgressEvent_JSONRoundTrip(t *testing.T) {
	original := AgentProgressEvent{
		RunID:        "run-123",
		AgentType:    "task",
		State:        subAgentStateDone,
		ToolUseCount: 4,
		LastToolName: "read",
		TokenCount:   1500,
		ActivityDesc: "completed",
		ElapsedMs:    45200,
	}

	data, err := json.Marshal(original)
	require.NoError(t, err)
	require.True(t, json.Valid(data), "serialized event must be legal JSON")

	var decoded AgentProgressEvent
	require.NoError(t, json.Unmarshal(data, &decoded))

	assert.Equal(t, original, decoded)

	// omitempty: an event with empty last_tool_name and activity_desc must
	// omit both fields.
	start := AgentProgressEvent{RunID: "run-1", AgentType: "task", State: subAgentStateRunning}
	startData, err := json.Marshal(start)
	require.NoError(t, err)
	assert.NotContains(t, string(startData), "last_tool_name", "last_tool_name must be omitted when empty")
	assert.NotContains(t, string(startData), "activity_desc", "activity_desc must be omitted when empty")
}

// TestLastToolName_Extraction locks acceptance #2's tool-name attribution:
// the last tool called across all steps is surfaced, and empty steps yield "".
func TestLastToolName_Extraction(t *testing.T) {
	t.Run("returns last tool name across steps", func(t *testing.T) {
		ar := &fantasy.AgentResult{
			Steps: []fantasy.StepResult{
				{Response: fantasy.Response{Content: fantasy.ResponseContent{
					fantasy.ToolCallContent{ToolName: "read"},
				}}},
				{Response: fantasy.Response{Content: fantasy.ResponseContent{
					fantasy.ToolCallContent{ToolName: "edit"},
					fantasy.ToolCallContent{ToolName: "bash"},
				}}},
			},
		}
		assert.Equal(t, "bash", lastToolName(ar))
	})

	t.Run("no tool calls returns empty", func(t *testing.T) {
		ar := &fantasy.AgentResult{
			Response: fantasy.Response{Content: fantasy.ResponseContent{
				fantasy.TextContent{Text: "just text"},
			}},
		}
		assert.Equal(t, "", lastToolName(ar))
	})

	t.Run("nil result returns empty", func(t *testing.T) {
		assert.Equal(t, "", lastToolName(nil))
	})
}

// TestSubscribeAgentProgress_ReceivesPublished locks acceptance #1: a
// subscriber to the package-level broker receives the events a publisher
// emits, tagged with the correct pubsub EventType and payload.
func TestSubscribeAgentProgress_ReceivesPublished(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	sub := SubscribeAgentProgress(ctx)

	PublishAgentProgress(pubsub.UpdatedEvent, AgentProgressEvent{
		RunID:     "run-abc",
		AgentType: "task",
		State:     subAgentStateRunning,
	})

	select {
	case ev := <-sub:
		assert.Equal(t, pubsub.UpdatedEvent, ev.Type)
		assert.Equal(t, "run-abc", ev.Payload.RunID)
		assert.Equal(t, subAgentStateRunning, ev.Payload.State)
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for published progress event")
	}
}

// TestAgentProgress_FullBufferDoesNotBlock locks acceptance #4: a saturated
// subscriber channel must NOT block the publishing goroutine. Broker.Publish
// is lossy by design (drops + counts on full buffer), so PublishAgentProgress
// returns promptly even when nobody is draining.
func TestAgentProgress_FullBufferDoesNotBlock(t *testing.T) {
	// Subscribe with a context we keep alive, then never drain the channel.
	// The default broker buffer is large (4096); we publish well past it and
	// require that every Publish returns quickly (i.e. drops rather than
	// blocks). A blocked Publish would hang this test past the timeout.
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	_ = SubscribeAgentProgress(ctx) // intentionally undrained

	done := make(chan struct{})
	go func() {
		for i := 0; i < 6000; i++ { // > bufferSize (4096)
			PublishAgentProgress(pubsub.UpdatedEvent, AgentProgressEvent{
				RunID:     "run-saturate",
				AgentType: "task",
				State:     subAgentStateRunning,
			})
		}
		close(done)
	}()

	select {
	case <-done:
		// Success: the publisher never blocked despite a full, undrained buffer.
	case <-time.After(2 * time.Second):
		t.Fatal("PublishAgentProgress blocked on a full subscriber buffer (acceptance #4 regression)")
	}
}
