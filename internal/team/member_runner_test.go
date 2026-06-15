package team

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestValidTransitions_CoversAllMemberStatuses(t *testing.T) {
	// Every defined MemberStatus must be a key in validTransitions.
	for _, s := range allMemberStatuses {
		_, ok := validTransitions[s]
		assert.Truef(t, ok, "MemberStatus %q missing from validTransitions", s)
	}
	// The map must have exactly 11 keys (one per MemberStatus).
	assert.Len(t, validTransitions, 11, "validTransitions must cover all 11 MemberStatus values")
}

func TestValidTransitions_Allowed(t *testing.T) {
	tests := []struct {
		from MemberStatus
		to   MemberStatus
		ok   bool
	}{
		// created
		{MemberCreated, MemberStarting, true},
		{MemberCreated, MemberStopped, true},
		{MemberCreated, MemberIdle, false},
		// starting
		{MemberStarting, MemberIdle, true},
		{MemberStarting, MemberFailed, true},
		{MemberStarting, MemberStopped, false},
		// idle — most transitions
		{MemberIdle, MemberQueued, true},
		{MemberIdle, MemberRunning, true},
		{MemberIdle, MemberBlocked, true},
		{MemberIdle, MemberShuttingDown, true},
		{MemberIdle, MemberStopped, true},
		{MemberIdle, MemberFailed, false},
		// queued
		{MemberQueued, MemberRunning, true},
		{MemberQueued, MemberIdle, true},
		{MemberQueued, MemberShuttingDown, true},
		{MemberQueued, MemberFailed, false},
		// running
		{MemberRunning, MemberIdle, true},
		{MemberRunning, MemberWaitingPermission, true},
		{MemberRunning, MemberBlocked, true},
		{MemberRunning, MemberCancelingTurn, true},
		{MemberRunning, MemberFailed, true},
		{MemberRunning, MemberShuttingDown, true},
		{MemberRunning, MemberStopped, false},
		// waiting_permission
		{MemberWaitingPermission, MemberIdle, true},
		{MemberWaitingPermission, MemberBlocked, true},
		{MemberWaitingPermission, MemberCancelingTurn, true},
		{MemberWaitingPermission, MemberShuttingDown, true},
		{MemberWaitingPermission, MemberRunning, false},
		// blocked
		{MemberBlocked, MemberIdle, true},
		{MemberBlocked, MemberShuttingDown, true},
		{MemberBlocked, MemberStopped, true},
		{MemberBlocked, MemberRunning, false},
		// canceling_turn
		{MemberCancelingTurn, MemberIdle, true},
		{MemberCancelingTurn, MemberBlocked, true},
		{MemberCancelingTurn, MemberShuttingDown, true},
		{MemberCancelingTurn, MemberFailed, true},
		{MemberCancelingTurn, MemberRunning, false},
		// shutting_down
		{MemberShuttingDown, MemberStopped, true},
		{MemberShuttingDown, MemberFailed, true},
		{MemberShuttingDown, MemberIdle, false},
		// stopped (terminal)
		{MemberStopped, MemberIdle, false},
		{MemberStopped, MemberStarting, false},
		// failed
		{MemberFailed, MemberStopped, true},
		{MemberFailed, MemberIdle, false},
		{MemberFailed, MemberStarting, false},
	}
	for _, tt := range tests {
		t.Run(string(tt.from)+"->"+string(tt.to), func(t *testing.T) {
			allowed := validTransitions[tt.from]
			found := false
			for _, a := range allowed {
				if a == tt.to {
					found = true
					break
				}
			}
			if tt.ok {
				assert.True(t, found, "transition %s->%s should be allowed", tt.from, tt.to)
			} else {
				assert.False(t, found, "transition %s->%s should NOT be allowed", tt.from, tt.to)
			}
		})
	}
}

func TestWakeSource_Consts(t *testing.T) {
	sources := []WakeSource{WakeSourceMailbox, WakeSourceTask, WakeSourceExplicit, WakeSourceRecovery, WakeSourceDependency}
	seen := map[WakeSource]bool{}
	for _, s := range sources {
		assert.False(t, seen[s], "duplicate WakeSource value %d", s)
		seen[s] = true
	}
	assert.Len(t, sources, 5)
}
