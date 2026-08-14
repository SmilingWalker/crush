# B1+B2 权限生命周期接线 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 把 FSM/store 接进生产权限流：请求落库、UI 决策走 `fsm.Resolve`（scope 生效）、超时 expired、ctx 取消 canceled、FSM audit 落盘、grant 按 task scope 匹配。

**Architecture:** bridge 持有 fsm 实例，四个生命周期钩子（requestWithUI 落库 / ResolveRequest 决策 / handleTimeout 过期 / terminateEntry 取消）各调一次 FSM；exactly-once 依赖 A1 的 `store.Update` pending 检查；两时钟（60s display / 5min TTL）竞态由"store 非 pending 的 UI 决策 → late_response + deny"兜底。

**Tech Stack:** Go 1.26、testify、`-race`（必须保持绿色）。

**Spec:** `docs/superpowers/specs/2026-08-15-b1-b2-lifecycle-wiring-design.md`

## Global Constraints

- 所有 go 命令带 `GOPROXY=https://goproxy.cn,direct` 前缀。
- `go test -race ./internal/team/` 必须始终保持绿色（A 系列建立的基线）。
- 非 team 路径行为不变；`TestFSM_Resolve_AlreadyResolved` 等错误文案断言不得破坏。
- 提交前 `gofmt -w` 改动文件（gofumpt 不在 PATH）。
- 语义化单行 commit；注释整行大写开头句号结尾；日志消息大写开头。
- 不改本计划列出文件之外的文件。

---

### Task 1: FSM 加固——SetAuditFunc、CancelRequest、allow-once 不建 grant

**Files:**
- Modify: `internal/team/permission_fsm.go`
- Modify: `internal/team/permission_fsm_test.go`（更新 call-scope 断言 + 新测试）
- Modify: `internal/team/permission_e2e_test.go`（更新受 call-no-grant 影响的断言，仅此）

**Interfaces:**
- Consumes: 无。
- Produces: `(*PermissionFSM).SetAuditFunc(fn PermAuditFunc)`、`(*PermissionFSM).CancelRequest(ctx, requestID string) error`（幂等，非 pending 返回 nil，not found 返回 error）、非导出 `(*PermissionFSM).audit(ctx, event)` 助手；`scope=="call"` 的 Resolve 不再创建 grant（Task 2-4 依赖此语义）。

- [ ] **Step 1: 写失败测试**

`permission_fsm_test.go` 追加（缺 import `sync` 则补）：

```go
func TestFSM_SetAuditFunc_Propagates(t *testing.T) {
	ps := NewPermissionStore()
	gs := NewGrantStore()
	fsm := NewPermissionFSM(ps, gs, func(ctx context.Context, e PermAuditEvent) {})

	var mu sync.Mutex
	var events []PermAuditEvent
	fsm.SetAuditFunc(func(ctx context.Context, e PermAuditEvent) {
		mu.Lock()
		defer mu.Unlock()
		events = append(events, e)
	})

	ctx := context.Background()
	require.NoError(t, ps.CreateRequest(ctx, &PermissionRequest{
		ID: "saf1", TeamID: "t1", MemberID: "m1", SessionID: "s1",
		ToolName: "bash", Action: "execute", Status: "pending", CreatedAt: time.Now(),
	}))
	require.NoError(t, fsm.Expire(ctx, "saf1"))

	mu.Lock()
	defer mu.Unlock()
	require.Len(t, events, 1)
	assert.Equal(t, PermAuditPermissionExpired, events[0].Action)
}

func TestFSM_CancelRequest_Pending(t *testing.T) {
	fsm, ps, _ := setupFSM(t)
	ctx := context.Background()
	require.NoError(t, ps.CreateRequest(ctx, &PermissionRequest{
		ID: "cr1", TeamID: "t1", MemberID: "m1", Status: "pending", CreatedAt: time.Now(),
	}))
	require.NoError(t, fsm.CancelRequest(ctx, "cr1"))
	got, _ := ps.GetRequest(ctx, "cr1")
	assert.Equal(t, "canceled", got.Status)
}

func TestFSM_CancelRequest_IdempotentAndNotFound(t *testing.T) {
	fsm, ps, _ := setupFSM(t)
	ctx := context.Background()
	require.NoError(t, ps.CreateRequest(ctx, &PermissionRequest{
		ID: "cr2", Status: "allowed", CreatedAt: time.Now(),
	}))
	require.NoError(t, fsm.CancelRequest(ctx, "cr2"), "non-pending must be an idempotent no-op")
	require.Error(t, fsm.CancelRequest(ctx, "nope"), "unknown ID must error")
}
```

