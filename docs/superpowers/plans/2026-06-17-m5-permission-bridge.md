# M5: Permission Bridge — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Give members productive tools + permission control. Members can do real work (bash, read, write, edit) with leader approval.

**Architecture:** Phase 0 fixes the tool gap (AgentSpec.ToolPolicy in SpawnMember). Tasks 1-5 implement the core PermissionBridge based on original M5 plan at `docs/agent-team-mode/plan/tasks/06-m5-permission-bridge.md`.

**Tech Stack:** Go, sqlc, `permission.Service`, `fantasy.AgentTool`, bubbletea dialog.

---

## Phase 0: Tool Policy Fix

### Task 0: Set AgentSpec.ToolPolicy in SpawnMember

**File:** `internal/team/team_runner.go`

In `SpawnMember`, the `AgentSpec` is currently empty. Add `ToolPolicy` so members get the full standard toolset.

- [ ] **Step 1: Read current SpawnMember AgentSpec**

In `team_runner.go`, find `spec := agent.AgentSpec{}` (around line 145 in StartTeam, and similar in SpawnMember).

- [ ] **Step 2: Set ToolPolicy**

```go
spec := agent.AgentSpec{
    ToolPolicy: agent.ToolPolicyProfile{
        AllowedTools: nil,  // nil = all built-in tools (bash, read, write, edit, grep, glob, etc.)
    },
    MaxTurns: 50,
}
```

- [ ] **Step 3: Verify build + tests**

```bash
go build ./... && go test -count=1 -timeout 60s ./internal/team/...
```

- [ ] **Step 4: Commit**

```bash
git add internal/team/team_runner.go
git commit -m "feat(team): set ToolPolicy in SpawnMember so members get standard tools"
```

---

## M5 Core: PermissionBridge

Based on original M5 plan (`docs/agent-team-mode/plan/tasks/06-m5-permission-bridge.md`).

### Task 1: Permission Bridge + Store

**Files:** `internal/team/permission_bridge.go` (NEW), `internal/team/permission_store.go` (NEW)

- [ ] **Step 1: PermissionBridge struct + interface**

```go
type PermissionBridge struct {
    inner      permission.Service
    store      *PermissionStore
    grantStore *GrantStore
    auditFn    AuditFunc
}

func (b *PermissionBridge) Request(ctx context.Context, opts permission.CreatePermissionRequest) (bool, error) {
    // If not a team session, delegate to inner
    ac, hasTeam := actor.FromContext(ctx)
    if !hasTeam || ac.TeamID == "" {
        return b.inner.Request(ctx, opts)
    }
    // Check existing grants
    if grant, ok := b.grantStore.FindActiveGrant(ctx, ac, opts); ok {
        b.auditFn(ctx, AuditEvent{Action: "grant_auto", ...})
        return true, nil
    }
    // Create permission request, wait for UI decision
    req := b.store.CreateRequest(ctx, ac, opts)
    return b.waitDecision(ctx, req.ID)
}
```

- [ ] **Step 2: PermissionStore** — DB operations for `team_permission_requests` and `team_permission_grants` tables. Following original M5 schema (M5-02, M5-03).

- [ ] **Step 3: Verify build**

```bash
go build ./...
```

- [ ] **Step 4: Commit**

```bash
git add internal/team/permission_bridge.go internal/team/permission_store.go
git commit -m "feat(team): add PermissionBridge + PermissionStore for M5"
```

---

### Task 2: Permission FSM (State Machine)

**File:** `internal/team/permission_fsm.go` (NEW)

- [ ] **Step 1: State transitions**

```
pending → allowed (user approves) → grant created
pending → denied (user rejects) → member blocked
pending → expired (timeout) → member blocked
pending → canceled (run cancel) → cascade
pending → orphaned (startup recovery) → orphaned
allowed → (grant created with scope=call|task|session)
```

- [ ] **Step 2: Resolve method**

```go
func (fsm *PermissionFSM) Resolve(ctx context.Context, req ResolveRequest) error {
    run, _ := fsm.svc.GetRun(ctx, req.RunID)
    if run.IsTerminal() {
        fsm.auditFn(ctx, AuditEvent{Action: "late_response", ...})
        return nil // late response: audit only, no grant
    }
    if req.Decision == "allowed" && req.Scope == "task" {
        fsm.grantStore.CreateGrant(ctx, Grant{...})
    }
    // ...
}
```

- [ ] **Step 3: Verify build + tests**

```bash
go build ./... && go test -count=1 -timeout 30s ./internal/team/permission_fsm_test.go
```

- [ ] **Step 4: Commit**

---

### Task 3: Permission Queue + Timeout

**File:** `internal/team/permission_queue.go` (NEW)

- [ ] **Step 1: Queue implementation**

```go
type PermissionQueue struct {
    maxPending int           // default 3
    defaultTTL time.Duration // default 5 min
    pending    []*PermissionRequest
    mu         sync.Mutex
}

func (q *PermissionQueue) Enqueue(req *PermissionRequest) error  // fail if >= maxPending
func (q *PermissionQueue) StartTimeout(req *PermissionRequest)    // time.AfterFunc → expire
func (q *PermissionQueue) Dequeue(reqID string)                   // remove on resolve
```

- [ ] **Step 2: Verify build + tests**

- [ ] **Step 3: Commit**

---

### Task 4: Permission UI (Dialog extension)

**Files:** `internal/ui/dialog/permission.go` (MODIFY), `internal/ui/dialog/` related files

- [ ] **Step 1: Extend existing permission dialog with team context**

Show: member name, task title, run ID, tool name, resource path.

Add buttons: "Allow Once", "Allow for Task", "Deny".

- [ ] **Step 2: Wire into existing dialog system**

Use existing `dialog.Overlay` + `dialog.Dialog` interface.

- [ ] **Step 3: Verify build + tests**

- [ ] **Step 4: Commit**

---

### Task 5: Hook Audit + E2E

**Files:** `internal/agent/tools/hooked_tool.go` (MODIFY), test files

- [ ] **Step 1: Modify hooked_tool.go to write team audit on allow/deny**

```go
// In team mode: hook allow → team audit event
// In team mode: hook deny → team audit event
```

- [ ] **Step 2: E2E test**

```
TestM5_MemberRequestPermission_UserAllows: member write → pending → allow → tool proceeds
TestM5_MemberRequestPermission_UserDenies: member edit → pending → deny → blocked
TestM5_AllowTaskScope: allow for task → same task auto-approve
TestM5_NonTeamSessionUnchanged: non-team → existing flow unchanged
```

- [ ] **Step 3: Full build + test**

```bash
go build ./... && go test -count=1 -timeout 120s ./...
```

- [ ] **Step 4: Commit**

---

## Verification

1. `go build ./...` — no errors
2. Team tests pass (excluding known flake)
3. Member with ToolPolicy → calls bash/read/write → multi-turn work
4. Permission dialog shows team context
5. Allow/deny/expire state transitions work
6. Non-team sessions unchanged
