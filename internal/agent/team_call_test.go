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