`TestFSM_Resolve_Allowed_CallScope`（`permission_fsm_test.go:24-44`）末尾的 grant 断言替换为：

```go
	_, ok := gs.FindActiveGrant(ctx, "s1", "bash", "execute")
	assert.False(t, ok, "allow-once must not create a grant")
```

- [ ] **Step 2: 跑测试确认失败**

Run: `GOPROXY=https://goproxy.cn,direct go test ./internal/team/ -run 'TestFSM_' -v`
Expected: FAIL——`fsm.SetAuditFunc undefined`、`fsm.CancelRequest undefined`（编译错误）。

- [ ] **Step 3: 实现**

`permission_fsm.go`：

struct 与 audit 助手（替换现 struct 定义；`NewPermissionFSM` 不变）：

```go
// PermissionFSM manages the lifecycle of permission requests and grants.
type PermissionFSM struct {
	store      *PermissionStore
	grantStore *GrantStore
	// mu guards auditFn, replaceable after construction via SetAuditFunc.
	mu      sync.Mutex
	auditFn PermAuditFunc
}
```

新增（import 补 `"sync"`）：

```go
// SetAuditFunc replaces the audit callback (propagated from the bridge).
func (fsm *PermissionFSM) SetAuditFunc(fn PermAuditFunc) {
	fsm.mu.Lock()
	defer fsm.mu.Unlock()
	fsm.auditFn = fn
}

// audit emits an event through the current audit callback. The callback runs
// outside fsm.mu so it may persist to storage without holding the lock.
func (fsm *PermissionFSM) audit(ctx context.Context, event PermAuditEvent) {
	fsm.mu.Lock()
	fn := fsm.auditFn
	fsm.mu.Unlock()
	if fn != nil {
		fn(ctx, event)
	}
}
```

全部 5 处 `fsm.auditFn(ctx, PermAuditEvent{...})` 调用改为 `fsm.audit(ctx, ...)`（Resolve×2、Expire、Cancel、Orphan）。

`Resolve` 的 grant 段（现 75-105 行）替换为——call 不建 grant：

```go
	scope := req.Scope
	if scope == "" {
		scope = "call"
	}
	if scope != "call" {
		grant := &Grant{
			ID:              fmt.Sprintf("grant-%d", time.Now().UnixNano()),
			WorkspaceID:     updated.WorkspaceID,
			TeamID:          updated.TeamID,
			MemberID:        updated.MemberID,
			TaskID:          updated.TaskID,
			SessionID:       updated.SessionID,
			ToolName:        updated.ToolName,
			Action:          updated.Action,
			ResourceType:    updated.ResourceType,
			ResourceRef:     updated.ResourceRef,
			Scope:           scope,
			SourceRequestID: updated.ID,
			CreatedAt:       now,
		}
		switch scope {
		case "task":
			grant.ExpiresAt = now.Add(24 * time.Hour)
		case "session":
			grant.ExpiresAt = now.Add(7 * 24 * time.Hour)
		}
		if err := fsm.grantStore.CreateGrant(ctx, grant); err != nil {
			return fmt.Errorf("resolve: create grant: %w", err)
		}
	}
```

新增 `CancelRequest`（放在 `Expire` 之后）：

```go
// CancelRequest marks a single pending request as canceled (e.g. the member's
// context went away). Idempotent: already-resolved requests return nil.
func (fsm *PermissionFSM) CancelRequest(ctx context.Context, requestID string) error {
	updated, err := fsm.store.Update(ctx, requestID, func(r *PermissionRequest) error {
		if r.Status != "pending" {
			return errNotPending
		}
		r.Status = "canceled"
		return nil
	})
	if errors.Is(err, errNotPending) {
		return nil // already resolved — idempotent
	}
	if err != nil {
		return fmt.Errorf("cancel request: %w", err)
	}
	fsm.audit(ctx, PermAuditEvent{
		WorkspaceID: updated.WorkspaceID, SessionID: updated.SessionID, ToolCallID: updated.ToolCallID,
		Action: PermAuditPermissionCanceled, TeamID: updated.TeamID, MemberID: updated.MemberID,
		TaskID: updated.TaskID, RunID: updated.RunID, ToolName: updated.ToolName,
		Timestamp: time.Now(),
	})
	return nil
}
```

