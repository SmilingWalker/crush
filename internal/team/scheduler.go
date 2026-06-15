// scheduler.go implements M4-03 Scheduler: task claiming (delegating to
// Service's atomic ClaimNextTask), per-run heartbeat goroutines (periodic
// HeartbeatRun ticker), and startup stale-run recovery (detect runs whose
// heartbeat expired, mark them interrupted).
//
// The Scheduler wraps team.Service — it adds goroutine lifecycle and recovery
// orchestration, but delegates ALL database operations to Service.

package team

import (
	"context"
	"time"
)

// SchedulerConfig holds the tunable parameters for the Scheduler.
// Defaults from master doc M4-03:394-398.
type SchedulerConfig struct {
	HeartbeatInterval time.Duration // between heartbeat pings (default 30s)
	HeartbeatTimeout  time.Duration // stale threshold (default 90s)
	MaxConcurrentRuns int           // per-member limit (default 3, enforced by M4-02)
}

// DefaultSchedulerConfig returns the master-doc defaults.
func DefaultSchedulerConfig() SchedulerConfig {
	return SchedulerConfig{
		HeartbeatInterval: 30 * time.Second,
		HeartbeatTimeout:  90 * time.Second,
		MaxConcurrentRuns: 3,
	}
}

// Scheduler coordinates task claiming, heartbeat goroutines, and stale-run
// recovery. It wraps team.Service — all DB operations go through Service.
type Scheduler struct {
	svc Service
	cfg SchedulerConfig
}

// NewScheduler creates a Scheduler backed by the given Service.
func NewScheduler(svc Service, cfg SchedulerConfig) *Scheduler {
	return &Scheduler{svc: svc, cfg: cfg}
}

// ClaimNextTask delegates to the Service's atomic ClaimNextTask.
// The caller (M4-02 TeamRunner) is responsible for waking the member after a
// successful claim — the Scheduler only returns the claimed task.
func (s *Scheduler) ClaimNextTask(ctx context.Context, req ClaimNextTaskRequest) (TeamTask, error) {
	return s.svc.ClaimNextTask(ctx, req)
}

// StartHeartbeat launches a background goroutine that calls HeartbeatRun on the
// Service every cfg.HeartbeatInterval. The parent context controls lifetime:
// when it is cancelled, the goroutine stops. The returned cancel function lets
// the caller stop the heartbeat explicitly (e.g. after FinishRun or
// MarkRunTerminal).
//
// Heartbeat failures are non-fatal — the goroutine continues silently.
// M4-08 adds error logging and retry.
func (s *Scheduler) StartHeartbeat(ctx context.Context, runID string) (func(), error) {
	ctx, cancel := context.WithCancel(ctx)
	go func() {
		ticker := time.NewTicker(s.cfg.HeartbeatInterval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				if err := s.svc.HeartbeatRun(ctx, runID); err != nil {
					// Non-fatal: M4-08 adds logging/retry here.
				}
			}
		}
	}()
	return cancel, nil
}

// RecoverStaleRuns finds runs whose heartbeat_at is older than
// cfg.HeartbeatTimeout from now, marks each as interrupted via
// MarkRunTerminal, and returns a RecoveryReport. Only runs with status IN
// ('running','waiting_permission','queued') are returned by FindStaleRuns;
// MarkRunTerminal's status guard ('running') means only stale runs in
// 'running' are successfully marked (Seam 10 — M4-08 widens this).
//
// Idempotent: once a run is marked interrupted, it no longer matches the
// FindStaleRuns status filter, so a second recovery pass finds zero stale
// runs (unless new runs became stale in the meantime).
func (s *Scheduler) RecoverStaleRuns(ctx context.Context) (*RecoveryReport, error) {
	cutoff := time.Now().Add(-s.cfg.HeartbeatTimeout)
	stale, err := s.svc.FindStaleRuns(ctx, cutoff)
	if err != nil {
		return nil, err
	}

	report := &RecoveryReport{}
	for _, run := range stale {
		usageStatus := usageStatusForRun(run.Status)

		info := StaleRunInfo{
			RunID:             run.ID,
			TeamID:            run.TeamID,
			MemberID:          run.MemberID,
			Status:            string(run.Status),
			InterruptedReason: InterruptedHeartbeatLost,
			UsageStatus:       usageStatus,
		}
		if run.HeartbeatAt != nil {
			info.LastHeartbeat = *run.HeartbeatAt
		}

		// M4-08: pass the run's actual status as expected, so queued and
		// waiting_permission runs can also be transitioned to interrupted
		// (widen Seam 10 guard).
		if err := s.svc.MarkRunTerminal(ctx, MarkRunTerminalRequest{
			TeamID:         run.TeamID,
			RunID:          run.ID,
			Status:         RunInterrupted,
			ExpectedStatus: run.Status,
			Error:          "heartbeat timeout — run interrupted by stale recovery",
			UsageStatus:    usageStatus,
		}); err != nil {
			report.Errors = append(report.Errors, RecoveryError{
				RunID: run.ID,
				Error: err.Error(),
			})
			continue
		}

		report.InterruptedCount++
		report.Details = append(report.Details, info)
	}

	return report, nil
}

// usageStatusForRun maps a RunStatus to the appropriate UsageStatus string for
// an interrupted run. Running/waiting_permission had partial execution → partial;
// queued never started → unknown.
func usageStatusForRun(status RunStatus) string {
	switch status {
	case RunRunning, RunWaitingPermission:
		return UsageStatusPartial
	case RunQueued:
		return UsageStatusUnknown
	default:
		return UsageStatusUnknown
	}
}

// StartupRecovery performs a full startup recovery pass: marks stale runs as
// interrupted (via RecoverStaleRuns), then restores impacted members to idle
// (M4-03 acceptance #4 — deferred to M4-08). Each member whose run was
// interrupted has its current_run_id and current_task_id cleared and its status
// set to idle via UpdateMemberState.
//
// Member restoration is non-fatal — if one member's CAS fails (e.g. concurrent
// modification), the error is logged and the next member is attempted.
func (s *Scheduler) StartupRecovery(ctx context.Context) (*StartupRecoveryReport, error) {
	runReport, err := s.RecoverStaleRuns(ctx)
	if err != nil {
		return nil, err
	}

	// Deduplicate member IDs (one member may have multiple stale runs).
	members := make(map[string]string) // memberID → teamID
	for _, info := range runReport.Details {
		members[info.MemberID] = info.TeamID
	}

	restored := 0
	for memberID, teamID := range members {
		if _, err := s.svc.UpdateMemberState(ctx, UpdateMemberStateRequest{
			ID:     memberID,
			TeamID: teamID,
			Status: MemberIdle,
			// TaskID, RunID, ToolName left nil → cleared on the member row.
		}); err != nil {
			// Non-fatal: log and continue with the next member.
			continue
		}
		restored++
	}

	return &StartupRecoveryReport{
		RunsRecovered:   runReport.InterruptedCount,
		MembersRestored: restored,
	}, nil
}
