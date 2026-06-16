# M4 Architecture Review — Auto-Debug Items

> Date: 2026-06-16 | Reviewer: team-lead | Severity: 1 HIGH / 3 MEDIUM / 2 LOW

---

## HIGH-1: transitionLocked async DB write drops version

**File**: `internal/team/member_runner.go:117`

```go
go func() {
    updated, err := m.svc.UpdateMemberState(context.Background(), ...)
    if err != nil {
        slog.Error(...)
        return
    }
    _ = updated  // version 被丢弃
}()
```

**Problem**: in-memory state already set to `to`, but DB write may fail. On crash recovery:
- memory state = idle, DB state = running → inconsistency
- version never tracked → CAS blind on next transition

**Fix direction**:
- Option A: `m.Version = updated.Version` after successful DB write
- Option B: rollback in-memory state on DB write failure
- Option C: make transitionLocked synchronous (remove goroutine), caller already holds mu

---

## MEDIUM-2: PromptBuilder truncation logic never used

**File**: `internal/team/member_runner.go:333`

```go
pb.SetMaxBytes(0)  // 0 = no limit → truncation never triggers
```

**Problem**: 150 lines of priority-based overflow truncation (`concatWithTruncation`, `ErrContextOverflow`, 5 priority tiers) are dead code in production. Prompt can grow unbounded and exceed model context window.

**Fix direction**:
- Read `MaxBytes` from AgentSpec or config
- Wire through `buildPrompt()` → `pb.SetMaxBytes(spec.MaxPromptBytes)`

---

## MEDIUM-3: go mr.Start(ctx) swallows startup failures in StartTeam

**File**: `internal/team/team_runner.go:128`

```go
go mr.Start(ctx)  // error return ignored
```

**Problem**: If `factory.BuildRunner` fails (model unavailable, etc.), member enters `Failed` state but:
- TeamRunner.StartTeam doesn't know
- No retry or alert
- Member stays in registry with Failed state, never recovers

**Fix direction**:
- At minimum: log.Error on Start failure
- Better: use errgroup, collect results, return partial failure info

---

## MEDIUM-4: PromptBuilder has 4 stale placeholder sections

**File**: `internal/team/prompt_builder.go:285-312`

```go
dependencyResults()  → "not yet implemented"  // M4-11 is DONE
leaderInstruction()  → "no leader instruction" // no wire point
broadcastMessages()  → "no broadcast messages" // M4-07 is DONE  
sessionSummary()     → "no session summary"    // no wire point
```

**Problem**: Comments say "placeholder until M4-XX" but M4-11 (deps) and M4-07 (shutdown/broadcast) are already implemented. Sections return dead text.

**Fix direction**:
- `dependencyResults()`: wire to M4-11 GetTaskWithDeps
- `broadcastMessages()`: wire to M4-04 mailbox broadcast filter
- `leaderInstruction()` + `sessionSummary()`: either implement or mark as M5 deferred

---

## LOW-5: StopTeam serial shutdown

**File**: `internal/team/team_runner.go:140`

```go
for _, mr := range t.members {
    _ = mr.Shutdown(ctx, mode)  // 30s max per member
}
```

**Problem**: N members × 30s wait = slow teardown. Each Shutdown has its own wait loop; doing them in parallel would cut total time to max(slowest member).

**Fix direction**: errgroup + goroutine per member Shutdown call.

---

## LOW-6: TeamRunner.Status() reaches into MemberRunner.mu

**File**: `internal/team/team_runner.go:222`

```go
mr.mu.Lock()       // cross-object lock
ms := MemberRuntimeState{...}
mr.mu.Unlock()
```

**Problem**: Encapsulation break — if MemberRunner changes internal locking (e.g., to RWMutex), this silently breaks.

**Fix direction**: Add `MemberRunner.Status() MemberRuntimeState` method that handles its own locking.

---

## Severity summary

| ID | Component | Severity | Trigger condition | Fix effort |
|----|-----------|----------|-------------------|------------|
| HIGH-1 | transitionLocked | 🔴 | DB write fail + process crash | Small |
| MED-2 | SetMaxBytes(0) | 🟡 | Large prompt > context window | Small |
| MED-3 | go mr.Start() | 🟡 | Factory build fails | Small |
| MED-4 | Placeholder sections | 🟡 | Always (dead text in prompt) | Medium |
| LOW-5 | Serial shutdown | 🟢 | Many members | Small |
| LOW-6 | Cross-object lock | 🟢 | MemberRunner refactor | Small |