然后 `GOPROXY=https://goproxy.cn,direct go test ./internal/team/`——`permission_e2e_test.go` 中所有**call-scope allow 后断言 grant 存在**的用例（grep `FindActiveGrant`，至少 `:48`、`:88` 附近）改为断言无 grant（`assert.False(t, ok, "allow-once must not create a grant")`）；task/session scope 的断言保持。`:161-163` 的过时注释（"not recorded by enqueue alone in current impl"）同步更新为现状描述。

- [ ] **Step 4: 跑测试确认通过（含 -race）**

Run: `GOPROXY=https://goproxy.cn,direct go test -race ./internal/team/ -run 'TestFSM_|TestM5_' -v`
Expected: 全 PASS。

- [ ] **Step 5: 格式化并提交**

```bash
gofmt -w internal/team/permission_fsm.go internal/team/permission_fsm_test.go internal/team/permission_e2e_test.go
git add internal/team/permission_fsm.go internal/team/permission_fsm_test.go internal/team/permission_e2e_test.go
git commit -m "feat(team): FSM audit setter, CancelRequest, and allow-once without grant"
```

---

### Task 2: Bridge 落库（B2）+ audit 传播（B4）

**Files:**
- Modify: `internal/team/permission_bridge.go`
- Test: `internal/team/permission_bridge_test.go`

**Interfaces:**
- Consumes: Task 1 的 `fsm.SetAuditFunc`。
- Produces: bridge struct 新字段 `fsm *PermissionFSM`（Task 3 调 `fsm.Resolve/Expire/CancelRequest` 依赖）；`requestWithUI` 落库且 `teamReq` 字段完整（Task 3/4 依赖）。

- [ ] **Step 1: 写失败测试**

`permission_bridge_test.go` 追加：

```go
func TestBridge_RequestPersistsToStore(t *testing.T) {
	inner := permission.NewPermissionService(t.TempDir(), false, nil)
	bridge := NewPermissionBridge("default", inner)
	bridge.SetRequestTimeout(150 * time.Millisecond)

	ac := actor.ActorContext{
		SessionID: "s-persist", TeamID: "team-p", MemberID: "m-p", TaskID: "task-p", RunID: "run-p",
		MemberName: "m", MemberRole: "programmer",
	}
	done := make(chan bool, 1)
	go func() {
		allowed, _ := bridge.Request(ac.WithContext(t.Context()), permission.CreatePermissionRequest{
			SessionID: "s-persist", ToolCallID: "tc-persist", ToolName: "write",
			Action: "write", Description: "test", Path: "/tmp/persist",
		})
		done <- allowed
	}()

	require.Eventually(t, func() bool {
		_, err := bridge.store.GetRequest(context.Background(), "tc-persist")
		return err == nil
	}, 2*time.Second, 10*time.Millisecond, "request must be persisted while pending")

	got, err := bridge.store.GetRequest(context.Background(), "tc-persist")
	require.NoError(t, err)
	assert.Equal(t, "pending", got.Status)
	assert.Equal(t, "default", got.WorkspaceID)
	assert.Equal(t, "team-p", got.TeamID)
	assert.Equal(t, "m-p", got.MemberID)
	assert.Equal(t, "task-p", got.TaskID)
	assert.Equal(t, "run-p", got.RunID)
	assert.Equal(t, "/tmp/persist", got.ResourceRef)

	<-done // 150ms 超时自然结束（deny）
}

func TestBridge_SetAuditFunc_PropagatesToFSM(t *testing.T) {
	inner := permission.NewPermissionService(t.TempDir(), false, nil)
	bridge := NewPermissionBridge("default", inner)

	var mu sync.Mutex
	var events []PermAuditEvent
	bridge.SetAuditFunc(func(ctx context.Context, e PermAuditEvent) {
		mu.Lock()
		defer mu.Unlock()
		events = append(events, e)
	})

	ctx := context.Background()
	require.NoError(t, bridge.store.CreateRequest(ctx, &PermissionRequest{
		ID: "b4-1", TeamID: "t1", MemberID: "m1", SessionID: "s1",
		ToolName: "bash", Action: "execute", Status: "pending", CreatedAt: time.Now(),
	}))
	require.NoError(t, bridge.fsm.Expire(ctx, "b4-1"))

	mu.Lock()
	defer mu.Unlock()
	require.Len(t, events, 1)
	assert.Equal(t, PermAuditPermissionExpired, events[0].Action)
}
```

