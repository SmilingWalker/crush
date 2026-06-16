# M4-14: Production AgentFactory

## Goal

Replace the M2-era `SessionAgentFactory` (closure-based, ignores AgentSpec) with a production `CoordinatorAgentFactory` that uses M1 coordinator infrastructure to create real TurnRunner instances from AgentSpec parameters. After this, `MemberRunner.Start()` can build a real agent turn runner that connects to LLM providers.

## Context

- **M4-01** shipped `TurnRunnerAdapter` — bridges `SessionAgent` to `TurnRunner` interface. Works. No changes needed.
- **M4-01** also shipped `SessionAgentFactory` — a minimal `AgentFactory` that ignores `AgentSpec` and wraps a pre-configured `SessionAgent` provider closure. This was the "deferred production AgentFactory" seam from M2.
- **M4-14** replaces `SessionAgentFactory` with `CoordinatorAgentFactory`, which reads `AgentSpec` fields and creates properly-configured `SessionAgent` instances.

## Design Seams (researched + decided)

### Seam 1: How to expose coordinator's agent-building capability

**Researched:** `coordinator.buildAgent()` is private (confirmed M4-01). It creates SessionAgent with models, system prompt, and tools. The method is ~40 lines but calls into complex private helpers (`buildAgentModels` ~85 lines, `buildTools` ~130 lines, `buildProvider` ~50 lines). Duplicating these in a standalone factory would copy ~300 lines of provider/model wiring logic and create a maintenance fork.

**Decision:** Add one public method `BuildSessionAgent(ctx, AgentSpec) (SessionAgent, error)` to the `Coordinator` interface. Implement it on `coordinator` by adapting the existing `buildAgent` path.

Rationale:
- One interface method addition vs. 300 lines copied/adapted
- All provider/model/tool building stays in one place (coordinator)
- The existing `stubCoordinator` in `internal/server/sessions_isbusy_test.go` just needs a one-line stub
- Clean separation: coordinator knows how to build agents, the factory just wraps it

### Seam 2: AgentSpec to SessionAgent configuration mapping

**Researched:** `AgentSpec` fields and their counterparts in the coordinator's build pipeline.

| AgentSpec field | Maps to | Mechanism |
|---|---|---|
| `SystemPrompt string` | System prompt text | Wrap as inline prompt.Prompt; if empty, use default member prompt |
| `ModelType string` | Which models to use | "large" → `SelectedModelTypeLarge`, "small" → `SelectedModelTypeSmall`, empty/"inherit" → same as coder (large) |
| `PermissionMode string` | IsYolo + permission config | "bypassPermissions" → IsYolo=true; others → IsYolo=false |
| `ToolPolicy.AllowedTools` | config.Agent.AllowedTools | Passed through to tool filter (nil = all tools) |
| `ToolPolicy.DisallowedTools` | config.Agent.DisallowedTools | Passed through to tool denylist |
| `MaxTurns int` | config.Agent.MaxTurns | Reserved, not enforced yet at SessionAgent level |

**Decision:** Map AgentSpec → `config.Agent` inside `BuildSessionAgent`, then reuse the existing `buildAgent` code path (adapted to accept the mapped agent config). This is a thin mapping layer, not a new build pipeline.

### Seam 3: IsSubAgent flag

**Decision:** Member agents are NOT sub-agents. `IsSubAgent=false`. This means:
- No sub-agent tool filtering (bash/write/edit are allowed based on ToolPolicy)
- No `DisallowedTools` sub-agent safety filter
- They use the full tool suite as configured by ToolPolicy

### Seam 4: System prompt for member agents

**Researched:** No member-specific prompt template exists. The `task.md.tpl` is used for sub-agents.

**Decision:** Embed a simple default member system prompt (adapted from `task.md.tpl` with team-member context) as a `go:embed` template. When `AgentSpec.SystemPrompt` is non-empty, the factory wraps it as a raw (non-template) prompt. When empty, the default template is used. This gives callers control: `TeamRunner` passes empty spec → gets default; future SpawnMember can pass custom prompts.

### Seam 5: Scope

**Decision:** Only change files under `internal/agent/`. No changes to `internal/team/` (MemberRunner/TeamRunner already call `AgentFactory.BuildRunner` correctly).

## Files to create/modify

### New file: `internal/agent/agent_factory.go`

```go
// CoordinatorAgentFactory implements AgentFactory using M1 coordinator infrastructure.
type CoordinatorAgentFactory struct {
    coordinator Coordinator
}

func NewCoordinatorAgentFactory(c Coordinator) *CoordinatorAgentFactory {
    return &CoordinatorAgentFactory{coordinator: c}
}

func (f *CoordinatorAgentFactory) BuildRunner(ctx context.Context, spec AgentSpec) (TurnRunner, error) {
    sa, err := f.coordinator.BuildSessionAgent(ctx, spec)
    if err != nil {
        return nil, fmt.Errorf("build session agent: %w", err)
    }
    return NewTurnRunnerFromSessionAgent(sa), nil
}
```

### New file: `internal/agent/templates/member.md.tpl`

Default system prompt for team members. Minimal — derived from `task.md.tpl` with member role context.

### Modified: `internal/agent/coordinator.go`

- Add `BuildSessionAgent(ctx context.Context, spec AgentSpec) (SessionAgent, error)` to the `Coordinator` interface
- Implement `coordinator.BuildSessionAgent()` — maps AgentSpec → config.Agent, builds models/tools via existing helpers, creates SessionAgent

### Modified: `internal/server/sessions_isbusy_test.go`

- Add stub method for new interface method on `stubCoordinator`

### New file: `internal/agent/agent_factory_test.go`

- Test: empty AgentSpec → creates valid TurnRunner (uses defaults)
- Test: AgentSpec with ToolPolicy → tools are filtered correctly
- Test: AgentSpec with PermissionMode="bypassPermissions" → IsYolo=true
- Test: AgentSpec with SystemPrompt → custom prompt used
- Test: factory.BuildRunner returns error when coordinator.BuildSessionAgent fails

## Test plan

1. **Unit: CoordinatorAgentFactory.BuildRunner** — valid spec → non-nil TurnRunner
2. **Unit: BuildSessionAgent spec mapping** — verify ToolPolicy → AllowedTools mapping
3. **Unit: Permission mode mapping** — verify "bypassPermissions" sets IsYolo
4. **Integration: stubCoordinator updated** — existing tests still compile
5. **Existing tests pass** — `internal/agent/...`, `internal/team/...`, `internal/server/...` all green

## Implementation order

1. Add member system prompt template (`templates/member.md.tpl`)
2. Add `BuildSessionAgent` to Coordinator interface + implement on coordinator
3. Create `CoordinatorAgentFactory` in `agent_factory.go`
4. Update `stubCoordinator` in server test
5. Write agent_factory_test.go
6. Run full test suite to verify no regressions

## Commit

Single commit, conventional format, no `Co-Authored-By` trailer (per task spec).
