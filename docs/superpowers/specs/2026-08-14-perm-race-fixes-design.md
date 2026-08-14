# 权限栈并发修复（A1+A2+A3）：加锁纪律与 -race 绿色基线

> 日期：2026-08-14
> 来源：docs/superpowers/plans/2026-08-14-code-scan-backlog.md 的 A1/A2/A3
> 里程碑：M5（权限审批）质量加固
> 流程：brainstorming（用户已确认范围）→ 本 spec → writing-plans → 实施

## 背景

`go test -race ./internal/team/` 当前失败。代码审查发现权限栈存在三类并发缺陷：

1. **A1**：`PermissionStore` 的读方法（`GetRequest`/`ListByRun`/`ListPendingByMember`）
   返回 map 内的共享指针。`PermissionFSM`（`permission_fsm.go:48-52, 141, 157`）
   在 store 锁外直接改这些指针的字段，与 store 读侧的 RLock 形成数据竞态。
   此外 FSM 的"读-改-写回"模式（GetRequest → 改字段 → UpdateRequest 整体替换）
   存在 TOCTOU：两个并发 Resolve 都能看到 `pending`。
2. **A2**：`PermissionBridge` 的 `auditFn`/`tracker`/`requestTimeout` 三个字段
   由 setter（`permission_bridge.go:263-277`）在构造后写入，被 `Request`/
   `requestWithUI`/`pumpDisplay` 在请求路径上无同步读取。
3. **A3**：测试 fake `recordingTurnRunner`（`member_runner_test.go:165-179`）
   的 `runCalls` 被 runner goroutine 写、测试 goroutine 读，无同步。
   `-race` 实测抓到的两条 DATA RACE（`TestMemberRunner_Start_IdleLoop_Wake_Run_Success`
   和 `TestMemberRunner_handleWake_RunError`）均为这一根因。
   `e2e_test.go:61-74` 的 `e2eRecordingRunner` 是同模式复制品，需一并修复。

注：`sql: database is closed` 日志（`TestMemberRunner_handleWake_RunError`）是
shutdown 顺序问题（backlog A5），不是 race 来源，本 spec 不处理。

## 目标

1. `PermissionStore` 成为请求状态变更的唯一所有者，所有变更在 store 锁内原子完成。
2. `PermissionBridge` 的 setter 可变字段读写全部由 `queueMu` 保护。
3. 两个测试 fake 的跨 goroutine 字段访问同步化。
4. **验收基线：`go test -race ./internal/team/` 转绿**，普通 `go test ./internal/team/`
   行为不变（本修复不改任何对外行为）。

## 非目标

- B1/B2（FSM 接入生产路径）——后续单独 spec。
- A4（`pumpDisplay` 持锁调用 pubsub Publish 的脆弱耦合）——不是实测竞态，重构另行处理。
- A5（shutdown 后写库竞态）、C1（FindActiveGrant 忽略 scope）等 backlog 其余项。
- PermissionQueue 的 `maxPending`/`defaultTTL` 无锁读写（`permission_queue.go:30-33, 82`，
  backlog E 项外围）——实际均为启动期单线程配置，不在本修复内。

## 设计决策

| 决策 | 选择 | 理由 |
|------|------|------|
| store 写入模型 | 原子 `Update(ctx, id, fn)` 回调式，替代读-改-写回 | 一个方法覆盖 Resolve/Expire/Cancel/Orphan 四种迁移；pending 检查放进回调内消灭 TOCTOU |
| 读方法返回值 | 返回副本（新分配），不返回内部指针 | 从类型上杜绝"拿到指针在锁外改"的误用 |
| `UpdateRequest`（整体替换） | 删除 | 只有 FSM 和两个测试在用；替换语义本身是竞态根源 |
| FSM 职责 | 保留生命周期决策（grant 创建、audit），状态写入委托 store | 锁归 store、策略归 FSM，边界清晰 |
| grant 创建与 audit 时机 | 在 store 锁外，用 Update 返回的副本执行 | 避免持 store 锁调用外部回调（auditFn 可能写 DB） |
| bridge 字段保护 | 复用 `queueMu`，读侧先快照再使用 | 不新增锁，与现有 pending 状态机同一把锁序 |
| fake 同步 | fake 内加 mutex + 快照访问器 | 测试读断言仍简单；直接暴露字段必然再犯 |