- [ ] **Step 2: 跑测试确认失败**

Run: `GOPROXY=https://goproxy.cn,direct go test ./internal/team/ -run 'TestBridge_' -v`
Expected: FAIL——`bridge.fsm undefined`（编译错误）。

- [ ] **Step 3: 实现**

`permission_bridge.go`：

struct `queue` 字段后加 `fsm *PermissionFSM`；构造函数（现 278-280 行）改为：

```go
	fsm := NewPermissionFSM(bridge.store, bridge.grantStore, bridge.auditFn)
	bridge.fsm = fsm
	bridge.queue = NewPermissionQueue(fsm)
```

`SetAuditFunc` 改为：

```go
// SetAuditFunc sets the audit callback for permission events. The callback is
// also propagated to the FSM so lifecycle transitions are audited.
func (b *PermissionBridge) SetAuditFunc(fn PermAuditFunc) {
	b.queueMu.Lock()
	b.auditFn = fn
	b.queueMu.Unlock()
	b.fsm.SetAuditFunc(fn)
}
```

`requestWithUI` 的 `teamReq`（现 401-411 行）补字段，并在其后紧跟落库（在 `queueMu` 注册块**之前**）：

```go
	now := time.Now()
	teamReq := &PermissionRequest{
		ID:          reqID,
		WorkspaceID: b.workspaceID,
		TeamID:      ac.TeamID,
		MemberID:    ac.MemberID,
		TaskID:      ac.TaskID,
		RunID:       ac.RunID,
		SessionID:   opts.SessionID,
		ToolCallID:  reqID,
		ToolName:    opts.ToolName,
		Action:      opts.Action,
		ResourceRef: opts.Path,
		Status:      "pending",
		CreatedAt:   now,
		ExpiresAt:   now.Add(requestTimeout),
	}

	// B2: persist so the FSM lifecycle (resolve/expire/cancel) has a row to
	// transition. In-memory store — failure is log-only.
	if err := b.store.CreateRequest(ctx, teamReq); err != nil {
		slog.Debug("perm_bridge: persist permission request failed", "tool_call_id", reqID, "error", err)
	}
```

- [ ] **Step 4: 跑测试确认通过（含 -race）**

Run: `GOPROXY=https://goproxy.cn,direct go test -race ./internal/team/ -run 'TestBridge_' -v`
Expected: 全 PASS（既有 bridge 测试不受影响——`TestPermissionBridge_ConcurrentSettersAndRequest` 的 SetAuditFunc 现在会传播到 FSM，行为兼容）。

- [ ] **Step 5: 格式化并提交**

```bash
gofmt -w internal/team/permission_bridge.go internal/team/permission_bridge_test.go
git add internal/team/permission_bridge.go internal/team/permission_bridge_test.go
git commit -m "feat(team): persist team permission requests and propagate audit to FSM"
```

---

### Task 3: 生命周期接线（B1+B3）——Resolve/Expire/Cancel 钩子

**Files:**
- Modify: `internal/team/permission_bridge.go`（ResolveRequest、handleTimeout、terminateEntry）
- Create: `internal/team/permission_lifecycle_test.go`

**Interfaces:**
- Consumes: Task 1 的 `CancelRequest`；Task 2 的 `bridge.fsm` 字段与落库。
- Produces: 生产路径完整生命周期（Task 4 的 grant 断言依赖真实 Resolve 流）。

- [ ] **Step 1: 写失败测试（新文件，真实路径）**

`internal/team/permission_lifecycle_test.go`：

