# 权限栈并发修复（A1+A2+A3）Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 消灭 `internal/team` 权限栈的三类数据竞态（store 共享指针、bridge setter 字段、测试 fake），建立 `go test -race ./internal/team/` 绿色基线。

**Architecture:** `PermissionStore` 增加原子 `Update(ctx, id, fn)`（fn 在写锁内对副本执行，成功才写回），读方法全部返回副本；`PermissionFSM` 四个状态迁移改走 `Update`（pending 前置检查放进 fn 消灭 TOCTOU），删除 `UpdateRequest`；`PermissionBridge` 三个 setter 字段挂 `queueMu`，读侧先快照；两个测试 fake 加 mutex + 快照访问器。行为零变化。

**Tech Stack:** Go 1.26（repo 标准）、testify（assert/require）、`-race` 检测器。

**Spec:** `docs/superpowers/specs/2026-08-14-perm-race-fixes-design.md`

## Global Constraints

- 所有 go 命令必须带 `GOPROXY=https://goproxy.cn,direct` 前缀（默认代理在本机不可达）。
- 行为零变化：权限语义、audit 事件内容、错误信息文案（含 `"not pending"`、`"not found"` 子串）不得改变。
- 提交前对改动文件跑 `gofumpt -w`（不可用则 `goimports`/`gofmt`）。
- 注释：整行注释大写开头、句号结尾；日志消息大写开头。
- 语义化单行 commit（`fix:`/`test:`/`refactor:`）。
- 不改本计划列出的文件之外的任何文件。

---

### Task 1: PermissionStore 原子 Update + 读方法返回副本

**Files:**
- Modify: `internal/team/permission_bridge.go`（PermissionStore 区段，约 152-214 行）
- Test: `internal/team/permission_bridge_test.go`

**Interfaces:**
- Consumes: 无新依赖。
- Produces: `func (s *PermissionStore) Update(ctx context.Context, id string, fn func(*PermissionRequest) error) (*PermissionRequest, error)`；`GetRequest`/`ListByRun`/`ListPendingByMember` 返回副本（Task 2 依赖）。本任务**保留** `UpdateRequest`（FSM 仍在用，Task 2 删）。

- [ ] **Step 1: 写失败测试**

在 `permission_bridge_test.go` 末尾追加（若 import 缺 `errors`、`fmt`、`sync`、`time` 则补上）：

```go
func TestPermissionStore_Update_AppliesMutation(t *testing.T) {
	ps := NewPermissionStore()
	ctx := context.Background()
	require.NoError(t, ps.CreateRequest(ctx, &PermissionRequest{ID: "u1", Status: "pending"}))

	got, err := ps.Update(ctx, "u1", func(r *PermissionRequest) error {
		if r.Status != "pending" {
			return fmt.Errorf("not pending: %s", r.Status)
		}
		r.Status = "allowed"
		r.Decision = "allowed"
		return nil
	})
	require.NoError(t, err)
	assert.Equal(t, "allowed", got.Status)

	stored, _ := ps.GetRequest(ctx, "u1")
	assert.Equal(t, "allowed", stored.Status)
}

func TestPermissionStore_Update_FnErrorAborts(t *testing.T) {
	ps := NewPermissionStore()
	ctx := context.Background()
	require.NoError(t, ps.CreateRequest(ctx, &PermissionRequest{ID: "u2", Status: "pending"}))

	_, err := ps.Update(ctx, "u2", func(r *PermissionRequest) error {
		r.Status = "allowed" // partial mutation before the error.
		return errors.New("boom")
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "boom")

	stored, _ := ps.GetRequest(ctx, "u2")
	assert.Equal(t, "pending", stored.Status, "aborted update must not persist partial mutation")
}

func TestPermissionStore_Update_NotFound(t *testing.T) {
	ps := NewPermissionStore()
	_, err := ps.Update(context.Background(), "nope", func(r *PermissionRequest) error { return nil })
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not found")
}

func TestPermissionStore_ReadsReturnCopies(t *testing.T) {
	ps := NewPermissionStore()
	ctx := context.Background()
	require.NoError(t, ps.CreateRequest(ctx, &PermissionRequest{
		ID: "c1", Status: "pending", RunID: "run-c", MemberID: "m-c",
	}))

	got, _ := ps.GetRequest(ctx, "c1")
	got.Status = "allowed"
	stored, _ := ps.GetRequest(ctx, "c1")
	assert.Equal(t, "pending", stored.Status, "mutating a returned copy must not touch the store")

	byRun := ps.ListByRun(ctx, "run-c")
	require.Len(t, byRun, 1)
	byRun[0].Status = "denied"
	stored, _ = ps.GetRequest(ctx, "c1")
	assert.Equal(t, "pending", stored.Status)

	byMember := ps.ListPendingByMember(ctx, "m-c")
	require.Len(t, byMember, 1)
	byMember[0].Status = "orphaned"
	stored, _ = ps.GetRequest(ctx, "c1")
	assert.Equal(t, "pending", stored.Status)
}

func TestPermissionStore_Update_Concurrent(t *testing.T) {
	ps := NewPermissionStore()
	ctx := context.Background()
	require.NoError(t, ps.CreateRequest(ctx, &PermissionRequest{ID: "cc1", Status: "pending"}))

	const n = 16
	var wg sync.WaitGroup
	start := make(chan struct{})
	successes := make(chan int, n)
	for range n {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			_, err := ps.Update(ctx, "cc1", func(r *PermissionRequest) error {
				if r.Status != "pending" {
					return errors.New("not pending")
				}
				r.Status = "allowed"
				return nil
			})
			if err == nil {
				successes <- 1
			}
		}()
	}
	close(start)
	wg.Wait()
	close(successes)
	count := 0
	for range successes {
		count++
	}
	assert.Equal(t, 1, count, "exactly one concurrent transition must win")
}
```

