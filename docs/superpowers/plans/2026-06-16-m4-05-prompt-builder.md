# M4-05: PromptEnvelope Builder

> Status: merged (reconstructed plan) | 1.5 days
> Dependencies: M4-04 (Mailbox), M4-01 (MemberRunner)
> Worktree: `G:/ai-project/remote-github/crush-worktrees/m4-05`

## Goal

Replace M4-01's `buildPrompt` stub with a full PromptEnvelope builder that assembles 9 sections in fixed priority order, with structured overflow truncation.

## Files

| File | Action | Purpose |
|---|---|---|
| `internal/team/prompt_builder.go` | NEW | PromptBuilder struct, 9 section methods, Build/concat/truncate |
| `internal/team/prompt_builder_test.go` | NEW | 27 tests covering all 6 acceptance criteria |
| `internal/team/member_runner.go` | MODIFY | Replace buildPrompt stub with PromptBuilder.Build() |

## Architecture

### MailboxReader Interface (Seam 1)
Subset of Service: just `GetUnreadMessages`. Mock-friendly. Service satisfies it directly.

### Section Priority Order (fixed, Seam 2)
```
1 (untouchable): system_policy → member_identity → current_task → reporting_rules
2 (high):        direct_messages
3 (medium):      dependency_results → leader_instruction
4 (low):         broadcast_messages
5 (lowest):      session_summary
```

### Overflow Truncation (Seam 3)
- Byte-count based (not token count), consistent with `delegate_runner.go`
- Greedy truncation from priority 5 down to 2
- Priority 1 sections NEVER truncated
- If priority-1 exceeds MaxBytes → `ErrContextOverflow`

### UNTRUSTED PEER INPUT (Seam 4)
Mailbox content (direct_messages, broadcast_messages) marked with `[UNTRUSTED PEER INPUT]` prefix.

### Task Text (Seam 5)
Optional `*TeamTask` set before Build(). MemberRunner passes the claimed task.

## Design Seams

1. **MailboxReader interface** — subset of Service, mock-friendly
2. **Task text via optional *TeamTask** — set on PromptBuilder before Build()
3. **Byte count** — not token count, aligns with delegate_runner.go
4. **Structured per-section truncation** — header preserved, low-priority content replaced with `[truncated]`
5. **MemberRunner integration** — replaces M4-01 buildPrompt stub

## Acceptance Criteria (6)

1. Section order testable
2. Task full text never truncated
3. Direct before broadcast
4. Overflow truncates low priority
5. Task-only overflow → ErrContextOverflow
6. Mailbox UNTRUSTED PEER INPUT marker

## Implementation (4 tasks TDD)

- T1: Types + MailboxReader + PromptBuilder struct + ErrContextOverflow
- T2: 9 section methods
- T3: Build + concat + overflow truncation
- T4: Integrate with MemberRunner (replace buildPrompt stub)
