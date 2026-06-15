// recovery.go defines the StaleRunInfo, RecoveryReport, and related types
// shared between M4-03 (basic stale-run detection) and M4-08 (expanded recovery
// with InterruptedReason, UsageStatus, member-state recovery, and
// StartupRecovery).
//
// The RecoverStaleRuns method on Scheduler (scheduler.go) produces a
// RecoveryReport. M4-08 expands StaleRunInfo and adds StartupRecovery logic.

package team

import "time"

// InterruptedReason classifies why a run was interrupted.
// M4-08 adds this enum; M4-03 only recorded "heartbeat timeout" as a string.
type InterruptedReason string

const (
	InterruptedCrashed        InterruptedReason = "crashed"
	InterruptedHeartbeatLost  InterruptedReason = "heartbeat_lost"
	InterruptedLeaderShutdown InterruptedReason = "leader_shutdown"
)

// UsageStatus categorises the cost-data completeness of a terminal run.
// Defined as package-level string consts because UsageStatus is a free-form
// string on TeamRun (not a Valid()-bearing enum). M4-12 Cost Accounting
// consumes these values to decide whether to display cost or "---".
const (
	UsageStatusFinal   = "final"
	UsageStatusPartial = "partial"
	UsageStatusUnknown = "unknown"
)

// StaleRunInfo describes a single stale run detected during recovery.
// M4-08 adds InterruptedReason (InterruptedReason) and UsageStatus (string).
type StaleRunInfo struct {
	RunID             string            `json:"run_id"`
	TeamID            string            `json:"team_id"`
	MemberID          string            `json:"member_id"`
	Status            string            `json:"status"`
	LastHeartbeat     time.Time         `json:"last_heartbeat"`
	InterruptedReason InterruptedReason `json:"interrupted_reason,omitempty"`
	UsageStatus       string            `json:"usage_status,omitempty"`
}

// RecoveryError tracks a single failed transition during RecoverStaleRuns.
// M4-08 adds this so callers can report which runs failed and why.
type RecoveryError struct {
	RunID string `json:"run_id"`
	Error string `json:"error"`
}

// RecoveryReport is the result of a stale-run recovery pass.
// M4-08 adds Errors for transition-failure tracking.
type RecoveryReport struct {
	InterruptedCount int             `json:"interrupted_count"`
	Details          []StaleRunInfo  `json:"details,omitempty"`
	Errors           []RecoveryError `json:"errors,omitempty"`
}

// StartupRecoveryReport is the combined result of a StartupRecovery pass:
// stale-run interruption + member-state restoration.
type StartupRecoveryReport struct {
	RunsRecovered   int `json:"runs_recovered"`
	MembersRestored int `json:"members_restored"`
}