## 改动点

### 1. `internal/team/permission_bridge.go` — PermissionStore（A1）

```go
// Update 在写锁内对存储的请求执行 fn；fn 返回错误则中止不落盘。
// 返回迁移后的副本。fn 内做 status 前置检查（如 != "pending" 则报错）。
func (s *PermissionStore) Update(
    ctx context.Context, id string, fn func(*PermissionRequest) error,
) (*PermissionRequest, error)
```

- `GetRequest`/`ListByRun`/`ListPendingByMember`：返回副本。
- 删除 `UpdateRequest`。

### 2. `internal/team/permission_fsm.go`（A1）

- `Resolve`：一次 `Update` 内完成 pending 检查 + 状态/决策字段写入；成功后用
  返回副本在锁外建 grant、发 audit。
- `Expire`：`Update` 内 pending 检查 + 置 `expired`；audit 用副本，锁外发。
- `Cancel`/`Orphan`：list 副本后逐个 `Update`（回调内置 pending 检查保证幂等），
  audit 锁外逐个发。

### 3. `internal/team/permission_bridge.go` — PermissionBridge 字段（A2）

- `SetAuditFunc`/`SetActiveSessionTracker`/`SetRequestTimeout`：写侧加 `queueMu`。
- 读侧快照：`Request`（auditFn、tracker）、`requestWithUI`（auditFn、requestTimeout，
  注意 `:373` 在锁外读）、`ResolveRequest`（auditFn，在已持锁段快照、释放后再调用；
  现有代码已是先解锁再调用，保持）。
- `pumpDisplay` 的 `:504` 读取已在 `queueMu` 内，随 setter 加锁自动安全，无需改。

### 4. 测试 fake（A3）

- `recordingTurnRunner`（`member_runner_test.go`）与 `e2eRecordingRunner`
  （`e2e_test.go`）：加 `mu sync.Mutex`；`Run` 内 append 加锁；新增
  `RunCallsCount() int` 与 `RunCalls() []agent.TeamAgentCall`（快照）。
- 更新读点：`member_runner_test.go:221, 234-236, 268, 297`、
  `e2e_test.go:179`、`shutdown_test.go:191, 206`。
- `busy` 字段：均为 Start 前写入（goroutine 创建构成 happens-before），
  不改，仅注释说明。

### 5. 受影响测试迁移

- `permission_bridge_test.go:97-108`（`TestPermissionStore_UpdateRequest`）：
  改为测试 `Update`（含 fn 报错中止、不存在 ID 报错两个新分支）。
- `permission_fsm_test.go`、`permission_e2e_test.go` 中对 `GetRequest` 的断言：
  副本语义下现有断言（读 `got.Status` 等）不变，预期零改动或极小改动。

## 测试策略

- 新增 `TestPermissionStore_Update_Concurrent`：N 个 goroutine 并发对同一
  pending 请求 `Update(allowed/denied)`，断言恰好一个成功——证明 TOCTOU 消失。
- 新增 `TestPermissionFSM_Resolve_Concurrent`：并发 Resolve，断言 audit 恰好一条。
- 既有测试全部保持通过（行为不变约束）。
- 验收命令：`go test -race ./internal/team/`、`go test ./internal/team/`。

## 验收条件

- [ ] `PermissionStore` 读方法返回副本，`UpdateRequest` 已删除
- [ ] FSM 四个迁移方法全部经 `Update` 原子写入，pending 检查在锁内
- [ ] bridge 三个 setter 字段读写均持 `queueMu`
- [ ] 两个 fake 的 `runCalls` 访问同步，直接字段读取全部消除
- [ ] `go build ./...` 通过
- [ ] `go test ./internal/team/` 通过
- [ ] `go test -race ./internal/team/` 通过（绿色基线建立）