- [ ] **Step 2: 跑测试确认失败**

Run: `GOPROXY=https://goproxy.cn,direct go test ./internal/team/ -run 'TestPermissionStore_' -v`
Expected: 编译失败，`ps.Update undefined (type *team.PermissionStore has no field or method Update)`。

- [ ] **Step 3: 实现 Update 与副本读**

在 `permission_bridge.go` 的 `GetRequest` 之前加：

```go
// Update applies fn to a copy of the stored request under the write lock and
// writes it back only if fn succeeds. The status precondition check belongs
// inside fn so the check and the write are atomic. Returns a copy of the
// post-update state.
func (s *PermissionStore) Update(
	ctx context.Context, id string, fn func(*PermissionRequest) error,
) (*PermissionRequest, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	req, ok := s.requests[id]
	if !ok {
		return nil, fmt.Errorf("permission request not found: %s", id)
	}
	candidate := *req
	if err := fn(&candidate); err != nil {
		return nil, err
	}
	*req = candidate
	out := *req
	return &out, nil
}
```

`GetRequest` 的 `return req, nil` 改为：

```go
	out := *req
	return &out, nil
```

`ListByRun` 与 `ListPendingByMember` 的 `result = append(result, req)` 均改为：

```go
		c := *req
		result = append(result, &c)
```

（`UpdateRequest` 本任务不动。）

- [ ] **Step 4: 跑测试确认通过**

Run: `GOPROXY=https://goproxy.cn,direct go test ./internal/team/ -run 'TestPermissionStore_' -v`
Expected: 全部 PASS。
Run: `GOPROXY=https://goproxy.cn,direct go test ./internal/team/`
Expected: PASS（FSM 走副本读改写回，功能不变）。

- [ ] **Step 5: 格式化并提交**

```bash
gofumpt -w internal/team/permission_bridge.go internal/team/permission_bridge_test.go
git add internal/team/permission_bridge.go internal/team/permission_bridge_test.go
git commit -m "fix(team): make PermissionStore reads return copies and add atomic Update"
```

---

### Task 2: FSM 迁移到 Update，删除 UpdateRequest

**Files:**
- Modify: `internal/team/permission_fsm.go`（全部四个迁移方法）
- Modify: `internal/team/permission_bridge.go`（删除 `UpdateRequest`，约 182-188 行）
- Modify: `internal/team/permission_bridge_test.go`（删除 `TestPermissionStore_UpdateRequest`，约 97-111 行）
- Test: `internal/team/permission_fsm_test.go`

