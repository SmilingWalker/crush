# 代码扫描问题清单（2026-08-14）

> 来源：全量 build/vet/test + `-race` 检测 + `internal/team` 深度代码审查
> 背景：分支 `agent-team`，M5 权限审批进行中
> 用法：按依赖顺序逐个修复，每项完成后勾选并在下方记录 commit

## 修复顺序总览

依赖关系决定了顺序：先修 data race（建立 `-race` 绿色基线），
再修生命周期接线（根因），随后 scope 语义、审计覆盖，最后测试质量。

---

## A. 并发问题（`go test -race ./internal/team/` 当前红色）

### [x] A1. PermissionFSM 在 store 锁外修改共享指针
- 位置：`internal/team/permission_fsm.go:48-52, 141, 157`
- `GetRequest`/`ListByRun` 返回 map 中的指针，bridge 在 RLock 下读
  `req.Status`（`permission_bridge.go:196, 209`），FSM 写侧无锁。

### [x] A2. bridge 配置字段 setter 写 / Request 读无同步
- 位置：`internal/team/permission_bridge.go:263-277` vs `:309-320`
- `auditFn`/`tracker`/`requestTimeout` 应由 `queueMu` 保护或改为构造注入。

### [x] A3. 测试 fake 无同步读写（-race 实测命中）
- 位置：`internal/team/member_runner_test.go:173` vs `:221`
- `recordingTurnRunner` 的字段被 runner goroutine 写、测试 goroutine 读。

### [ ] A4. pumpDisplay 持锁调用 pubsub Publish
- 位置：`internal/team/permission_bridge.go:512-519`
- 依赖外部包"永不阻塞"的隐含约定，先出锁再发布。

### [ ] A5. shutdown 后 member 状态写库竞态（-race 伴随出现）
- 位置：`TestMemberRunner_handleWake_RunError` 报 `sql: database is closed`
- member 状态迁移在 DB 关闭后仍在执行，shutdown 顺序需要收口。

### [ ] A6. delegate_runner.go:201 trailing goroutine slog.Debug 无锁读 group.Status（潜在竞态，-race 现未触发）

### [ ] A7. permission_queue.go 定时器回调读 req.ID 且捕获 caller ctx（现安全；对应原 E2/E3 硬化）

## B. 生命周期断线（根因：display 队列与 FSM/store 两套平行实现）

### [ ] B1. ResolveRequest 丢弃 scope，从不调用 fsm.Resolve
- 位置：`internal/team/permission_bridge.go:445-481`
- "Allow for Task" 等同 "allow once"；`PermissionFSM.Resolve` 生产零调用。

### [ ] B2. requestWithUI 从不调 store.CreateRequest
- 位置：`internal/team/permission_bridge.go:364-391`
- FSM/store 层操作空库：ListByRun/ListPendingByMember 永远为空，
  fsm.Cancel/fsm.Orphan 恒为 no-op。
- 注意：B1 依赖 B2（Resolve 需要请求已入库）。

### [ ] B3. permission_expired 生产不可达
- 位置：所有退出路径先 Dequeue 停掉 TTL 定时器（`permission_bridge.go:438, 473, 551`）
- 唯一调 fsm.Expire 的定时器被提前拆除。

### [ ] B4. SetAuditFunc 不传播给 FSM
- 位置：`internal/team/permission_bridge.go:257` vs `:263`
- FSM 构造时拿到 no-op 值副本，5 种 FSM audit 事件被静默丢弃。

## C. 权限语义缺陷

### [ ] C1. FindActiveGrant 忽略 TaskID 和 Scope
- 位置：`internal/team/permission_bridge.go:95-105`
- `scope:"call"` 的一次性授权实际变成 30 分钟 session 级放行；
  task 级 grant 跨 task 生效。B1/B2 接线后立即变成真实安全问题。

### [ ] C2. Hook 预批准与 allowedTools 白名单被 team 路径绕过
- 位置：`internal/agent/hooked_tool.go:92`、`internal/team/permission_bridge.go:311-335`
- hook 返回 allow 仍弹 UI；inner service 的检查被 bridge 跳过。

## D. 审计覆盖缺口

### [ ] D1. hook_allow/hook_deny 只声明未触发（即 M5-09）
- 位置：`internal/agent/hooked_tool.go:64-71, 93-100` 仅 slog。

### [ ] D2. grant_auto 缺 TaskID/RunID；late_response 缺 Team/Member 字段
- 位置：`internal/team/permission_bridge.go:320-324, 454-457`
- 各 action 审计字段不一致。

## E. 资源与错误处理

### [ ] E1. `_ = b.queue.Enqueue(...)` 吞错
- 位置：`internal/team/permission_bridge.go:391`。队列满时 TTL 兜底静默消失。

### [ ] E2. 定时器回调忽略 fsm.Expire 错误且捕获已取消的 caller ctx
- 位置：`internal/team/permission_queue.go:48`。

### [ ] E3. 重复 Enqueue 同一 ID 泄漏 Timer
- 位置：`internal/team/permission_queue.go:47-54` 不查重。

### [ ] E4. PermissionStore/GrantStore 无淘汰，进程内无限增长
- 位置：`internal/team/permission_bridge.go:154, 85`。

### [ ] E5. tracker 字段死代码（"聚焦 session"未实现）
- 位置：`internal/team/permission_bridge.go:228, 269-271`。

## F. 测试质量

### [ ] F1. "E2E"测试驱动生产从不调用的路径，掩盖 B1-B3
- 位置：`internal/team/permission_e2e_test.go`（fsm.Resolve/Cancel/Orphan 直调）。
- `:161-163` 把缺失的 audit 断言成了"已知变通"。

### [ ] F2. time.Sleep 同步（flake 源）
- 位置：`permission_e2e_test.go:115`、`e2e_test.go` 约 10 处。
- 改用 require.Eventually / channel。

### [ ] F3. TestCoderAgent golden 漂移（非 team 包）
- 位置：`internal/agent`，疑似上游 v0.84.0 改 title prompt 未更新 golden。

### [ ] F4. go vet：csync.Map 按值传递复制了 RWMutex
- 位置：`internal/csync/maps.go:137` JSONSchemaAlias。

---

## 完成记录

| 日期 | 问题 | Commit | 备注 |
|------|------|--------|------|
| 2026-08-14 | A1 | d5d6ff2b, 5b11000b | store Update+copies；FSM 状态迁移改走原子 Update |
| 2026-08-14 | A2 | 0585ea56 | setter 字段以 queueMu 同步 |
| 2026-08-14 | A3 | b6791aa7, d7b0a4f4 | fakes 同步 + delegate/queue 后续修复；全包 -race 绿 |
| 2026-08-14 | 超出原范围 | 67bfcd6b | CreateRequest 防御性拷贝，修复 A1 暴露的竞态 |
