// recovery.go defines the StaleRunInfo and RecoveryReport types shared between
// M4-03 (basic stale-run detection) and M4-08 (expanded recovery with
// InterruptedReason, UsageStatus, and member-state recovery).
//
// The RecoverStaleRuns method on Scheduler (scheduler.go) produces a
// RecoveryReport. M4-08 expands StaleRunInfo and adds StartupRecovery logic.

package team

import "time"

// StaleRunInfo describes a single stale run detected during recovery.
// M4-08 adds InterruptedReason (string) and UsageStatus (string).
type StaleRunInfo struct {
	RunID         string    `json:"run_id"`
	TeamID        string    `json:"team_id"`
	MemberID      string    `json:"member_id"`
	Status        string    `json:"status"`
	LastHeartbeat time.Time `json:"last_heartbeat"`
}

// RecoveryReport is the result of a stale-run recovery pass.
type RecoveryReport struct {
	InterruptedCount int            `json:"interrupted_count"`
	Details          []StaleRunInfo `json:"details,omitempty"`
}
