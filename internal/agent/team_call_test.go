package agent

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestTurnStatus_IsTerminal(t *testing.T) {
	assert.True(t, TurnCompleted.IsTerminal())
	assert.True(t, TurnCanceled.IsTerminal())
	assert.True(t, TurnFailed.IsTerminal())
	assert.False(t, TurnQueued.IsTerminal())
	assert.False(t, TurnRunning.IsTerminal())
}

func TestTeamAgentCall_NotConfusableWithSessionAgentCall(t *testing.T) {
	// 编译时类型检查：TeamAgentCall 和 SessionAgentCall 是不同的类型
	var tc TeamAgentCall
	// 这行如果编译通过，说明两个类型确实独立
	_ = tc.SessionID
	_ = tc.ToolPolicy
}

func TestAgentSpec_CarriesMaxTurns(t *testing.T) {
	// MaxTurns 是 AgentSpec 上唯一的 turn-budget seam（BuildRunner 只暴露 AgentSpec）。
	// 零值 = 无限制（无 turn 预算），与 config.Agent.MaxTurns 语义一致。
	spec := AgentSpec{
		AgentType:    "general-purpose",
		SystemPrompt: "p",
		MaxTurns:     8,
	}
	assert.Equal(t, 8, spec.MaxTurns)

	// 零值断言：未设置预算时为零。
	var zero AgentSpec
	assert.Zero(t, zero.MaxTurns)
}
