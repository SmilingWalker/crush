# M2 Delegate Demo — Scenario

> **Prerequisites (live demo):** the default (`--live`) path needs two blockers
> cleared — see `scripts/demo-m2-prerequisites.md`. The `--smoke` path runs
> today and previews the render + aggregate wiring with mock delegates.

## Scenario: search three directions in parallel

1. Start crush with the delegate preview enabled (once
   `Experimental.AgentTeamPreview` ships — Blocker 2).
2. Send the prompt:

   ```
   Search the entire codebase for:
   a) All SQL migration files and their schemas
   b) All HTTP API route definitions
   c) All Go interface definitions
   Use 3 delegates in parallel.
   ```

## Expected behavior

### T1: Delegate launch
- Compact delegate item appears: `Delegates 3 running / 0 done`.
- Each delegate shows a running spinner.

### T2: Delegates executing
- Press Enter to expand — all three delegates show `running`.
- Each delegate shows current activity (e.g. `grep: CREATE TABLE`).

### T3: Delegates finishing
- First finishes → `Delegates 2 running / 1 done`.
- Second finishes → `Delegates 1 running / 2 done`.
- All finish → `group.Status = done`.
- The aggregated result appears in the message.

### T4: Cancel scenario
- While a delegate is running, press Esc → a cancel prompt appears.
- On confirm, every delegate's status becomes `canceled`.

## Verification checklist
- [ ] Three delegates launch in parallel.
- [ ] Each delegate uses read-only tools only (`view`/`grep`/`glob`/`ls`/`sourcegraph`).
- [ ] The expand panel shows correct per-delegate status.
- [ ] After completion the aggregated result is correct.
- [ ] Cancel works end-to-end (Esc → confirm → all canceled).

### Verified today by `--smoke` (no live LLM)
- [x] Compact-line skeleton + per-child status derived from real `DelegateRunGroup` state.
- [x] `AggregateResults` markdown renders with CANCELED annotation on the in-flight delegate.
- [x] `CancelGroup` path runs without panic.