```go
package team

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/charmbracelet/crush/internal/actor"
	"github.com/charmbracelet/crush/internal/permission"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// auditCapture collects PermAuditEvents for lifecycle assertions.
type auditCapture struct {
	mu     sync.Mutex
	events []PermAuditEvent
}

func (c *auditCapture) record(ctx context.Context, e PermAuditEvent) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.events = append(c.events, e)
}

func (c *auditCapture) actions() []PermAuditAction {
	c.mu.Lock()
	defer c.mu.Unlock()
	actions := make([]PermAuditAction, len(c.events))
	for i, e := range c.events {
		actions[i] = e.Action
	}
	return actions
}

// startTeamRequest launches bridge.Request on a team actor and returns the
// decision channel. The display timer must be long (or the ctx cancelled) to
// end it.
func startTeamRequest(bridge *PermissionBridge, ctx context.Context, reqID string) <-chan bool {
	ac := actor.ActorContext{
		SessionID: "s-lc", TeamID: "team-lc", MemberID: "m-lc", TaskID: "task-lc", RunID: "run-lc",
		MemberName: "m", MemberRole: "programmer",
	}
	res := make(chan bool, 1)
	go func() {
		allowed, _ := bridge.Request(ac.WithContext(ctx), permission.CreatePermissionRequest{
			SessionID: "s-lc", ToolCallID: reqID, ToolName: "write",
			Action: "write", Description: "test", Path: "/tmp/lc",
		})
		res <- allowed
	}()
	return res
}

// waitRegistered blocks until the request's entry is registered.
func waitRegistered(t *testing.T, bridge *PermissionBridge, reqID string) {
	t.Helper()
	require.Eventually(t, func() bool {
		_, ok := bridge.TeamContextFor(reqID)
		return ok
	}, 2*time.Second, 10*time.Millisecond, "entry must register")
}

func newLifecycleBridge(t *testing.T) (*PermissionBridge, *auditCapture) {
	t.Helper()
	inner := permission.NewPermissionService(t.TempDir(), false, nil)
	bridge := NewPermissionBridge("default", inner)
	bridge.SetRequestTimeout(5 * time.Second)
	capt := &auditCapture{}
	bridge.SetAuditFunc(capt.record)
	return bridge, capt
}

func grantCount(bridge *PermissionBridge) int {
	bridge.grantStore.mu.RLock()
	defer bridge.grantStore.mu.RUnlock()
	return len(bridge.grantStore.grants)
}

func TestM5_UserAllows_Call_NoGrant(t *testing.T) {
	bridge, capt := newLifecycleBridge(t)
	res := startTeamRequest(bridge, t.Context(), "lc-allow")
	waitRegistered(t, bridge, "lc-allow")

	require.NoError(t, bridge.ResolveRequest("lc-allow", true, "call"))

	select {
	case allowed := <-res:
		assert.True(t, allowed)
	case <-time.After(2 * time.Second):
		t.Fatal("request did not return")
	}

	got, err := bridge.store.GetRequest(context.Background(), "lc-allow")
	require.NoError(t, err)
	assert.Equal(t, "allowed", got.Status)
	assert.Equal(t, "call", got.DecisionScope)
	assert.Equal(t, "user", got.DecidedBy)
	assert.Equal(t, 0, grantCount(bridge), "allow-once must not create a grant")
	assert.Contains(t, capt.actions(), PermAuditPermissionAllowed)
}

func TestM5_UserDenies_PersistsDenied(t *testing.T) {
	bridge, capt := newLifecycleBridge(t)
	res := startTeamRequest(bridge, t.Context(), "lc-deny")
	waitRegistered(t, bridge, "lc-deny")

	require.NoError(t, bridge.ResolveRequest("lc-deny", false, "call"))

	select {
	case allowed := <-res:
		assert.False(t, allowed)
	case <-time.After(2 * time.Second):
		t.Fatal("request did not return")
	}

	got, _ := bridge.store.GetRequest(context.Background(), "lc-deny")
	assert.Equal(t, "denied", got.Status)
	assert.Contains(t, capt.actions(), PermAuditPermissionDenied)
}

func TestM5_AllowTaskScope_CreatesGrant(t *testing.T) {
	bridge, _ := newLifecycleBridge(t)
	res := startTeamRequest(bridge, t.Context(), "lc-task")
	waitRegistered(t, bridge, "lc-task")

	require.NoError(t, bridge.ResolveRequest("lc-task", true, "task"))
	<-res

	bridge.grantStore.mu.RLock()
	var taskGrant *Grant
	for _, g := range bridge.grantStore.grants {
		if g.SourceRequestID == "lc-task" {
			taskGrant = g
		}
	}
	bridge.grantStore.mu.RUnlock()
	require.NotNil(t, taskGrant, "task-scope allow must create a grant")
	assert.Equal(t, "task", taskGrant.Scope)
	assert.Equal(t, "task-lc", taskGrant.TaskID)
}

func TestM5_TimeoutExpires(t *testing.T) {
	bridge, capt := newLifecycleBridge(t)
	bridge.SetRequestTimeout(50 * time.Millisecond)
	res := startTeamRequest(bridge, t.Context(), "lc-exp")
	waitRegistered(t, bridge, "lc-exp")

	select {
	case allowed := <-res:
		assert.False(t, allowed)
	case <-time.After(2 * time.Second):
		t.Fatal("request did not time out")
	}

	got, err := bridge.store.GetRequest(context.Background(), "lc-exp")
	require.NoError(t, err)
	assert.Equal(t, "expired", got.Status)
	assert.Contains(t, capt.actions(), PermAuditPermissionExpired)
}

func TestM5_CtxCancelMarksCanceled(t *testing.T) {
	bridge, capt := newLifecycleBridge(t)
	ctx, cancel := context.WithCancel(t.Context())
	res := startTeamRequest(bridge, ctx, "lc-cancel")
	waitRegistered(t, bridge, "lc-cancel")

	cancel()

	select {
	case allowed := <-res:
		assert.False(t, allowed)
	case <-time.After(2 * time.Second):
		t.Fatal("request did not return after cancel")
	}

	got, err := bridge.store.GetRequest(context.Background(), "lc-cancel")
	require.NoError(t, err)
	assert.Equal(t, "canceled", got.Status)
	assert.Contains(t, capt.actions(), PermAuditPermissionCanceled)
}

func TestM5_LateAllowAfterTTLExpiry(t *testing.T) {
	bridge, capt := newLifecycleBridge(t)
	bridge.GetQueue().WithLimits(3, 80*time.Millisecond) // FSM TTL well under the 5s display timer
	res := startTeamRequest(bridge, t.Context(), "lc-late")
	waitRegistered(t, bridge, "lc-late")

	// TTL fires first: the store row leaves pending while the entry survives.
	require.Eventually(t, func() bool {
		got, err := bridge.store.GetRequest(context.Background(), "lc-late")
		return err == nil && got.Status == "expired"
	}, 2*time.Second, 10*time.Millisecond, "TTL must expire the store row")

	require.NoError(t, bridge.ResolveRequest("lc-late", true, "call"))

	select {
	case allowed := <-res:
		assert.False(t, allowed, "late decision must deny")
	case <-time.After(2 * time.Second):
		t.Fatal("request did not return")
	}
	assert.Contains(t, capt.actions(), PermAuditLateResponse)
	assert.Equal(t, 0, grantCount(bridge), "late decision must not create a grant")
}
```