**Interfaces:**
- Consumes: Task 1 的 `(*PermissionStore).Update`。
- Produces: FSM 四个方法签名不变；新增包级哨兵 `errNotPending`；`UpdateRequest` 从此不存在（后续任何任务不得引用）。

- [ ] **Step 1: 写失败测试**

在 `permission_fsm_test.go` 末尾追加（import 缺 `sync` 则补）：

```go
func TestPermissionFSM_Resolve_Concurrent(t *testing.T) {
	ps := NewPermissionStore()
	gs := NewGrantStore()

	var mu sync.Mutex
	var events []PermAuditEvent
	auditFn := func(ctx context.Context, e PermAuditEvent) {
		mu.Lock()
		defer mu.Unlock()
		events = append(events, e)
	}
	fsm := NewPermissionFSM(ps, gs, auditFn)

	ctx := context.Background()
	require.NoError(t, ps.CreateRequest(ctx, &PermissionRequest{
		ID: "cc1", TeamID: "t1", MemberID: "m1", SessionID: "s1", RunID: "run1",
		ToolName: "bash", Action: "execute", Status: "pending", CreatedAt: time.Now(),
	}))

	const n = 16
	var wg sync.WaitGroup
	start := make(chan struct{})
	for i := range n {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			decision := "allowed"
			if i%2 == 1 {
				decision = "denied"
			}
			_ = fsm.Resolve(ctx, ResolveRequest{
				RequestID: "cc1", Decision: decision, Scope: "call", DecidedBy: "user",
			})
		}(i)
	}
	close(start)
	wg.Wait()

	mu.Lock()
	defer mu.Unlock()
	assert.Len(t, events, 1, "exactly one decision must win and be audited")

	got, err := ps.GetRequest(ctx, "cc1")
	require.NoError(t, err)
	assert.Contains(t, []string{"allowed", "denied"}, got.Status)

	gs.mu.RLock()
	grantCount := len(gs.grants)
	gs.mu.RUnlock()
	if got.Status == "allowed" {
		assert.Equal(t, 1, grantCount)
	} else {
		assert.Equal(t, 0, grantCount)
	}
}
```

- [ ] **Step 2: 跑测试确认失败**

Run: `GOPROXY=https://goproxy.cn,direct go test ./internal/team/ -run 'TestPermissionFSM_Resolve_Concurrent' -v`
Expected: FAIL —— `events` 长度 > 1（当前读-改-写回允许多个 goroutine 都看到 pending，各自 audit）。

- [ ] **Step 3: 迁移 FSM**

`permission_fsm.go` 顶部 import 加 `"errors"`，加包级哨兵：

```go
// errNotPending is returned by store Update callbacks when the request has
// already left the pending state; callers treat it as an idempotent no-op.
var errNotPending = errors.New("request is not pending")
```

`Resolve` 整体替换为（grant/audit 逻辑不变，仅状态写入改走 Update）：

