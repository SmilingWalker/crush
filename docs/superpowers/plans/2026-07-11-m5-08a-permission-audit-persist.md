# M5-08a Permission Audit Persist Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Persist permission audit events to the `team_audit_events` SQLite table instead of writing them only to `slog`, so audit records survive restarts and are queryable by team/member/task.

**Architecture:** The `PermissionBridge` already fires `PermAuditEvent`s via its `PermAuditFunc` callback at 6 call sites (1 in the bridge grant-auto path, 5 in the FSM resolve/expire/cancel/orphan paths). Today `app.go` implements that callback as `slog.Info`. We change the callback to open a short transaction, convert `PermAuditEvent` → `AuditEvent` via a new `PermEventToAuditEvent` helper, and call `AuditStore.AppendAudit`. Failures are best-effort (slog.Error, no impact on the permission decision). Three new fields (`WorkspaceID`, `SessionID`, `ToolCallID`) are added to `PermAuditEvent` so the persisted row is complete.

**Tech Stack:** Go 1.26, `database/sql`, `log/slog`, `github.com/google/uuid`, existing `internal/team` permission/audit plumbing.

## Global Constraints

- `-race` is broken (cgo toolchain fault) — tests use `-count=3` jitter, not `-race`.
- `go build ./...` is authoritative (gopls in worktrees often misreports).
- SQLite `:memory:` test fixtures MUST `SetMaxOpenConns(1)` + use `runTx` pattern (per-connection `:memory:` isolation guard).
- Single-line conventional commit, no trailer (no Co-Authored-By, no Signed-off-by).
- One commit per task.
- workspaceID is the hardcoded string `"default"` (matches `internal/team/leader_tools.go:68` convention; no dynamic workspace discovery exists yet).

---

## File Map

| File | Responsibility | Action |
|------|----------------|--------|
| `internal/team/permission_bridge.go` | `PermAuditEvent` struct, `PermissionBridge` wiring, grant-auto audit site, `PermEventToAuditEvent` converter | Modify |
| `internal/team/permission_fsm.go` | 5 FSM audit call sites (allowed/denied/expired/canceled/orphaned) | Modify |
| `internal/team/permission_bridge_test.go` | Unit tests for converter + existing audit assertion updates | Modify |
| `internal/app/app.go` | `SetAuditFunc` implementation: DB-backed persist instead of slog; store `auditStore` reference | Modify |
| `internal/app/app_team_test.go` | E2E persist test: audit row lands in `team_audit_events` | Modify |

---

## Task 1: Add fields to PermAuditEvent + workspaceID to PermissionBridge

**Files:**
- Modify: `internal/team/permission_bridge.go`

**Interfaces:**
- Produces: `PermAuditEvent` gains `WorkspaceID string`, `SessionID string`, `ToolCallID string`. `NewPermissionBridge` gains a `workspaceID string` parameter. `PermissionBridge` gains a `workspaceID string` field.

- [ ] **Step 1: Add fields to PermAuditEvent struct**

In `internal/team/permission_bridge.go`, find the struct (around line 60) and add three fields. The final struct:

```go
// PermAuditEvent records a permission-related event for the audit trail.
type PermAuditEvent struct {
	WorkspaceID string
	SessionID   string
	ToolCallID  string
	Action      PermAuditAction
	TeamID      string
	MemberID    string
	TaskID      string
	RunID       string
	ToolName    string
	Resource    string
	Decision    string
	Scope       string
	DecidedBy   string
	Timestamp   time.Time
}
```

- [ ] **Step 2: Add workspaceID field to PermissionBridge struct**

Find `type PermissionBridge struct {` (around line 217). Add `workspaceID string` as the first field:

```go
type PermissionBridge struct {
	workspaceID string
	inner       permission.Service
	store       *PermissionStore
	// ...rest unchanged
```

- [ ] **Step 3: Update NewPermissionBridge signature**

Find `func NewPermissionBridge(inner permission.Service) *PermissionBridge` (around line 241). Change to:

```go
func NewPermissionBridge(workspaceID string, inner permission.Service) *PermissionBridge {
```

And in the constructor body, set the field. Find the struct literal that builds the bridge (the `bridge := &PermissionBridge{...}` block) and add `workspaceID: workspaceID,` as the first field.

- [ ] **Step 4: Update the grant-auto audit call site to include new fields**

Find the `auditFn` call at the grant-auto path (around line 309). It currently looks like:

