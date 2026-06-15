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
		info := StaleRunInfo{
			RunID:    run.ID,
			TeamID:   run.TeamID,
			MemberID: run.MemberID,
			Status:   string(run.Status),
		}
		if run.HeartbeatAt != nil {
			info.LastHeartbeat = *run.HeartbeatAt
		}

		// MarkRunTerminal guards on status='running' (Seam 10). A stale run in
		// 'queued' or 'waiting_permission' will fail to match — skip it silently.
		// M4-08 widens the guard to accept those statuses too.
		if err := s.svc.MarkRunTerminal(ctx, MarkRunTerminalRequest{
			TeamID: run.TeamID,
			RunID:  run.ID,
			Status: RunInterrupted,
			Error:  "heartbeat timeout — run interrupted by stale recovery",
		}); err != nil {
			// MarkRunTerminal failed — likely the run is not in 'running' status.
			// Don't count it as interrupted; M4-08 adds structured error tracking.
			continue
		}

		report.InterruptedCount++
		report.Details = append(report.Details, info)
	}

	return report, nil
}