- [ ] **Step 2: 跑测试确认失败**

Run: `GOPROXY=https://goproxy.cn,direct go test ./internal/team/ -run 'TestM5_' -v`
Expected: FAIL——`TestM5_UserAllows_Call_NoGrant`（store 报 not found：Task 2 已落库但 Resolve 未写状态）、`TestM5_TimeoutExpires`（status 仍 pending）、`TestM5_CtxCancelMarksCanceled`（仍 pending）、`TestM5_LateAllowAfterTTLExpiry`（member 收 true 而非 false）；`TestM5_UserDenies_PersistsDenied`、`TestM5_AllowTaskScope_CreatesGrant`（状态/ grant 未写）。

- [ ] **Step 3: 实现接线**

`permission_bridge.go`：

`ResolveRequest`——auditFn 快照上移到函数开头（`!ok` 分支与迟到分支共用），尾部（现 511-518 行）替换为：

```go
	b.queue.Dequeue(reqID)

	slog.Debug("perm_bridge: ResolveRequest",
		"tool_call_id", reqID, "allowed", allowed, "scope", scope, "was_displayed", wasDisplayed,
	)

	decision := "denied"
	if allowed {
		decision = "allowed"
	}
	if err := b.fsm.Resolve(context.Background(), ResolveRequest{
		RequestID: reqID, Decision: decision, Scope: scope, DecidedBy: "user",
	}); err != nil {
		// The store row already left pending (TTL expiry or cancel raced us):
		// a late decision — audit it and deny the member. No grant is created.
		slog.Debug("perm_bridge: resolve raced by terminal state, denying", "tool_call_id", reqID, "error", err)
		auditFn(context.Background(), PermAuditEvent{
			WorkspaceID: b.workspaceID, SessionID: entry.opts.SessionID, ToolCallID: reqID,
			Action: PermAuditLateResponse, TeamID: entry.ac.TeamID, MemberID: entry.ac.MemberID,
			TaskID: entry.ac.TaskID, RunID: entry.ac.RunID, ToolName: entry.opts.ToolName,
			Decision: decision, Timestamp: time.Now(),
		})
		entry.ch <- false
		return nil
	}

	entry.ch <- allowed // buffered, size 1 — never blocks
	return nil
```

