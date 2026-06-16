// shutdown.go implements the M4-07 Shutdown sequence — the full 9-step member
// shutdown flow supporting all 3 StopModes (Graceful/Cancel/Force).
//
// The 9-step sequence (master doc M4-07):
//   1. stopWakeups      — set shuttingDown flag; loop drops new wakes
//   2. write shutdown event  — PublishMemberEvent("member.shutting_down")
//   3. cancel context   — Graceful: defer cancel until after wait; Cancel/Force: immediate
//   4. wait loop        — poll state until Idle/Stopped/Failed or 30s deadline
//   5. flush            — call flushFn if set (nil-safe)
//   6. mark terminal    — Graceful: skip (turn completed normally); others: MarkRunTerminal
//   7. stopped state    — transitionLocked(ShuttingDown) then transitionLocked(Stopped)
//   8. write stopped event — PublishMemberEvent("member.stopped")
//   9. publish          — already done in steps 2&8 via PublishedAt = now
//
// Step differences by mode:
//   Graceful: wait for current turn to complete naturally, no cancel/markTerminal
//   Cancel:   cancel context → wait for drain → mark run canceled
//   Force:    immediate cancel, mark run interrupted, skip events/flush

package team

import (
	"context"
	"log/slog"
	"time"
)

// Shutdown executes the 9-step shutdown sequence for a member.
//
// Parameters:
//   - ctx: used for the wait-loop deadline; DB operations use context.Background()
//     since the caller's ctx may already be cancelled
//   - mode: Graceful (wait for turn), Cancel (cancel turn then wait), Force (immediate)
//
// Returns nil on success. Shutdown completes all 9 steps even if the caller's ctx
// is cancelled during the wait loop (the loop is cut short but terminal state is
// still reached). Shutdown is idempotent — calling it on an already-stopped or
// failed member returns nil immediately.
func (m *MemberRunner) Shutdown(ctx context.Context, mode StopMode) error {
	// Step 1: stopWakeups — prevent new wakes from being processed by the loop.
	m.mu.Lock()
	if m.State == MemberStopped || m.State == MemberFailed {
		m.mu.Unlock()
		return nil // idempotent: already terminal
	}
	m.shuttingDown = true
	m.mu.Unlock()

	// Step 2: write shutdown event.
	// Use background context — the caller's ctx may be cancelled by now.
	if mode != StopForce {
		if m.svc != nil {
			if err := m.svc.PublishMemberEvent(context.Background(), m.TeamID, m.ID, "member.shutting_down"); err != nil {
				slog.Error("Shutdown: PublishMemberEvent(shutting_down) failed",
					"member_id", m.ID, "error", err)
			}
		}
	}

	// Step 3: cancel context.
	//   Graceful: DON'T cancel yet — let the current turn complete naturally.
	//   Cancel/Force: cancel immediately to interrupt any in-progress turn.
	if mode != StopGraceful {
		m.mu.Lock()
		if m.cancel != nil {
			m.cancel()
		}
		m.mu.Unlock()
	}

	// Step 4: wait loop — poll until the member reaches a quiescent state.
	//   Graceful: wait for turn to complete (no cancel issued yet).
	//   Cancel:   wait for the cancelled context to drain through handleWake.
	//   Force:    skip entirely.
	// If the caller's ctx is cancelled during the wait, we break out of the loop
	// but still complete steps 5-9 to reach terminal state.
	if mode != StopForce {
		deadline := time.Now().Add(30 * time.Second)
		for time.Now().Before(deadline) {
			m.mu.Lock()
			state := m.State
			m.mu.Unlock()
			if state == MemberIdle || state == MemberStopped || state == MemberFailed {
				break
			}
			select {
			case <-ctx.Done():
				// Caller cancelled — cut the wait short but continue shutdown.
				goto proceedToFlush
			case <-time.After(100 * time.Millisecond):
			}
		}

		// Graceful: the turn completed; now cancel to stop the loop.
		if mode == StopGraceful {
			m.mu.Lock()
			if m.cancel != nil {
				m.cancel()
			}
			m.mu.Unlock()
		}
	}
proceedToFlush:

	// Step 5: flush — drain any pending async DB writes.
	if mode != StopForce && m.flushFn != nil {
		if err := m.flushFn(context.Background()); err != nil {
			slog.Error("Shutdown: flushFn failed", "member_id", m.ID, "error", err)
		}
	}

	// Step 6: mark run terminal.
	//   Graceful: the turn completed normally via handleWake → FinishRun, so skip.
	//   Cancel:   mark RunCanceled.
	//   Force:    mark RunInterrupted.
	if mode != StopGraceful {
		m.mu.Lock()
		runID := m.currentRunID
		m.mu.Unlock()
		if runID != "" && m.svc != nil {
			termStatus := RunCanceled
			if mode == StopForce {
				termStatus = RunInterrupted
			}
			if err := m.svc.MarkRunTerminal(context.Background(), MarkRunTerminalRequest{
				TeamID: m.TeamID, RunID: runID, Status: termStatus,
				Error: "shutdown",
			}); err != nil {
				slog.Error("Shutdown: MarkRunTerminal failed",
					"member_id", m.ID, "run_id", runID, "status", termStatus, "error", err)
			}
		}
	}

	// Step 7: stopped state — transition through the valid path shutting_down → stopped.
	m.mu.Lock()
	if m.State != MemberStopped && m.State != MemberFailed {
		m.transitionLocked(MemberShuttingDown)
		m.transitionLocked(MemberStopped)
	}
	m.mu.Unlock()

	// Step 8: write stopped event.
	if mode != StopForce {
		if m.svc != nil {
			if err := m.svc.PublishMemberEvent(context.Background(), m.TeamID, m.ID, "member.stopped"); err != nil {
				slog.Error("Shutdown: PublishMemberEvent(stopped) failed",
					"member_id", m.ID, "error", err)
			}
		}
	}

	// Step 9: publish — already done in steps 2 & 8 (PublishMemberEvent sets PublishedAt=now).
	return nil
}