```go
// Resolve handles a user decision on a pending permission request.
func (fsm *PermissionFSM) Resolve(ctx context.Context, req ResolveRequest) error {
	switch req.Decision {
	case "allowed", "denied":
	default:
		return fmt.Errorf("resolve: unknown decision %q", req.Decision)
	}

	now := time.Now()
	updated, err := fsm.store.Update(ctx, req.RequestID, func(r *PermissionRequest) error {
		if r.Status != "pending" {
			return fmt.Errorf("request %s is not pending (status=%s)", req.RequestID, r.Status)
		}
		r.Status = req.Decision
		r.Decision = req.Decision
		r.DecidedBy = req.DecidedBy
		r.DecidedAt = &now
		if req.Decision == "allowed" {
			r.DecisionScope = req.Scope
		}
		return nil
	})
	if err != nil {
		return fmt.Errorf("resolve: %w", err)
	}

	if req.Decision == "denied" {
		fsm.auditFn(ctx, PermAuditEvent{
			WorkspaceID: updated.WorkspaceID, SessionID: updated.SessionID, ToolCallID: updated.ToolCallID,
			Action: PermAuditPermissionDenied, TeamID: updated.TeamID, MemberID: updated.MemberID,
			TaskID: updated.TaskID, RunID: updated.RunID, ToolName: updated.ToolName,
			Resource: updated.ResourceRef, Decision: "denied", DecidedBy: req.DecidedBy,
			Timestamp: now,
		})
		return nil
	}

	scope := req.Scope
	if scope == "" {
		scope = "call"
	}
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
	case "call":
		grant.ExpiresAt = now.Add(30 * time.Minute)
	case "task":
		grant.ExpiresAt = now.Add(24 * time.Hour)
	case "session":
		grant.ExpiresAt = now.Add(7 * 24 * time.Hour)
	}

	if err := fsm.grantStore.CreateGrant(ctx, grant); err != nil {
		return fmt.Errorf("resolve: create grant: %w", err)
	}

	fsm.auditFn(ctx, PermAuditEvent{
		WorkspaceID: updated.WorkspaceID, SessionID: updated.SessionID, ToolCallID: updated.ToolCallID,
		Action: PermAuditPermissionAllowed, TeamID: updated.TeamID, MemberID: updated.MemberID,
		TaskID: updated.TaskID, RunID: updated.RunID, ToolName: updated.ToolName,
		Resource: updated.ResourceRef, Decision: "allowed", Scope: scope, DecidedBy: req.DecidedBy,
		Timestamp: now,
	})
	return nil
}
```

`Expire` 整体替换为：

```go
// Expire marks a pending request as expired (called on timeout).
func (fsm *PermissionFSM) Expire(ctx context.Context, requestID string) error {
	updated, err := fsm.store.Update(ctx, requestID, func(r *PermissionRequest) error {
		if r.Status != "pending" {
			return errNotPending
		}
		r.Status = "expired"
		return nil
	})
	if errors.Is(err, errNotPending) {
		return nil // already resolved — idempotent
	}
	if err != nil {
		return fmt.Errorf("expire: %w", err)
	}
	fsm.auditFn(ctx, PermAuditEvent{
		WorkspaceID: updated.WorkspaceID, SessionID: updated.SessionID, ToolCallID: updated.ToolCallID,
		Action: PermAuditPermissionExpired, TeamID: updated.TeamID, MemberID: updated.MemberID,
		TaskID: updated.TaskID, RunID: updated.RunID, ToolName: updated.ToolName,
		Timestamp: time.Now(),
	})
	return nil
}
```

`Cancel` 整体替换为：

```go
// Cancel marks all pending requests for a run as canceled and emits an audit
// event for each. Returns the count of canceled requests.
func (fsm *PermissionFSM) Cancel(ctx context.Context, runID string) (int, error) {
	requests := fsm.store.ListByRun(ctx, runID)
	count := 0
	for _, req := range requests {
		updated, err := fsm.store.Update(ctx, req.ID, func(r *PermissionRequest) error {
			if r.Status != "pending" {
				return errNotPending
			}
			r.Status = "canceled"
			return nil
		})
		if errors.Is(err, errNotPending) {
			continue
		}
		if err != nil {
			return count, fmt.Errorf("cancel: %w", err)
		}
		count++
		fsm.auditFn(ctx, PermAuditEvent{
			WorkspaceID: updated.WorkspaceID, SessionID: updated.SessionID, ToolCallID: updated.ToolCallID,
			Action: PermAuditPermissionCanceled, TeamID: updated.TeamID, MemberID: updated.MemberID,
			RunID: updated.RunID, ToolName: updated.ToolName, Timestamp: time.Now(),
		})
	}
	return count, nil
}
```

`Orphan` 整体替换为：

```go
// Orphan marks all pending requests for a member as orphaned (used during
// startup recovery). Returns the count of orphaned requests.
func (fsm *PermissionFSM) Orphan(ctx context.Context, memberID string) (int, error) {
	requests := fsm.store.ListPendingByMember(ctx, memberID)
	count := 0
	for _, req := range requests {
		updated, err := fsm.store.Update(ctx, req.ID, func(r *PermissionRequest) error {
			if r.Status != "pending" {
				return errNotPending
			}
			r.Status = "orphaned"
			return nil
		})
		if errors.Is(err, errNotPending) {
			continue
		}
		if err != nil {
			return count, fmt.Errorf("orphan: %w", err)
		}
		count++
		fsm.auditFn(ctx, PermAuditEvent{
			WorkspaceID: updated.WorkspaceID, SessionID: updated.SessionID, ToolCallID: updated.ToolCallID,
			Action: PermAuditPermissionOrphaned, TeamID: updated.TeamID, MemberID: updated.MemberID,
			RunID: updated.RunID, ToolName: updated.ToolName, Timestamp: time.Now(),
		})
	}
	return count, nil
}
```

