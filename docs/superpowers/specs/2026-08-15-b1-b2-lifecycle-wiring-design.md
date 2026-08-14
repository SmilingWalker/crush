# B1+B2 生命周期接线：FSM/store 接入生产权限流

> 日期：2026-08-15
> 来源：docs/superpowers/plans/2026-08-14-code-scan-backlog.md 的 B1/B2/B3/B4/C1
> 里程碑：M5（权限审批）
> 前置：A1-A3 已完成（store 原子 Update + 副本读、bridge 字段同步、-race 绿基线）

## 背景

代码扫描确认：权限栈存在两套平行实现——display 队列（bridge 内的
FIFO/displayed/entries，UI 只接了这套）和 FSM/store 层
（`PermissionFSM` + `PermissionStore`，生产从不调用）。后果：

1. **B1**：`ResolveRequest` 丢弃 `scope` 参数，从不调 `fsm.Resolve`——
   "Allow for Task" 实际等同 "Allow Once"，grant 从不创建。
2. **B2**：`requestWithUI` 从不调 `store.CreateRequest`——FSM/store 层
   操作空库，`ListByRun`/`ListPendingByMember` 恒空，恢复逻辑恒 no-op。
3. **B3**：`permission_expired` audit 生产不可达（所有退出路径先 Dequeue
   停掉唯一会调 `fsm.Expire` 的 TTL 定时器）。
4. **B4**：`SetAuditFunc` 不传播给 FSM（构造期快照 no-op 副本）——FSM 的
   5 种 audit 事件即使接线也落入 void。
5. **C1**：`FindActiveGrant` 忽略 TaskID 和 Scope——接线后 grant 变成
   真实放行依据，scope 匹配缺陷立即成为安全问题。

UI 侧已就绪：`PermBridgeResolve(reqID, allowed, scope)` 已传 scope。

## 用户裁决（2026-08-15）

1. **Allow Once（scope=call）不建 grant**——M5 计划约束 #6 为准
   （"allow once 只 resolve 当前 tool call"），废弃 FSM 现有的 30 分钟
   call grant 行为。
2. **60s 显示超时记 expired**——store 状态 expired +
   `permission_expired` audit（M5-04 状态机：pending→expired 超时）。

## 目标

1. 请求生命周期在生产路径上落库并闭环：requested →
   allowed/denied/expired/canceled，每种迁移都有 audit。
2. "Allow for Task" 真正生效：task-scoped grant 创建，且只对同 task 的
   后续同类请求自动放行。
3. FSM audit 落盘（修复 B4）。
4. 行为变化仅限本 spec 声明的语义；非 team 路径不变。

## 非目标

- M5-09（hook_allow/hook_deny 接线 hooked_tool.go）——backlog D1。
- PermissionStore/GrantStore 淘汰（E4）、queue 回调硬化（A7）。
- grant 持久化到 DB（team_permission_grants 表）——目前仍内存态，
  DB 化随 M5 后续任务。
- FSM 队列 5min TTL 与 60s display 计时器的合并（方案 3 已否决）。
- "Allow for Session" UI 按钮（FSM 保留 session scope 支持，UI 未提供，
  ResolveRequest 签名不变）。

## 设计决策

| 决策 | 选择 | 理由 |
|------|------|------|
| 接线位置 | bridge 直接持有 fsm，四个生命周期钩子各调一次 FSM | 决策流集中、queue 保持限流职责（方案 1） |
| exactly-once | 依赖 A1 的 store.Update pending 检查 | 已有并验证的机制，不新增锁 |
| store 非 pending 时的 UI 决策 | late_response audit + member 收 deny | M5-07 语义：迟到决策不产生 grant |
| call scope | 不建 grant | 用户裁决 #1 |
| display 超时 | fsm.Expire（expired + audit） | 用户裁决 #2 |
| ctx 取消 | 新增 fsm.CancelRequest(id)，镜像 Expire | Cancel(runID) 波及同 run 其他请求，不精确 |
| FSM audit 传播 | FSM 加 SetAuditFunc（内部小锁），bridge.SetAuditFunc 同步两者 | 与 A2 的 setter 同步纪律一致 |
| FindActiveGrant | 加 taskID 参数：task grant 要求 TaskID 相等；session grant 匹配 session 内任意 task | C1 修复 |
| FSM 调用失败处理 | 只打日志，不阻塞 member 唤醒 | 决策流不能卡死；audit/状态是尽力而为 |
| terminateEntry 的 ctx | context.Background()（caller ctx 已取消） | 沿用 M5-08b late_response 先例 |

## 改动点

### 1. `internal/team/permission_bridge.go` — PermissionBridge

- struct 加 `fsm *PermissionFSM` 字段；`NewPermissionBridge` 保留已构造
  的 fsm 指针（现在只给了 queue）。
- `SetAuditFunc`：更新 bridge 字段后调用 `b.fsm.SetAuditFunc(fn)`
  （两者都在 queueMu 之外各自加锁——FSM 用自己的锁）。

### 2. `requestWithUI` — 落库 + 补字段（B2）

