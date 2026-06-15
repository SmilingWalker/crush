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