最后：删除 `permission_bridge.go` 中的 `UpdateRequest` 方法（约 182-188 行）与 `permission_bridge_test.go` 中的 `TestPermissionStore_UpdateRequest`（约 97-111 行）。

- [ ] **Step 4: 跑测试确认通过**

Run: `GOPROXY=https://goproxy.cn,direct go test ./internal/team/ -v -run 'TestFSM|TestPermissionFSM|TestPermissionStore'`
Expected: 全部 PASS（`TestFSM_Resolve_AlreadyResolved` 等错误文案断言不受影响）。
Run: `GOPROXY=https://goproxy.cn,direct go test -race ./internal/team/ -run 'TestFSM|TestPermissionFSM|TestPermission'`
Expected: PASS。

- [ ] **Step 5: 格式化并提交**

```bash
gofumpt -w internal/team/permission_fsm.go internal/team/permission_bridge.go internal/team/permission_fsm_test.go internal/team/permission_bridge_test.go
git add -A internal/team/
git commit -m "refactor(team): route FSM state transitions through atomic PermissionStore.Update"
```

---

### Task 3: PermissionBridge setter 字段同步（A2）

**Files:**
- Modify: `internal/team/permission_bridge.go`（setters 262-277 行；`Request` 约 304-336；`requestWithUI` 约 342-389；`ResolveRequest` 约 445-459）
- Test: `internal/team/permission_bridge_test.go`

**Interfaces:**
- Consumes: 无。
- Produces: 签名均不变；新增约定——`auditFn`/`tracker`/`requestTimeout` 的读写必须持 `queueMu`（Task 4/5 不触碰这些字段）。

- [ ] **Step 1: 写失败测试（以 -race 为裁判）**

在 `permission_bridge_test.go` 末尾追加（import 缺 `sync`、`"github.com/google/uuid"` 则补）：

```go
// TestPermissionBridge_ConcurrentSettersAndRequest exercises setter-field
// reads on the live request path. Functionally it only asserts Request
// returns; under -race an unsynchronized read/write pair trips the detector.
func TestPermissionBridge_ConcurrentSettersAndRequest(t *testing.T) {
	inner := permission.NewPermissionService(t.TempDir(), false, nil)
	bridge := NewPermissionBridge("default", inner)

	stop := make(chan struct{})
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; ; i++ {
			select {
			case <-stop:
				return
			default:
			}
			bridge.SetAuditFunc(func(ctx context.Context, e PermAuditEvent) {})
			bridge.SetRequestTimeout(time.Duration(50+i%10) * time.Millisecond)
		}
	}()

	ac := actor.ActorContext{
		SessionID: "s-race", TeamID: "team-r", MemberID: "m-r",
		MemberName: "m", MemberRole: "programmer",
	}
	for range 3 {
		ctx, cancel := context.WithTimeout(ac.WithContext(t.Context()), 150*time.Millisecond)
		_, _ = bridge.Request(ctx, permission.CreatePermissionRequest{
			SessionID: "s-race", ToolCallID: uuid.New().String(), ToolName: "write",
			Action: "write", Description: "test", Path: "/tmp/x",
		})
		cancel()
	}
	close(stop)
	wg.Wait()
}
```

- [ ] **Step 2: 以 -race 跑测试确认失败**

Run: `GOPROXY=https://goproxy.cn,direct go test -race ./internal/team/ -run 'TestPermissionBridge_ConcurrentSettersAndRequest' -v`
Expected: FAIL —— `WARNING: DATA RACE`，`SetRequestTimeout` 写 vs `requestWithUI`（约 :373）或 `pumpDisplay` 读 `b.requestTimeout`；`SetAuditFunc` 写 vs `requestWithUI`（约 :384）读 `b.auditFn`。