快照上移后的函数开头：

```go
	b.queueMu.Lock()
	entry, ok := b.entries[reqID]
	auditFn := b.auditFn
	if !ok {
		// …existing late/unknown branch unchanged, using auditFn…
```

`handleTimeout` 尾部（`b.queue.Dequeue(reqID)` 与 `close(entry.timeoutCh)` 之间）插入：

```go
	if err := b.fsm.Expire(context.Background(), reqID); err != nil {
		slog.Debug("perm_bridge: expire failed", "tool_call_id", reqID, "error", err)
	}
```

`terminateEntry` 尾部（`b.queue.Dequeue(reqID)` 之后）追加：

```go
	if err := b.fsm.CancelRequest(context.Background(), reqID); err != nil {
		slog.Debug("perm_bridge: cancel failed", "tool_call_id", reqID, "error", err)
	}
```

注意：`requestWithUI` 的三个 select 分支注释（"already dequeued from the FSM queue"）保持正确——Dequeue 依旧先于 FSM 调用。

- [ ] **Step 4: 跑测试确认通过（含 -race）**

Run: `GOPROXY=https://goproxy.cn,direct go test -race ./internal/team/ -v -run 'TestM5_|TestPermissionBridge|TestFSM_'`
Expected: 全 PASS。
Run: `GOPROXY=https://goproxy.cn,direct go test -race ./internal/team/`
Expected: PASS（全包绿）。

- [ ] **Step 5: 格式化并提交**

```bash
gofmt -w internal/team/permission_bridge.go internal/team/permission_lifecycle_test.go
git add internal/team/permission_bridge.go internal/team/permission_lifecycle_test.go
git commit -m "feat(team): wire permission lifecycle to FSM resolve/expire/cancel"
```

---

### Task 4: Grant 按 task scope 匹配（C1）+ grant_auto 字段补全

**Files:**
- Modify: `internal/team/permission_bridge.go`（FindActiveGrant、Request 的 grant 检查与 grant_auto audit）
- Modify: `internal/team/permission_bridge_test.go`（3 个 FindActiveGrant 测试更新签名+夹具）
- Modify: `internal/team/permission_fsm_test.go`、`internal/team/permission_e2e_test.go`（剩余 FindActiveGrant 调用点适配）
- Test: `internal/team/permission_bridge_test.go`（新增 2 个 scope 匹配测试）

**Interfaces:**
- Consumes: Task 3 的真实 Resolve 流（task grant 现在真实存在）。
- Produces: `(*GrantStore).FindActiveGrant(ctx, sessionID, taskID, toolName, action string) (*Grant, bool)`；grant_auto audit 含 TaskID/RunID。

- [ ] **Step 1: 写失败测试**

`permission_bridge_test.go` 追加：

```go
func TestGrantStore_FindActiveGrant_TaskScope(t *testing.T) {
	gs := NewGrantStore()
	ctx := context.Background()
	now := time.Now()
	require.NoError(t, gs.CreateGrant(ctx, &Grant{
		ID: "g-task", SessionID: "s1", TaskID: "task-1", ToolName: "bash", Action: "execute",
		Scope: "task", ExpiresAt: now.Add(time.Hour), CreatedAt: now,
	}))

	_, ok := gs.FindActiveGrant(ctx, "s1", "task-1", "bash", "execute")
	assert.True(t, ok, "task grant must match its own task")

	_, ok = gs.FindActiveGrant(ctx, "s1", "task-2", "bash", "execute")
	assert.False(t, ok, "task grant must not match another task")
}

func TestGrantStore_FindActiveGrant_SessionScopeCrossTask(t *testing.T) {
	gs := NewGrantStore()
	ctx := context.Background()
	now := time.Now()
	require.NoError(t, gs.CreateGrant(ctx, &Grant{
		ID: "g-sess", SessionID: "s1", TaskID: "task-1", ToolName: "bash", Action: "execute",
		Scope: "session", ExpiresAt: now.Add(time.Hour), CreatedAt: now,
	}))

	_, ok := gs.FindActiveGrant(ctx, "s1", "task-2", "bash", "execute")
	assert.True(t, ok, "session grant must match any task in the session")
}
```

- [ ] **Step 2: 跑测试确认失败**

Run: `GOPROXY=https://goproxy.cn,direct go test ./internal/team/ -run 'TestGrantStore_FindActiveGrant' -v`
Expected: FAIL——编译错误（参数个数不匹配）。