```go
b.auditFn(ctx, PermAuditEvent{
	Action: PermAuditGrantAuto, TeamID: ac.TeamID, MemberID: ac.MemberID,
	ToolName: opts.ToolName, Decision: "allowed", Scope: grant.Scope, Timestamp: time.Now(),
})
```

Change to include the three new fields:

```go
b.auditFn(ctx, PermAuditEvent{
	WorkspaceID: b.workspaceID, SessionID: opts.SessionID, ToolCallID: opts.ToolCallID,
	Action: PermAuditGrantAuto, TeamID: ac.TeamID, MemberID: ac.MemberID,
	ToolName: opts.ToolName, Decision: "allowed", Scope: grant.Scope, Timestamp: time.Now(),
})
```

- [ ] **Step 5: Verify it compiles (callers of NewPermissionBridge will break — fix temporarily)**

The `NewPermissionBridge` callers are in `internal/app/app.go:178` and `internal/team/permission_bridge_test.go` (multiple). For now, pass `"default"` as workspaceID at every call site. Search for `NewPermissionBridge(` across the repo and add `"default"` as the first argument.

Run: `go build ./internal/team/...`
Expected: compile error-free.

Run: `go test ./internal/team/... -run "PermissionBridge" -count=1`
Expected: PASS (existing tests should still pass — they don't assert on the new fields yet).

- [ ] **Step 6: Commit**

```bash
git add internal/team/permission_bridge.go
git commit -m "feat(team): add WorkspaceID/SessionID/ToolCallID to PermAuditEvent + workspaceID to PermissionBridge"
```

---

## Task 2: Backfill SessionID/ToolCallID/WorkspaceID at the 5 FSM audit sites

**Files:**
- Modify: `internal/team/permission_fsm.go`

**Interfaces:**
- Consumes: `permReq` (a `PermissionRequest`) at each FSM audit site already carries `WorkspaceID`, `SessionID`, `ToolCallID`, `TaskID`, `RunID`, `MemberID`, `TeamID`, `ToolName`, `ResourceRef`.
- Produces: all 5 FSM `auditFn` calls emit complete `PermAuditEvent`s.

- [ ] **Step 1: Update the permission_allowed audit site**

Find the `auditFn` call under `case "allowed":` (around line 88). It currently:

```go
fsm.auditFn(ctx, PermAuditEvent{
	Action: PermAuditPermissionAllowed, TeamID: permReq.TeamID, MemberID: permReq.MemberID,
	TaskID: permReq.TaskID, RunID: permReq.RunID, ToolName: permReq.ToolName,
	Resource: permReq.ResourceRef, Decision: "allowed", Scope: scope, DecidedBy: req.DecidedBy,
	Timestamp: now,
})
```

Change to:

```go
fsm.auditFn(ctx, PermAuditEvent{
	WorkspaceID: permReq.WorkspaceID, SessionID: permReq.SessionID, ToolCallID: permReq.ToolCallID,
	Action: PermAuditPermissionAllowed, TeamID: permReq.TeamID, MemberID: permReq.MemberID,
	TaskID: permReq.TaskID, RunID: permReq.RunID, ToolName: permReq.ToolName,
	Resource: permReq.ResourceRef, Decision: "allowed", Scope: scope, DecidedBy: req.DecidedBy,
	Timestamp: now,
})
```

- [ ] **Step 2: Update the permission_denied audit site**

Find the `auditFn` call under `case "denied":` (around line 101). Add the same three fields (`WorkspaceID: permReq.WorkspaceID, SessionID: permReq.SessionID, ToolCallID: permReq.ToolCallID,`) as the first line inside the struct literal, before `Action:`.

- [ ] **Step 3: Update the permission_expired audit site**

Find the `auditFn` call in the `Expire` method (around line 125). Add the three fields from `permReq`:

```go
fsm.auditFn(ctx, PermAuditEvent{
	WorkspaceID: permReq.WorkspaceID, SessionID: permReq.SessionID, ToolCallID: permReq.ToolCallID,
	Action: PermAuditPermissionExpired, TeamID: permReq.TeamID, MemberID: permReq.MemberID,
	// ...rest unchanged
})
```

- [ ] **Step 4: Update the permission_canceled audit site**

Find the `auditFn` call in the `Cancel` method (around line 140). Add the three fields from `req` (the loop variable, a `PermissionRequest`):

```go
fsm.auditFn(ctx, PermAuditEvent{
	WorkspaceID: req.WorkspaceID, SessionID: req.SessionID, ToolCallID: req.ToolCallID,
	Action: PermAuditPermissionCanceled, TeamID: req.TeamID, MemberID: req.MemberID,
	// ...rest unchanged
})
```

- [ ] **Step 5: Update the permission_orphaned audit site**

Find the `auditFn` call in the `Orphan` method (around line 155). Add the three fields from `req`:

```go
fsm.auditFn(ctx, PermAuditEvent{
	WorkspaceID: req.WorkspaceID, SessionID: req.SessionID, ToolCallID: req.ToolCallID,
	Action: PermAuditPermissionOrphaned, TeamID: req.TeamID, MemberID: req.MemberID,
	// ...rest unchanged
})
```

- [ ] **Step 6: Verify build + tests**

Run: `go build ./internal/team/...`
Expected: PASS.

Run: `go test ./internal/team/... -run "Permission|FSM|Grant" -count=1`
Expected: PASS (existing tests don't assert new fields yet — that's Task 5).

- [ ] **Step 7: Commit**

```bash
git add internal/team/permission_fsm.go
git commit -m "feat(team): backfill WorkspaceID/SessionID/ToolCallID at FSM audit call sites"
```

---

## Task 3: Add PermEventToAuditEvent converter

**Files:**
- Modify: `internal/team/permission_bridge.go`
- Modify: `internal/team/permission_bridge_test.go`

**Interfaces:**
- Produces: `PermEventToAuditEvent(e PermAuditEvent) AuditEvent` — exported converter.
- Consumes: `strPtrOrNil` (existing helper in the `team` package, used throughout `store_audit.go`).

- [ ] **Step 1: Write the failing test**

In `internal/team/permission_bridge_test.go`, add this test at the end of the file:

```go
func TestPermEventToAuditEvent_FullMapping(t *testing.T) {
	e := PermAuditEvent{
		WorkspaceID: "ws-1",
		SessionID:   "sess-1",
		ToolCallID:  "call-1",
		Action:      PermAuditPermissionAllowed,
		TeamID:      "team-1",
		MemberID:    "member-1",
		TaskID:      "task-1",
		RunID:       "run-1",
		ToolName:    "write",
		Decision:    "allowed",
		Scope:       "task",
		DecidedBy:   "user",
		Timestamp:   time.Date(2026, 7, 11, 12, 0, 0, 0, time.UTC),
	}

	got := PermEventToAuditEvent(e)

	require.Equal(t, "ws-1", got.WorkspaceID)
	require.Equal(t, "team-1", got.TeamID)
	require.Equal(t, "permission.permission_allowed", got.EventType)
	require.NotNil(t, got.Action)
	require.Equal(t, "permission_allowed", *got.Action)
	require.NotNil(t, got.MemberID)
	require.Equal(t, "member-1", *got.MemberID)
	require.NotNil(t, got.SessionID)
	require.Equal(t, "sess-1", *got.SessionID)
	require.NotNil(t, got.ToolCallID)
	require.Equal(t, "call-1", *got.ToolCallID)
	require.NotNil(t, got.ResourceType)
	require.Equal(t, "tool", *got.ResourceType)
	require.NotNil(t, got.ResourceRef)
	require.Equal(t, "write", *got.ResourceRef)
	require.NotNil(t, got.Decision)
	require.Equal(t, "allowed", *got.Decision)
	require.NotNil(t, got.Scope)
	require.Equal(t, "task", *got.Scope)
	require.NotNil(t, got.Summary)
	require.Equal(t, "decided_by:user", *got.Summary)
	require.Equal(t, e.Timestamp, got.CreatedAt)
	require.NotEmpty(t, got.ID)
}

func TestPermEventToAuditEvent_EmptyFieldsBecomeNil(t *testing.T) {
	e := PermAuditEvent{
		WorkspaceID: "ws-1",
		TeamID:      "team-1",
		Action:      PermAuditPermissionExpired,
		Timestamp:   time.Now(),
		// MemberID, SessionID, TaskID, etc. all empty
	}

	got := PermEventToAuditEvent(e)

	require.Nil(t, got.MemberID)
	require.Nil(t, got.SessionID)
	require.Nil(t, got.ToolCallID)
	require.Nil(t, got.TaskID)
	require.Nil(t, got.RunID)
	require.Nil(t, got.Summary) // DecidedBy empty → Summary nil
	require.Nil(t, got.ResourceRef) // ToolName empty → ResourceRef nil
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/team/... -run "TestPermEventToAuditEvent" -count=1 -v`
Expected: FAIL with "undefined: PermEventToAuditEvent".

- [ ] **Step 3: Write the converter**

In `internal/team/permission_bridge.go`, add this function at the end of the file (after the existing `Publish` method):

```go
// PermEventToAuditEvent converts a PermAuditEvent into an AuditEvent row
// suitable for AppendAudit. DecidedBy is stored in Summary (prefixed
// "decided_by:") because AuditEvent has no dedicated column for it.
// ToolName is split into ResourceType="tool" + ResourceRef=ToolName.
func PermEventToAuditEvent(e PermAuditEvent) AuditEvent {
	action := string(e.Action)
	summary := ""
	if e.DecidedBy != "" {
		summary = "decided_by:" + e.DecidedBy
	}
	return AuditEvent{
		ID:           uuid.New().String(),
		WorkspaceID:  e.WorkspaceID,
		TeamID:       e.TeamID,
		MemberID:     strPtrOrNil(e.MemberID),
		TaskID:       strPtrOrNil(e.TaskID),
		RunID:        strPtrOrNil(e.RunID),
		SessionID:    strPtrOrNil(e.SessionID),
		ToolCallID:   strPtrOrNil(e.ToolCallID),
		EventType:    "permission." + action,
		Action:       strPtrOrNil(action),
		ResourceType: strPtrOrNil("tool"),
		ResourceRef:  strPtrOrNil(e.ToolName),
		Decision:     strPtrOrNil(e.Decision),
		Scope:        strPtrOrNil(e.Scope),
		Summary:      strPtrOrNil(summary),
		CreatedAt:    e.Timestamp,
	}
}
```

Ensure `github.com/google/uuid` is imported (it already is — check the import block).

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/team/... -run "TestPermEventToAuditEvent" -count=1 -v`
Expected: PASS (both subtests).

- [ ] **Step 5: Commit**

```bash
git add internal/team/permission_bridge.go internal/team/permission_bridge_test.go
git commit -m "feat(team): add PermEventToAuditEvent converter for audit persistence"
```

---

## Task 4: Wire auditFunc to write DB in app.go

**Files:**
- Modify: `internal/app/app.go`

**Interfaces:**
- Consumes: `team.PermEventToAuditEvent`, `team.AuditStore.AppendAudit`, `team.AuditEvent` from Task 3.
- Produces: `app.permBridge.SetAuditFunc` backed by SQLite persistence.

- [ ] **Step 1: Store auditStore reference on App struct**

Find the `type App struct {` definition (around line 35). After the `permBridge` field block (ends around line 100), add:

```go
	// auditStore writes team permission audit events to team_audit_events.
	// Backs the permBridge.SetAuditFunc callback (M5-08a).
	auditStore team.AuditStore
```

- [ ] **Step 2: Assign auditStore in New()**

Find the line `team.NewAuditStore(q),` inside the `team.NewService(...)` call (around line 199). That line creates the store inline but doesn't save it. Before the `team.NewService(...)` call, extract it:

Find this block (the `team.NewService` call starting around line 196):

```go
		team.NewRunStore(q), team.NewEventStore(q), team.NewAuditStore(q),
```

Change to assign the audit store to a local variable first. Find the full `team.NewService(...)` call — it spans multiple lines. Add before it:

```go
	auditStore := team.NewAuditStore(q)
```

And replace `team.NewAuditStore(q),` inside the `NewService` call arguments with `auditStore,`.

Then in the `app := &App{...}` struct literal (find where `permBridge:` is assigned, around line 178), add:

```go
		auditStore: auditStore,
```

- [ ] **Step 3: Replace the slog-only SetAuditFunc with DB-backed implementation**

Find the current `SetAuditFunc` block (around line 179):

```go
	app.permBridge.SetAuditFunc(func(ctx context.Context, e team.PermAuditEvent) {
		slog.Info("team permission audit",
			"action", e.Action,
			"team_id", e.TeamID,
			"member_id", e.MemberID,
			"tool", e.ToolName,
			"decision", e.Decision,
		)
	})
```

Replace with:

```go
	app.permBridge.SetAuditFunc(func(ctx context.Context, e team.PermAuditEvent) {
		// Best-effort persistence: a failure here must not affect the
		// permission decision that already happened. Log and move on.
		tx, err := app.db.BeginTx(ctx, nil)
		if err != nil {
			slog.Error("Failed to begin permission audit tx",
				"action", e.Action, "team_id", e.TeamID, "error", err)
			return
		}
		defer tx.Rollback()
		if err := app.auditStore.AppendAudit(ctx, tx, team.PermEventToAuditEvent(e)); err != nil {
			slog.Error("Failed to persist permission audit",
				"action", e.Action, "team_id", e.TeamID, "error", err)
			return
		}
		if err := tx.Commit(); err != nil {
			slog.Error("Failed to commit permission audit",
				"action", e.Action, "error", err)
		}
	})
```

Note: `app.db` must exist on the App struct. Verify it does — if not, add `db *sql.DB` to the struct and assign it from the `conn` parameter in `New()`. Check the existing struct for a `db` field first.

- [ ] **Step 4: Verify app.db field exists or add it**

Run this check:

```bash
grep -n "app\.db\|db \*sql\.DB\|db *sql" internal/app/app.go | head -5
```

If `app.db` is NOT already a field, add it: in `type App struct`, add `db *sql.DB`, and in `New()`'s struct literal add `db: conn,`.

- [ ] **Step 5: Verify build**

Run: `go build ./internal/app/...`
Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add internal/app/app.go
git commit -m "feat(app): persist permission audit to team_audit_events via AppendAudit"
```

---

## Task 5: E2E test — audit row lands in DB

**Files:**
- Modify: `internal/app/app_team_test.go`

**Interfaces:**
- Consumes: `team.PermissionBridge`, `team.AuditStore.ListAudit`, real SQLite from app test fixtures.

- [ ] **Step 1: Check existing app_team_test.go fixtures**

Run: `head -50 internal/app/app_team_test.go`

Identify how existing tests construct an `*App` with a real SQLite DB (the test helper that creates `app.New(...)` with a `:memory:` DB and `SetMaxOpenConns(1)`). Note the helper function name — you'll reuse it.

- [ ] **Step 2: Write the failing test**

Add this test to `internal/app/app_team_test.go` (adapt the helper name from Step 1 — if the helper is named `newTestApp` use that; otherwise use whatever exists):

```go
func TestPermissionAudit_PersistsToDB(t *testing.T) {
	app := newTestApp(t) // reuse the existing helper that builds *App with :memory: SQLite

	// Simulate the bridge firing an audit event the way the FSM would.
	bridge := app.PermBridge()
	require.NotNil(t, bridge)

	// Fire a grant_auto audit directly through the bridge's auditFn by
	// calling a path that triggers it. The simplest: use the exported
	// PermEventToAuditEvent + AppendAudit path via the callback the app
	// registered. We invoke the callback through a team-context Request
	// — but for a focused DB-persist test, exercise the callback directly
	// via a helper if exposed, otherwise drive a Request.
	//
	// Pragmatic approach: the app registered SetAuditFunc. We can't call
	// the private callback directly. Instead, trigger it via the bridge
	// by making a team-context permission request that hits the grant-auto
	// path. That requires a grant to exist. Simpler: use a test-only seam.

	// TODO: if no test seam exists, this test drives a full Request flow.
	// For now, verify the callback was wired by checking ListAudit is empty
	// before any event, and non-empty after the bridge processes a request.

	// See Task 5 Step 3 for the concrete approach based on what seams exist.
}
```

- [ ] **Step 3: Determine the concrete test approach**

The clean way to test the DB persist is to drive a real `bridge.Request(ctx, opts)` call with a team actor context, let it fire `auditFn`, then call `ListAudit`. Check whether `team.PermissionBridge` exposes a way to inject a team actor context into `ctx` for tests:

Run: `grep -n "actor.WithTeam\|ActorContext\|FromContext\|actor.New" internal/team/permission_bridge.go internal/actor/*.go | head -10`

If `actor.WithTeam(ctx, ...)` or similar exists, the test sets up a grant, calls `Request`, and asserts `ListAudit` returns a row. If driving a full Request is too heavy (requires UI interaction), instead add a tiny test seam: export the auditFunc callback for tests by storing it on the bridge and calling it directly.

- [ ] **Step 4: Implement the test concretely**

Based on Step 3's finding, write the actual test. The recommended minimal seam (if full Request is too heavy): the test fires the audit callback the app registered, then checks `ListAudit`. To do this without exposing private state, use the real `Request` path with `SkipRequests=true` (which auto-allows and fires `grant_auto` audit):

```go
func TestPermissionAudit_PersistsToDB(t *testing.T) {
	app := newTestApp(t)
	bridge := app.PermBridge()
	require.NotNil(t, bridge)

	// grant_auto audit fires when an active grant exists and a matching
	// request comes in. Seed a grant via the GrantStore, then make a
	// request with a team actor context.
	ctx := actor.WithTeamContext(t.Context(), actor.ActorContext{
		TeamID:   "team-1",
		MemberID: "member-1",
	})
	bridge.GrantStore().CreateGrant(ctx, Grant{
		// ...minimal grant matching the request below
	})

	// SkipRequests=true auto-allows without UI popup, firing grant_auto audit.
	app.Permissions.SetSkipRequests(true)

	allowed, err := bridge.Request(ctx, permission.CreatePermissionRequest{
		ToolName:  "write",
		ToolCallID: "call-1",
		SessionID: "sess-1",
	})
	require.NoError(t, err)
	require.True(t, allowed)

	// Verify the audit row landed.
	rows, err := app.auditStore.ListAudit(ctx, /*tx or direct*/, "team-1", 10)
	require.NoError(t, err)
	require.Len(t, rows, 1)
	require.Equal(t, "permission.grant_auto", rows[0].EventType)
}
```

Adapt based on: whether `GrantStore()` is exported, whether `actor.WithTeamContext` exists, and whether `ListAudit` can be called without a `*sql.Tx` (it may need one — use the app's DB to begin a tx).

- [ ] **Step 5: Run test to verify it fails (if red), then pass**

Run: `go test ./internal/app/... -run "TestPermissionAudit_PersistsToDB" -count=1 -v`

If it fails on a missing seam (e.g. `GrantStore()` not exported), add the minimal export in `permission_bridge.go` — a one-line accessor `func (b *PermissionBridge) GrantStore() *GrantStore { return b.grantStore }`.

- [ ] **Step 6: Commit**

```bash
git add internal/app/app_team_test.go internal/team/permission_bridge.go
git commit -m "test(app): verify permission audit persists to team_audit_events"
```

---

## Task 6: Update existing audit assertion tests + final verification

**Files:**
- Modify: `internal/team/permission_bridge_test.go`

- [ ] **Step 1: Update tests that assert on PermAuditEvent fields**

Existing tests like `TestPermissionBridge_TeamSessionShowsPopupWhenSkipOff`, `TestPermissionBridge_TimeoutDeniesAndCleansUp` etc. capture audit events via a test `PermAuditFunc`. Find them:

Run: `grep -n "auditFn\|PermAuditEvent\|capturedAudit\|auditEvents" internal/team/permission_bridge_test.go`

For each captured event, add assertions for the new fields:

```go
require.Equal(t, "default", captured.WorkspaceID)
require.Equal(t, "sess-test", captured.SessionID)
require.Equal(t, "call-test", captured.ToolCallID)
```

Use the sessionID/toolCallID that the test already passes into `CreatePermissionRequest`. If a test doesn't set them, add them to the request.

- [ ] **Step 2: Run full team test suite**

Run: `go test ./internal/team/... -count=1`
Expected: PASS.

- [ ] **Step 3: Run full app test suite**

Run: `go test ./internal/app/... -count=1`
Expected: PASS.

- [ ] **Step 4: Run full build + vet**

Run: `go build ./... && go vet ./internal/team/... ./internal/app/...`
Expected: PASS (only pre-existing csync warning acceptable).

- [ ] **Step 5: Commit**

```bash
git add internal/team/permission_bridge_test.go
git commit -m "test(team): assert WorkspaceID/SessionID/ToolCallID in audit event captures"
```

---

## Self-Review Notes

**Spec coverage:**
- ✅ SetAuditFunc writes team_audit_events (Task 4)
- ✅ 6 已触发 action 字段完整 (Tasks 1+2)
- ✅ audit 落盘失败只 slog.Error (Task 4 — best-effort pattern)
- ✅ ListAudit 能查到 permission audit 行 (Task 5)
- ✅ go build + go test 通过 (Task 6 final verification)

**Known soft spot:** Task 5 Step 3/4 has a conditional ("if GrantStore() not exported, add accessor"). This is flagged because I could not verify `GrantStore()` export status without running code. The implementer should check and add the one-liner if needed — it's a trivial, well-scoped addition.