`teamReq` 补齐：`WorkspaceID: b.workspaceID`、`TeamID: ac.TeamID`、
`MemberID: ac.MemberID`、`TaskID: ac.TaskID`、`RunID: ac.RunID`、
`ResourceRef: opts.Path`（grant 匹配与 audit 字段来源）。在入 display
队列后调用 `store.CreateRequest`（失败仅日志——内存 map 实际不失败）。

### 3. `ResolveRequest` — 决策接线（B1）

现状 `entry.ch <- allowed` 改为：

```go
err := b.fsm.Resolve(context.Background(), ResolveRequest{
    RequestID: reqID,
    Decision:  decision, // "allowed" | "denied" 由 allowed 映射
    Scope:     scope,
    DecidedBy: "user",
})
if err != nil {
    // store 已非 pending（TTL 先到 / 已取消）：迟到决策
    auditFn(PermAuditLateResponse ...) // ToolCallID=reqID
    entry.ch <- false
    return nil // bridge 层 entry 已清理，UI 无感
}
entry.ch <- allowed
```

（auditFn 取自现有快照；late_response 补 TeamID/MemberID 等字段——
entry.ac 可用，修复 D2 的一半。）

### 4. `handleTimeout` — 超时接线（B3）

在清理 entry 后、`close(entry.timeoutCh)` 前调用
`b.fsm.Expire(context.Background(), reqID)`（失败仅日志）。

### 5. `terminateEntry` — 取消接线

在 `b.queue.Dequeue(reqID)` 后调用
`b.fsm.CancelRequest(context.Background(), reqID)`（失败仅日志）。

### 6. `internal/team/permission_fsm.go` — FSM

- `Resolve` 的 `scope == "call"` 分支：不建 grant（仍写状态 + audit）。
- 新增 `CancelRequest(ctx, requestID) error`：Update 内 pending 检查 +
  置 canceled + `permission_canceled` audit，`errNotPending` 幂等返回
  nil——逐行镜像 `Expire`。
- 新增 `SetAuditFunc(fn)`：`mu sync.Mutex` 保护 `auditFn` 字段；所有
  `fsm.auditFn(...)` 调用点改经快照。

### 7. `internal/team/permission_bridge.go` — GrantStore（C1）

`FindActiveGrant(ctx, sessionID, toolName, action)` 签名加 `taskID string`：

- `grant.Scope == "task"`：要求 `grant.TaskID == taskID`
- `grant.Scope == "session"`：session 内任意 task 放行
- （call grant 不再存在；老数据如遇到按不匹配处理）

`Request` 的调用点传 `ac.TaskID`。

## 时序矩阵（两时钟交互）

| 场景 | store 结果 | member 收到 | audit |
|------|-----------|------------|-------|
| UI allow（store pending） | allowed (+task grant) | true | permission_allowed |
| UI deny（store pending） | denied | false | permission_denied |
| 显示 60s 到点 | expired | false | permission_expired |
| FIFO 等待 >5min TTL 先到，UI 后点 allow | expired（保持） | false | late_response |
| member ctx 取消 | canceled | ctx.Err() | permission_canceled |
| entries 查无（已终止/不存在） | 不变 | — | late_response（现状保留） |

## 测试策略

真实路径测试（替换只测 FSM 直调的旧 e2e 场景，F1 一并推进）：

1. `TestM5_MemberRequestPermission_UserAllows`：Request 阻塞 →
   ResolveRequest(true,"call") → member 收 true，store=allowed，**无 grant**
2. `TestM5_AllowTaskScope`：allow "task" → 同 task 再请求自动放行
   （grant_auto）→ 不同 task（同 session）再弹窗
3. `TestM5_TimeoutExpires`：SetRequestTimeout(50ms) → 过期 → store=expired
   + permission_expired audit + member 收 false
4. `TestM5_LateAllowAfterTTLExpiry`：FIFO 压后（先占 displayed）+
   WithLimits 调短 TTL → TTL 先 Expire → UI allow → member 收 false +
   late_response audit
5. `TestM5_CtxCancelMarksCanceled`：ctx 取消 → store=canceled + audit
6. `TestM5_PermissionDenied`：deny → store=denied，member 收 false
7. `TestFSM_SetAuditFunc_Propagates`：bridge.SetAuditFunc 后 FSM 事件
   进回调（B4）

既有 `FindActiveGrant` 调用方测试随签名更新。

## 验收条件

- [x] `requestWithUI` 落库，teamReq 字段完整（WorkspaceID/Team/Member/Task/Run/ResourceRef）
- [x] `ResolveRequest` 调 fsm.Resolve；scope=call 不建 grant；scope=task 建 task-scoped grant
- [x] store 非 pending 的 UI 决策 → late_response audit + member 收 deny
- [x] 60s 超时 → expired + permission_expired audit（生产可达）
- [x] ctx 取消 → canceled + permission_canceled audit
- [x] FSM audit 经 bridge.SetAuditFunc 传播落盘
- [x] FindActiveGrant 匹配 taskID；task grant 不跨 task 放行
- [x] 非 team 路径行为不变（既有测试守护）
- [x] `go build ./...`、`go test ./internal/team/`、`go test -race ./internal/team/` 全绿