- [ ] **Step 3: 实现**

`FindActiveGrant` 整体替换：

```go
// FindActiveGrant returns an active (non-expired) grant matching the session,
// tool name, action, and task scope: a task-scoped grant only matches the
// task it was created for; a session-scoped grant matches any task in the
// session.
func (g *GrantStore) FindActiveGrant(ctx context.Context, sessionID string, taskID string, toolName string, action string) (*Grant, bool) {
	g.mu.RLock()
	defer g.mu.RUnlock()
	for _, grant := range g.grants {
		if grant.SessionID != sessionID || grant.ToolName != toolName || grant.Action != action {
			continue
		}
		if !time.Now().Before(grant.ExpiresAt) {
			continue
		}
		if grant.Scope == "session" {
			return grant, true
		}
		if grant.Scope == "task" && grant.TaskID == taskID {
			return grant, true
		}
	}
	return nil, false
}
```

`Request` 的调用点（现 350 行）改为传 task 并补 audit 字段：

```go
	if grant, ok := b.grantStore.FindActiveGrant(ctx, opts.SessionID, ac.TaskID, opts.ToolName, opts.Action); ok {
		slog.Debug("perm_bridge: active grant found (auto-allow)", "tool_call_id", opts.ToolCallID, "scope", grant.Scope)
		auditFn(ctx, PermAuditEvent{
			WorkspaceID: b.workspaceID, SessionID: opts.SessionID, ToolCallID: opts.ToolCallID,
			Action: PermAuditGrantAuto, TeamID: ac.TeamID, MemberID: ac.MemberID,
			TaskID: ac.TaskID, RunID: ac.RunID, ToolName: opts.ToolName,
			Decision: "allowed", Scope: grant.Scope, Timestamp: time.Now(),
		})
		return true, nil
	}
```

适配既有调用点（grep `FindActiveGrant` 逐个更新签名）：`permission_bridge_test.go:48,62,75`（夹具补 `Scope`/`TaskID`，断言保持原意图）、`permission_fsm_test.go`、`permission_e2e_test.go` 剩余处（task-scope 的 `:157` 夹具需带 TaskID 且查询传相同值）。

- [ ] **Step 4: 跑测试确认通过（含 -race + grant_auto 集成）**

Run: `GOPROXY=https://goproxy.cn,direct go test -race ./internal/team/ -run 'TestGrantStore|TestM5_|TestPermissionBridge' -v`
Expected: 全 PASS。
Run: `GOPROXY=https://goproxy.cn,direct go test -race ./internal/team/`
Expected: PASS。

- [ ] **Step 5: 格式化并提交**

```bash
gofmt -w internal/team/permission_bridge.go internal/team/permission_bridge_test.go internal/team/permission_fsm_test.go internal/team/permission_e2e_test.go
git add internal/team/
git commit -m "fix(team): match grants by task scope and enrich grant_auto audit"
```

---

### Task 5: 全量验证 + 文档收尾

**Files:**
- Modify: `docs/superpowers/plans/2026-08-14-code-scan-backlog.md`
- Modify: `docs/superpowers/specs/2026-08-15-b1-b2-lifecycle-wiring-design.md`

**Interfaces:**
- Consumes: Task 1-4 全部产出。
- Produces: backlog B1/B2/B3/B4/C1 勾选与完成记录；spec 验收条件勾选。

- [ ] **Step 1: 全量验证**

```bash
GOPROXY=https://goproxy.cn,direct go build ./...
GOPROXY=https://goproxy.cn,direct go test ./internal/team/
GOPROXY=https://goproxy.cn,direct go test -race ./internal/team/
```

Expected: 三条全过。

- [ ] **Step 2: 文档更新**

backlog：`B1`、`B2`、`B3`、`B4`、`C1` 五个标题 `[ ]`→`[x]`；完成记录表追加五行（日期 2026-08-15，Commit 填实际 SHA）；`C1` 体中"关联"行追加"已修复：FindActiveGrant 按 taskID 匹配"。spec：验收条件 9 项全部 `[x]`。

- [ ] **Step 3: 提交**

```bash
git add docs/superpowers/plans/2026-08-14-code-scan-backlog.md docs/superpowers/specs/2026-08-15-b1-b2-lifecycle-wiring-design.md
git commit -m "docs(plan): mark code-scan backlog B1-B4 and C1 complete"
```