- [ ] **Step 3: 实现同步**

三个 setter 改为：

```go
// SetAuditFunc sets the audit callback for permission events.
func (b *PermissionBridge) SetAuditFunc(fn PermAuditFunc) {
	b.queueMu.Lock()
	defer b.queueMu.Unlock()
	b.auditFn = fn
}
```

```go
// SetActiveSessionTracker injects the shared ActiveSessionTracker from app.go.
// Must be called before any team member tool calls. Caller owns the tracker lifecycle.
func (b *PermissionBridge) SetActiveSessionTracker(t *ActiveSessionTracker) {
	b.queueMu.Lock()
	defer b.queueMu.Unlock()
	b.tracker = t
}
```

```go
// SetRequestTimeout overrides how long requestWithUI waits for a UI decision
// before denying. Intended for tests.
func (b *PermissionBridge) SetRequestTimeout(d time.Duration) {
	b.queueMu.Lock()
	defer b.queueMu.Unlock()
	b.requestTimeout = d
}
```

`Request` 开头（`actor.FromContext` 之后）加快照并全文用局部变量：

```go
	b.queueMu.Lock()
	auditFn := b.auditFn
	hasTracker := b.tracker != nil
	b.queueMu.Unlock()
```

debug 日志的 `"tracker_set", b.tracker != nil` 改为 `"tracker_set", hasTracker`；grant_auto 分支的 `b.auditFn(...)` 改为 `auditFn(...)`。

`requestWithUI` 开头（`reqID` 生成之前）加：

```go
	b.queueMu.Lock()
	auditFn := b.auditFn
	requestTimeout := b.requestTimeout
	b.queueMu.Unlock()
```

`teamReq` 的 `ExpiresAt: now.Add(b.requestTimeout)` 改为 `now.Add(requestTimeout)`；M5-08b audit 的 `b.auditFn(...)` 改为 `auditFn(...)`。`pumpDisplay` 内 `time.AfterFunc(b.requestTimeout, ...)`（约 :504）已在 `queueMu` 内，不改。

`ResolveRequest` 的 `!ok` 分支：在 `b.queueMu.Lock()` 之后、`Unlock` 之前快照 `auditFn := b.auditFn`，解锁后的 `b.auditFn(...)` 改用 `auditFn`。

- [ ] **Step 4: 以 -race 跑测试确认通过**

Run: `GOPROXY=https://goproxy.cn,direct go test -race ./internal/team/ -run 'TestPermissionBridge' -v`
Expected: 全部 PASS（含新测试，无 DATA RACE）。

- [ ] **Step 5: 格式化并提交**

```bash
gofumpt -w internal/team/permission_bridge.go internal/team/permission_bridge_test.go
git add internal/team/permission_bridge.go internal/team/permission_bridge_test.go
git commit -m "fix(team): synchronize PermissionBridge setter fields with queueMu"
```

---

### Task 4: 测试 fake 同步（A3，-race 转绿的最后一环）

**Files:**
- Modify: `internal/team/member_runner_test.go`（fake 165-179 行；读点 221、234-236、268、297）
- Modify: `internal/team/e2e_test.go`（fake 60-74 行；读点 179）
- Modify: `internal/team/shutdown_test.go`（读点 191、206）

**Interfaces:**
- Consumes: 无。
- Produces: 两个 fake 的 `RunCallsCount() int` 与 `RunCalls() []agent.TeamAgentCall` 快照访问器；直接读 `.runCalls` 从此禁止。

- [ ] **Step 1: 以 -race 复现失败（测试已存在）**

Run: `GOPROXY=https://goproxy.cn,direct go test -race ./internal/team/ -run 'TestMemberRunner_Start_IdleLoop|TestMemberRunner_handleWake' -v`
Expected: FAIL —— `WARNING: DATA RACE`，`recordingTurnRunner.Run` 写 `runCalls`（:173）vs 测试读（:221/:297）。

- [ ] **Step 2: 修 recordingTurnRunner 并更新读点**

`member_runner_test.go` 的 fake 改为：

