package actor_test

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/charmbracelet/crush/internal/actor"
	"github.com/stretchr/testify/assert"
)

func TestActorContext_RoundTrip(t *testing.T) {
	ctx := context.Background()
	_, ok := actor.FromContext(ctx)
	assert.False(t, ok)

	ac := actor.ActorContext{
		SessionID:       "sess_abc123",
		ParentSessionID: "sess_parent",
		MessageID:       "msg_456",
		ToolCallID:      "call_789",
	}
	ctx = ac.WithContext(ctx)

	got, ok := actor.FromContext(ctx)
	assert.True(t, ok)
	assert.Equal(t, "sess_abc123", got.SessionID)
	assert.Equal(t, "sess_parent", got.ParentSessionID)
}

func TestActorContext_ShortID(t *testing.T) {
	ac := actor.ActorContext{SessionID: "abcdefghijklmnop"}
	assert.Equal(t, "abcdefgh", ac.ShortID())

	ac2 := actor.ActorContext{SessionID: "short"}
	assert.Equal(t, "short", ac2.ShortID())
}

func TestActorContext_IsSubAgent(t *testing.T) {
	assert.True(t, actor.ActorContext{ParentSessionID: "parent"}.IsSubAgent())
	assert.False(t, actor.ActorContext{}.IsSubAgent())
}

func TestActorContext_JSONOmitempty(t *testing.T) {
	// M1 omitempty 字段为空时不应出现在 JSON 中；SessionID 无 omitempty，始终出现。
	ac := actor.ActorContext{SessionID: "sess_abc123"}
	data, err := json.Marshal(ac)
	assert.NoError(t, err)

	var decoded map[string]any
	assert.NoError(t, json.Unmarshal(data, &decoded))

	// SessionID 无 omitempty —— 必须出现
	assert.Contains(t, decoded, "session_id")
	assert.Equal(t, "sess_abc123", decoded["session_id"])

	// 所有 omitempty 字段为空 —— 不应出现
	for _, key := range []string{
		"parent_session_id", "message_id", "tool_call_id", "workspace_id",
		"team_id", "member_id", "member_name", "member_role", "task_id", "run_id",
	} {
		assert.NotContains(t, decoded, key, "empty field %s should be omitted", key)
	}
}

func TestActorContext_IsTeamMember(t *testing.T) {
	// 两个字段都填才是 team member
	assert.True(t, actor.ActorContext{TeamID: "team_1", MemberID: "member_1"}.IsTeamMember())
	// 缺一不可
	assert.False(t, actor.ActorContext{TeamID: "team_1"}.IsTeamMember())
	assert.False(t, actor.ActorContext{MemberID: "member_1"}.IsTeamMember())
	// 都空
	assert.False(t, actor.ActorContext{}.IsTeamMember())
}
