# Tool Prune Design

> Add a proactive, in-memory tool output pruning mechanism to Crush's agent loop.

## Problem

Crush has no tool-level context management. Every API call sends the full history of all tool outputs, even outputs that are no longer useful. The only space management mechanism is Summarize, which replaces the entire conversation with an LLM-generated summary. This is wasteful — old `bash` outputs, `grep` results, and file reads continue consuming tokens long after they've been consumed by the LLM.

## Solution

Add a `prune()` function that runs at the start of every `Run()` call, before `preparePrompt()`. It modifies messages in-memory only (no DB writes), replacing old tool outputs with a short marker string and clearing large edit parameters. This preserves original data in the database while reducing the token cost of each API call.

## Design Decisions

### In-memory only, no DB writes

Prune modifies the `[]message.Message` slice passed to `preparePrompt()` but never writes back to the database. Original tool outputs are permanently preserved. This means:

- No `PrunedAt` field needed on `ToolResult`
- No database migration
- No data loss
- Every `Run()` re-evaluates from the full DB data

### Trigger: every Run() call

Prune runs proactively at the start of every `Run()`, independent of context overflow detection. It is a background hygiene mechanism, not an emergency response. Summarize remains the separate emergency handler triggered by `StopWhen`.

### Route A: client-side message modification

Directly modify message content in-memory. No API-level features required. Compatible with all providers (self-hosted models with OpenAI-compatible API).

### PruneMinimum = 5K

Since self-hosted models have no prompt cache, the minimum threshold exists only to avoid trivial prunes that save negligible tokens. 5K tokens (~20KB) is a reasonable floor.

## Tool Classification

Three tiers, following Claude Code's approach:

| Tier | Tools | Action |
|------|-------|--------|
| **Prunable** | `bash`, `grep`, `glob`, `view`, `ls`, `sourcegraph`, `fetch`, `agentic_fetch`, `crush_info`, `crush_logs` | Replace `ToolResult.Content` with `"[tool output pruned]"` |
| **Clearable use** | `edit`, `multiedit`, `write` | Clear `ToolCall.Input` to `"{}"`, leave `ToolResult` intact |
| **Protected** | `agent`, `job_output`, `job_kill`, `download`, `todos`, `lsp_*`, all MCP tools | No modification |

**Rationale:**

- Prunable tools produce large, one-shot outputs (file contents, search results, command output). The LLM still knows "what was done" from the `ToolCall` parameters even after the output is pruned.
- Edit tools have large `ToolCall.Input` (old_string + new_string) but small, useful `ToolResult` (confirmation messages). Clearing only the input saves tokens without losing useful context.
- Protected tools affect control flow, state, or have unpredictable behavior (MCP).

## Protection Strategy

Triple protection to avoid pruning recent, potentially useful outputs:

1. **Turn protection**: Skip the most recent 2 user-message turns. LLMs most likely reference recent tool outputs.
2. **Token protection**: Protect the most recent 40K estimated tokens of tool output (accumulated from newest to oldest). This prevents pruning a recent large output just because it exceeds the turn boundary.
3. **Summary boundary**: Stop scanning at summary messages (`IsSummaryMessage == true`). Anything before a summary has already been condensed.

## Algorithm

```
1. Build toolCallID → msgIndex lookup (for clearableToolUses backtracking)
2. Reverse-scan messages from newest to oldest:
   a. Count user messages as turns
   b. If turns < 2: skip (turn protection)
   c. If assistant message is summary: stop scanning
   d. For each ToolResult in tool-role messages:
      - If tool not in prunableTools or clearableToolUses: skip
      - Estimate tokens (len(content) / 4)
      - Accumulate into running total
      - If total > 40K (outside protection zone): add to prune targets
3. If total freed tokens < 5K (PruneMinimum): return, not worth it
4. Execute pruning in-memory:
   a. prunableTools: Content → "[tool output pruned]", Data → ""
   b. clearableToolUses: find ToolCall via lookup, Input → "{}"
5. Return freed token estimate
```

## Integration Point

**File**: `internal/agent/agent.go`, in `Run()`, between `getSessionMessages()` and `preparePrompt()`:

```go
msgs, err := a.getSessionMessages(ctx, currentSession)
// ... error handling ...

prune(msgs)  // in-memory, best-effort, errors ignored

history, files := a.preparePrompt(msgs, call.Attachments...)
```

The `prune()` call is best-effort. If it fails or frees nothing, `Run()` proceeds normally with unmodified messages.

## Token Estimation

```go
func estimateTokens(text string) int {
    return len(text) / 4
}
```

Rough but sufficient. The PruneMinimum buffer absorbs estimation errors. Worst case: over-estimate means we prune slightly less than optimal; under-estimate means we prune slightly more. Both are harmless.

## Files Changed

| File | Change |
|------|--------|
| `internal/agent/prune.go` | **New**: ~100 lines. `prune()`, `estimateTokens()`, tool classification maps, constants. |
| `internal/agent/agent.go` | **Modified**: Add 1-line `prune(msgs)` call between `getSessionMessages()` and `preparePrompt()`. |

No changes to `content.go`, database schema, or any other files.

## Edge Cases

- **Empty conversation**: `prune()` scans nothing, returns immediately.
- **Short conversation** (< 2 turns): Everything is within the turn protection zone, nothing is pruned.
- **Conversation with summary**: Prune stops at the summary boundary. Post-summary messages are evaluated normally.
- **Tool result with error** (`IsError=true`): Still eligible for pruning — error messages can also be large.
- **MCP tools**: Not in either classification map, so never pruned. Correct default behavior.
- **Multiple ToolResults in one message**: Each is evaluated independently. The message struct is modified once.

## Future Extensions (not in scope)

- **Pre-truncation**: Cap tool output size at source (`convertToToolResult`).
- **Per-round budget**: Limit total tool output per assistant turn.
- **Time-based trigger**: Prune when prompt cache expires (only for providers with cache).
- **Disk persistence**: Save large outputs to disk with preview replacement.
- **Disable flag**: `CRUSH_DISABLE_PRUNE` environment variable.