```go
// recordingTurnRunner records Run calls for test assertions. runCalls is
// written on the runner goroutine and read on the test goroutine, so all
// access goes through mu. runResult/runErr/busy are only set at construction
// (before Start), which is ordered by goroutine creation.
type recordingTurnRunner struct {
	mu        sync.Mutex
	runCalls  []agent.TeamAgentCall
	runResult agent.TurnRunResult
	runErr    error
	busy      bool
}

func (m *recordingTurnRunner) Run(ctx context.Context, call agent.TeamAgentCall) (agent.TurnRunResult, error) {
	m.mu.Lock()
	m.runCalls = append(m.runCalls, call)
	m.mu.Unlock()
	return m.runResult, m.runErr
}

// RunCallsCount returns the number of recorded Run calls.
func (m *recordingTurnRunner) RunCallsCount() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.runCalls)
}

// RunCalls returns a snapshot of the recorded Run calls.
func (m *recordingTurnRunner) RunCalls() []agent.TeamAgentCall {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]agent.TeamAgentCall, len(m.runCalls))
	copy(out, m.runCalls)
	return out
}
```

（import 缺 `sync` 则补。）读点替换：

- `:221` 与 `:297`：`calls := len(mockRunner.runCalls)` → `calls := mockRunner.RunCallsCount()`
- `:234-236`：

```go
	calls := mockRunner.RunCalls()
	require.Len(t, calls, 1)
	assert.Equal(t, mr.sessionID, calls[0].SessionID)
	assert.NotEmpty(t, calls[0].PromptEnvelope, "prompt should not be empty (stub)")
```

- `:268`：`assert.Equal(t, 0, len(mockRunner.runCalls), "no runs while busy")` → `assert.Equal(t, 0, mockRunner.RunCallsCount(), "no runs while busy")`

- [ ] **Step 3: 修 e2eRecordingRunner 并更新读点**

`e2e_test.go` 的 fake 加同样的 `mu sync.Mutex`、`Run` 内加锁，追加同样的 `RunCallsCount`/`RunCalls` 访问器（方法接收者类型为 `*e2eRecordingRunner`，注释同上）。`:179` 的 `calls := len(mockRunner.runCalls)` → `calls := mockRunner.RunCallsCount()`。

- [ ] **Step 4: 修 shutdown_test 读点**

`:191` `callsBefore := len(mockRunner.runCalls)` → `callsBefore := mockRunner.RunCallsCount()`；
`:206` `callsAfter := len(mockRunner.runCalls)` → `callsAfter := mockRunner.RunCallsCount()`。

- [ ] **Step 5: 以 -race 跑全包确认转绿**

Run: `GOPROXY=https://goproxy.cn,direct go test -race ./internal/team/`
Expected: PASS —— **这是本计划的验收基线**。

- [ ] **Step 6: 格式化并提交**

```bash
gofumpt -w internal/team/member_runner_test.go internal/team/e2e_test.go internal/team/shutdown_test.go
git add internal/team/member_runner_test.go internal/team/e2e_test.go internal/team/shutdown_test.go
git commit -m "test(team): synchronize recording fake runCalls access across goroutines"
```

---

### Task 5: 全量验证 + 文档收尾

**Files:**
- Modify: `docs/superpowers/plans/2026-08-14-code-scan-backlog.md`

**Interfaces:**
- Consumes: Task 1-4 的全部产出。
- Produces: backlog 中 A1/A2/A3 勾选与完成记录。

- [ ] **Step 1: 全量验证**

```bash
GOPROXY=https://goproxy.cn,direct go build ./...
GOPROXY=https://goproxy.cn,direct go test ./internal/team/
GOPROXY=https://goproxy.cn,direct go test -race ./internal/team/
```

Expected: 三条全部通过。

- [ ] **Step 2: 更新 backlog**

`2026-08-14-code-scan-backlog.md`：A1/A2/A3 标题的 `[ ]` 改为 `[x]`；"完成记录"表追加三行（日期 2026-08-14，问题 A1/A2/A3，Commit 填 Task 1-4 的实际 commit hash）。

- [ ] **Step 3: 提交**

```bash
git add docs/superpowers/plans/2026-08-14-code-scan-backlog.md
git commit -m "docs(plan): mark code-scan backlog A1-A3 complete"
```
