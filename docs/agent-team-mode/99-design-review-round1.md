# AgentTeam Mode 技术方案评审报告 (Round 1)

> 日期: 2026-06-02
> 评审方式: 7 人并行专项评审 (coherence, security-lens, product-lens, adversarial, design-lens, feasibility, scope-guardian)
> 评审范围: `docs/agent-team-mode/` 全部 14 份文档
> 文档类型: plan

## 评审团队

| 评审人 | 职责 | 激活原因 |
|--------|------|----------|
| coherence | 内部一致性、术语漂移、交叉引用完整性 | always-on |
| feasibility | 架构冲突、依赖缺口、迁移风险、可实现性 | always-on |
| security-lens | auth/authz、数据暴露、API 安全、威胁模型 | 计划涉及权限、敏感数据、工具策略 |
| product-lens | 前提有效性、战略后果、目标-工作对齐 | 重大架构承诺、多里程碑交付 |
| adversarial | 前提挑战、假设暴露、失败场景构造 | 新多 agent 架构、高风险域、greenfield |
| design-lens | 信息架构、交互状态、用户流、可访问性 | TUI 流、交互模式、UI 状态机 |
| scope-guardian | 范围对齐、非必要复杂度、过早框架化 | 10 里程碑、14 文档、30+ 表、13 PR |

## 总览

0 fixes applied. 48 items need attention (14 errors, 34 omissions). 22 FYI observations.

## 按严重级别汇总

| 级别 | 数量 | 关键主题 |
|------|------|----------|
| P0 | 2 | SessionAgent 实例复用模型、Run queued 语义鸿沟 |
| P1 | 29 | 安全模型缺陷、产品论证缺失、UI 状态机不完整、cancel/error 传播、实现风险、scope 膨胀 |
| P2 | 17 | 细粒度安全漏洞、UI 交互细节、adapter 参数映射 |
| FYI | 22 | 低优先级观察 |

---

## P0 — 必须修复

### 1. SessionAgent 是 per-instance 而非 per-session，MemberRunner 复用模型未定义

| 字段 | 值 |
|------|-----|
| Section | 05-runtime-control-plane (MemberRunner loop) |
| Type | error |
| Reviewer | adversarial (75) |
| Tier | manual |

tools/models/messageQueue 绑定在 SessionAgent 实例上，不是 session ID。MemberRunner 假设可以重复调用同一个 runner 的 Run()，但计划从未指定每个 MemberRunner 是获得自己的 SessionAgent 实例还是共享 coder 的。如果共享，SetTools/SetModels 的变更和 messageQueue 干扰会破坏状态。如果独立，AgentFactory 必须说明如何用受限工具构建 SessionAgent，而不经过 coordinator.buildAgent。

**Evidence:**
- `02-runtime-spike-m0-m05.md`: "MemberRunner loop -> wait prompt/cancel/stop -> build minimal TeamAgentCall -> call TurnRunner.Run(TeamAgentCall)"
- `05-runtime-control-plane.md`: "MemberRunner 是长期 runtime object... 每个 member 拥有一个可恢复的 child session"
- 现有 coordinator.go: `currentAgent SessionAgent` — 单实例，tools/models 通过 SetTools/SetModels 可变

### 2. Run (nil,nil) queued case 静默丢弃调用

| 字段 | 值 |
|------|-----|
| Section | 05-runtime-control-plane (Cancel/recovery) |
| Type | error |
| Reviewer | adversarial (75) + feasibility (100) |
| Tier | manual |

SessionAgent.Run 在 session busy 时入队调用并返回 (nil,nil)，但调用者无法区分"已入队"和"空结果完成"。入队的调用后续递归执行时，调用者无法得知执行已开始。MemberRunner 无法追踪实际 run 生命周期，可能写入错误的 team_runs 状态。adapter 必须将 (nil,nil) 转为 TurnQueued sentinel，但底层 SessionAgent 不提供入队调用开始执行时的回调或通知。M0.5 验收必须覆盖 queued 消歧。

**Evidence:**
- `00-technical-design.md`: "adapter 必须把 (nil,nil) case 转成 TurnRunResult{Status: TurnQueued}"
- `02-runtime-spike-m0-m05.md`: "queued turn 不触发 task completed、不写 completed run"
- 现有 agent.go: 入队后返回 (nil,nil)，递归处理时无外部通知

---

## P1 — 应该修复

### 错误 (Errors)

#### 3. Coordinator.Cancel(sessionID) 不足以支持 team cancel，替代合约未指定

| 字段 | 值 |
|------|-----|
| Section | 05-runtime-control-plane (Cancel semantics) |
| Type | error |
| Reviewer | adversarial (75) |
| Tier | manual |

现有 Cancel 只取消 context 并删除 messageQueue entry，不写 team run/member state/audit/event。MemberRunner.CancelMemberTurn 必须先写 team 级别状态变更（run interrupted、member idle/blocked、audit、event），然后调用 agent cancel。谁协调此顺序、步骤 2 失败后步骤 1 已提交怎么处理，均未定义。

#### 4. buildTools 硬编码所有工具构建，per-member 工具过滤侵入性强

| 字段 | 值 |
|------|-----|
| Section | 07-safety-permission-audit (M1 read-only policy) |
| Type | error |
| Reviewer | adversarial (75) |
| Tier | manual |

M1 delegate 需要 read-only 工具，但计划未指定 delegate 是获得独立 SessionAgent（用受限 buildTools）还是复用 coder 的 agent。构建的工具嵌入全局 permission.Service，M4 的 PermissionBridge 无法拦截其请求——除非工具被重建时注入 bridge。计划说"coordinator/buildTools filters by name"但 buildTools 用具体依赖（permissions, lspManager, filetracker）构建每个工具。

#### 5. ContentStore 并发 apply 无文件锁或写互斥

| 字段 | 值 |
|------|-----|
| Section | 04-team-domain-data-contract (Content store) |
| Type | error |
| Reviewer | adversarial (75) |
| Tier | manual |

base hash 验证和文件写入之间存在 TOCTOU 竞争。另一个进程（leader agent、另一个 patch apply、用户编辑器）可能在验证和写入之间修改目标文件。多文件 patch apply 不原子——3 个文件中写完 2 个后第 3 个失败，workspace 处于部分应用状态，无回滚机制。计划声明原子性（"先验证所有...再 apply"）但 filesystem 层面无法保证。

#### 6. M0.5 范围描述在文档间矛盾

| 字段 | 值 |
|------|-----|
| Section | 02-runtime-spike / README |
| Type | error |
| Reviewer | coherence (100) |
| Tier | manual |

doc 02 说"只做 hidden spike，不暴露给普通用户"，README 说"证明长期 MemberRunner 与现有 SessionAgent.Run 能安全共存"。spike 同时被描述为一次性验证和核心运行时假设的证明——实施者会对投入多少产生分歧。

#### 7. PermissionBridge 必须拦截 Request channel 生命周期，但底层互斥锁是瓶颈

| 字段 | 值 |
|------|-----|
| Section | 07-safety-permission-audit / 12-open-issues M4 |
| Type | error |
| Reviewer | feasibility (75) |
| Tier | manual |

现有 permission.Service.Request 持有 requestMu 互斥锁（序列化所有 permission 请求），包括 channel 等待期间。多个 teammate 同时请求权限时，此互斥锁成为瓶颈——一次只能处理一个 pending request。计划承认 max_permission_pending_per_team=3，但底层 service 一次只能处理一个。

#### 8. SessionAgent.Run 内部耦合远超 LLM 调用

| 字段 | 值 |
|------|-----|
| Section | 00-technical-design TeamAgentCall / 05-runtime-control-plane MemberRunner |
| Type | error |
| Reviewer | feasibility (75) |
| Tier | manual |

Run 内部做了：创建 user message、加载 session history、构建 fantasy agent、注入全局 MCP instructions、管理 streaming callbacks、更新 usage、生成 title、递归处理 queued messages。TeamAgentCall adapter 只映射输入参数，无法控制 Run 内部行为。prompt envelope 只能通过 Prompt 字段传入，但 title 生成（首次消息时 goroutine）、MCP 注入（从全局状态读取）、NonInteractive 设置等副作用需额外处理。

#### 9. MCP instructions 从全局状态读取，delegate 无法隔离

| 字段 | 值 |
|------|-----|
| Section | 07-safety-permission-audit MCP filtering |
| Type | error |
| Reviewer | feasibility (75) |
| Tier | manual |

SessionAgent.Run 从全局 mcp.GetStates() 读取 MCP instructions 并注入 system prompt。delegate 的 Run 执行时会看到与主 coder 相同的 MCP instructions。计划的工具过滤只移除了 MCP tools，但 MCP instructions 注入是独立的关注点。adapter 必须确保 SessionAgent 用排除了 MCP instructions 的 system prompt 创建，或修改 Run 方法接受抑制 MCP 注入的标志。

### 遗漏 (Omissions)

#### 10. 无 goroutine panic 恢复机制

| 字段 | 值 |
|------|-----|
| Section | 05-runtime-control-plane (Shutdown) |
| Type | omission |
| Reviewer | adversarial (75) |
| Tier | gated_auto |

MemberRunner 循环在 goroutine 中运行，未指定 panic recovery。TurnRunner.Run 可能 panic（SessionAgent.Run 有多个错误路径包括 context cancellation race），goroutine 静默退出。member 状态保持 "running" 直到 heartbeat 超时（30-60 秒）。应要求 recover() 标记 run failed、转换 member 状态、写 event/audit。

**Suggested fix:** MemberRunner goroutine 必须在循环体外套 recover()：(1) 标记当前 run 为 failed 并写入 error 详情，(2) 转换 member 到 failed 状态，(3) 写 event 和 audit，(4) 发布 SSE 通知。

#### 11. PermissionService 基于 channel，无跨进程恢复

| 字段 | 值 |
|------|-----|
| Section | 07-safety-permission-audit (PermissionBridge) |
| Type | omission |
| Reviewer | adversarial (75) |
| Tier | manual |

App 重启丢失所有 pending permission requests。member 保持 blocked，用户看不到请求，member 必须自行发现被阻塞并报告替代方案。计划说"转 orphaned"但未指定用户如何了解或解决 orphaned 请求。

#### 12. SSE team events 可能泄露跨 workspace 数据

| 字段 | 值 |
|------|-----|
| Section | 04-team-domain-data-contract (SSE auth) |
| Type | omission |
| Reviewer | security-lens (75) |
| Tier | manual |

计划声明 SSE 必须按 workspace 访问权过滤，但未提供实现机制。现有 pubsub 基础设施无文档化的 per-subscription workspace filter。如果 server 端 fan-out 不按 workspace_id 过滤，client 可能收到其他 workspace 的 team events。

#### 13. Mailbox prompt injection 可导致权限提升

| 字段 | 值 |
|------|-----|
| Section | 06-collaboration-protocol (Peer input) |
| Type | omission |
| Reviewer | security-lens (75) |
| Tier | manual |

缓解措施仅为 prompt envelope 中的文本免责声明。LLM 已知会遵从与 system prompt 冲突的注入指令。被入侵或 confused 的 teammate 可构造 mailbox 消息指示另一个 teammate 通过 team_report_status 或 team_send_message 泄露数据。team tools 不做内容过滤——team_report_status 可携带敏感文件内容。

#### 14. Sensitive deny-list 可通过 grep 结果绕过

| 字段 | 值 |
|------|-----|
| Section | 07-safety-permission-audit (deny-list) |
| Type | omission |
| Reviewer | security-lens (75) |
| Tier | manual |

deny-list 在路径级别强制执行，但 grep 搜索文件内容并返回匹配行。delegate 可用定向 grep 模式（如 "password="、"secret="）从 .env 等敏感文件中提取秘密，而无需直接读取文件。表格注释"结果需 redaction/deny-list"但无实现要求。

#### 15. Content store path traversal via artifact metadata

| 字段 | 值 |
|------|-----|
| Section | 04-team-domain-data-contract (Content store) |
| Type | omission |
| Reviewer | security-lens (75) |
| Tier | manual |

Patch artifacts 包含 workspace-relative 路径，apply service 解析为 filesystem 路径。M1 read-only 的路径防护规则（canonical resolution、symlink check、percent-decode、Unicode NFC、null byte rejection、workspace root containment）无等价规范存在于 M5 apply 路径解析。

#### 16. 无用户需求证据或问题验证

| 字段 | 值 |
|------|-----|
| Section | README / 00-technical-design |
| Type | omission |
| Reviewer | product-lens (75) |
| Tier | manual |

14 文档套件描述 6+ 里程碑工程工作，无任何引用用户痛点、支持请求、使用数据、竞争压力或假设用户 persona。整个动机是隐含的："多 agent 是下一个前沿"。计划风险：构建了一个没人要求的系统。

#### 17. M1 价值假设薄弱：read-only delegates vs. 现有 /agent tool

| 字段 | 值 |
|------|-----|
| Section | 02-runtime-spike / 03-roadmap |
| Type | omission |
| Reviewer | product-lens (75) |
| Tier | manual |

现有 /agent tool 已创建 child session 运行 sub-agent。增量是并行性 + read-only 限制，而非新能力类别。计划说 M1"证明产品价值"但未定义产品价值含义或如何衡量。想要并行调研的用户可以开多个终端。

#### 18. M1-M3 多里程碑间隔造成采用低谷

| 字段 | 值 |
|------|-----|
| Section | 03-four-plane-roadmap |
| Type | omission |
| Reviewer | product-lens (75) |
| Tier | manual |

M1 是 read-only demo，M2 是 debug-only DB scaffolding，M2.5 是不可见的 API hardening。第一个多 agent 交付真正协作价值的版本是 M3。可能数月工程无用户可采纳的产品。

#### 19. 无成功指标、采用目标或 kill criteria

| 字段 | 值 |
|------|-----|
| Section | 09-milestone-plan |
| Type | omission |
| Reviewer | product-lens (75) |
| Tier | manual |

每个里程碑有详细工程退出条件但零产品成功标准。无"M1 成功"定义（除了 demo 能跑）。无采用目标、无用户满意度信号、无指标会导致团队说"这不工作，我们应该停止"。

#### 20. internal/actor 包不存在，注入机制未指定

| 字段 | 值 |
|------|-----|
| Section | 07-safety-permission-audit / 11-implementation PR-1 |
| Type | omission |
| Reviewer | feasibility (75) |
| Tier | manual |

PR-1 创建 internal/actor 包，但计划未说明 ActorContext 如何注入现有 tool call chain。当前 tools 通过 context key 读取 SessionID，ActorContext 必须在同一注入点加入，所有现有 tool 都需从 tools.GetSessionFromContext 更新为也读 ActorContext。

#### 21. Coordinator 所有操作委托给 currentAgent 单例

| 字段 | 值 |
|------|-----|
| Section | 05-runtime-control-plane / 00-technical-design |
| Type | omission |
| Reviewer | feasibility (75) |
| Tier | manual |

Cancel、IsBusy、ClearQueue 等都只看 currentAgent。当 MemberRunner 在自己的 SessionAgent 上调用 Cancel(sessionID) 时，通过 Coordinator 路由的 cancel 请求会被忽略。Member session cancel 路由未指定。AgentRegistry 必须拦截指向 member session 的 cancel 请求。

#### 22. 无 team item 加载/skeleton 状态

| 字段 | 值 |
|------|-----|
| Section | 08-product-ui (TeamItemModel) |
| Type | omission |
| Reviewer | design-lens (75) |
| Tier | manual |

TeamItemModel 定义 loading_state (idle/loading_snapshot/reconnecting/stale) 但无 UI wireframe 或渲染规则。在 24 行 TUI 中，未处理的 loading 状态显示空白或过期数据。error state matrix 也不覆盖 loading states。

#### 23. Leader proposal item reject/decline 恢复路径缺失

| 字段 | 值 |
|------|-----|
| Section | 08-product-ui (Entry discovery) |
| Type | omission |
| Reviewer | design-lens (75) |
| Tier | manual |

用户选择 "run as single agent" 后，无文档指定 declined 渲染、leader 如何收到 decline 信号、或 snapshot 如何捕获 declined 状态。

#### 24. Insert aggregated result 无错误状态

| 字段 | 值 |
|------|-----|
| Section | 08-product-ui (M1 lifecycle) |
| Type | omission |
| Reviewer | design-lens (75) |
| Tier | manual |

insert 失败（leader session busy、model error）时，用户看到 "ready" 但 action 静默失败。无 retry 路径，无错误渲染。对无 undo 的 TUI 是用户可见的死胡同。

#### 25. Expanded team item 焦点与 chat timeline 滚动交互未定义

| 字段 | 值 |
|------|-----|
| Section | 08-product-ui (Focus/scroll) |
| Type | omission |
| Reviewer | design-lens (75) |
| Tier | manual |

焦点如何进入/离开 expanded item？tab 过最后一个 action 退出？esc 折叠还是仅移出焦点？无显式焦点状态机，实施者将产生不一致的键盘行为。

#### 26. 无 TUI 可访问性规范

| 字段 | 值 |
|------|-----|
| Section | 08-product-ui (Keyboard) |
| Type | omission |
| Reviewer | design-lens (75) |
| Tier | manual |

状态指示器可能仅依赖颜色而无文本标签。无 screen reader 支持或色盲安全设计提及。对开发者工具，终端可访问性影响使用 screen reader 或高对比度模式的视障用户。

#### 27. Store 接口膨胀：17 个 store 接口，每个仅一个实现

| 字段 | 值 |
|------|-----|
| Section | 04-team-domain-data-contract Service/Store |
| Type | omission |
| Reviewer | scope-guardian (75) |
| Tier | manual |

M2 的 6 个 + M3 的 5 个 + M4/M5 的 6 个 = 17 个 store 接口，全部只有 sqlc SQLite 一个实现。Service facade 已提供消费者边界，内部 store 接口增加了无多态收益的间接层。

**Suggested fix:** 用单一内部 `teamDB` 或 `teamQueries` struct 封装所有 sqlc queries per milestone，不设 per-entity store 接口。当第二个 store backend 实际出现时再提取接口。

#### 28. 30+ 数据库表跨 10 个里程碑

| 字段 | 值 |
|------|-----|
| Section | 04-team-domain-data-contract M2-M5 |
| Type | omission |
| Reviewer | scope-guardian (75) |
| Tier | manual |

现有代码库 4 张表（sessions, files, messages, read_files）。Team mode 一个功能增加 8 倍 DB 表面。当前 DB 有 7 个 migration；team mode 一项就产生超过现有全部代码库的 migration 数量。

#### 29. M1 DelegateRunner 与 MemberRunner 原语重复

| 字段 | 值 |
|------|-----|
| Section | 05-runtime-control-plane / 02-runtime-spike |
| Type | omission |
| Reviewer | scope-guardian (75) |
| Tier | manual |

M0.5 做 MemberRunner spike，M1 做 DelegateRunner（声称 M3 可复用），M3 做真正 MemberRunner——三轮实现同一概念（child session、actor context、tool filtering、cancel、result aggregation），无具体机制确保收敛。

**Suggested fix:** 消除 DelegateRunner 作为独立抽象。M1 应使用受限模式的 MemberRunner（in-memory state、read-only policy、one-shot lifecycle），验证实际 MemberRunner 代码路径。

#### 30. 19-30 个实际 PR 的协调成本过高

| 字段 | 值 |
|------|-----|
| Section | 11-implementation-plan |
| Type | omission |
| Reviewer | scope-guardian (75) |
| Tier | manual |

13 个 PR + 6 个建议子拆分（PR-6→6、PR-8→6、PR-10→5、PR-12→7）= 19-30 个实际 PR。每个 PR 必须维护 feature flag off 行为、通过测试、不破坏现有功能。对于首期演示是"并行只读调研 delegate"的功能，此 PR 数量级过重。

**Suggested fix:** 压缩到 8-10 个更大 PR。M0+M0.5 合一，M1 用 2 个而非 4 个。子拆分作为 PR 内实施笔记，非独立 PR。

#### 31. 7+ 新 Go package 用于一个功能

| 字段 | 值 |
|------|-----|
| Section | 00-technical-design |
| Type | omission |
| Reviewer | scope-guardian (75) |
| Tier | manual |

现有 ~15 个 internal package，team mode 增加近一半。internal/team 内又拆 member_runner/runner/mailbox/prompt_builder/recovery/artifacts/permission_bridge/apply_patch/content_store。

**Suggested fix:** internal/team 作为单一内聚包。只在出现明确依赖边界时提取（如 internal/actor 是合理的，因为 tools/permission/hooks 不能依赖 internal/team）。

---

## P2 — 考虑修复

### 错误 (Errors)

| # | Section | Issue | Reviewer | Conf | Tier |
|---|---------|-------|----------|------|------|
| 32 | 00-technical-design (TeamAgentCall) | TeamAgentCall adapter 必须桥接 provider options（Temperature, MaxOutputTokens 等）但映射未指定。TeamAgentCall 无这些字段。模型级参数从哪来？ | adversarial (75) | gated_auto |
| 33 | 01-architecture / 04-domain | 实现层术语 drift：部分 internal 代码引用使用 "teammate" 而非按命名约定应使用的 "member"。 | coherence (75) | gated_auto |

### 遗漏 (Omissions)

| # | Section | Issue | Reviewer | Conf | Tier |
|---|---------|-------|----------|------|------|
| 34 | 05-runtime + 06-protocol | 无 mailbox flood 限流。max_mailbox_depth 列出但超限行为未指定。broadcast 放大（1 消息→N receipts）无频率上限。 | security-lens (75) | manual |
| 35 | 01-architecture / 03-roadmap M6 | A2A gateway 无认证机制。M6 交付物含 gateway skeleton 但不要求 auth 设计作为 gate。未认证的 A2A 端点允许任意网络可达攻击者注入任务或窃取数据。 | security-lens (75) | manual |
| 36 | 07-safety (PermissionBridge) | Session scope grant 约束不足。"allow for session" 不在默认 UI 展示，但 session-scoped grant 实际允许什么（所有 tools？所有 resources？）未定义。 | security-lens (75) | manual |
| 37 | 04-team-domain (mailbox) | mailbox payload_json 无大小或结构验证。teammate 可生成极大 payload 消耗 DB 存储、拖慢查询或撑爆 prompt envelope 上下文窗口。 | security-lens (75) | manual |
| 38 | 03-roadmap / 04-domain | M3.5 change proposal 是精心设计的 review artifact 但可能无用户。不能 apply、pseudo_diff 非权威、未解释为什么比 mailbox 消息更好。 | product-lens (75) | manual |
| 39 | 08-product-ui (Entry) | Leader agent 自主性未指定——何时提议创建 team？用户发起还是 leader 建议？核心产品交互模型留给了 prompt engineering 实现细节。 | product-lens (75) | manual |
| 40 | 08-product-ui (Interaction) | CLI 用户管理多 agent team 的认知负荷可能超出采纳阈值。Team items、member rows、task boards、permission queues、proposal reviews、patch diff viewers——全在终端 UI 内，8 个快捷键、5 级焦点优先级。 | product-lens (75) | manual |
| 41 | 08-product-ui (Transcript) | Child transcript modal 缺少 empty/error/loading 状态。仅 "cannot load" fallback 已指定。 | design-lens (75) | manual |
| 42 | 08-product-ui (Permission) | Permission dialog esc 行为含糊。文档说"不 auto deny"但未指定关闭后请求是否保持 pending、下一条是否自动打开、用户如何重新访问被关闭的请求。 | design-lens (75) | manual |
| 43 | 08-product-ui (Multiple) | 多 team items 在 chat timeline 中无规范。多个 expanded items 超出 45% 高度预算。Tab 在 team items 间循环未定义。 | design-lens (75) | manual |
| 44 | 08-product-ui (M5 Patch) | Patch review apply 确认对话框内容未指定。对写入 workspace 文件的破坏性操作，确认应显示文件数、base hash 状态和 apply 后果。 | design-lens (75) | manual |
| 45 | 08-product-ui (M1 entry) | M1 delegate 入口点可发现性未指定。"Command dialog / action bar" 未在文档套件中定义。无快捷键、无 slash command、无触发机制。 | design-lens (75) | manual |
| 46 | 08-product-ui (Budget) | Budget exceeded 交互流不完整。错误状态列出 "increase budget" 但无对话框、输入框、快捷键或 API 端点支持预算修改。 | design-lens (75) | manual |
| 47 | 05-runtime-control-plane shutdown | FlushAll 在 message.Service 上而非 SessionAgent 上。TurnRunner 接口列出 FlushAll 但现有 SessionAgent 接口不暴露此方法。 | feasibility (75) | gated_auto |
| 48 | 04-team-domain-data-contract | sqlc transaction model 与计划 store 接口有差异。store 接受 Tx 参数但具体 Tx 类型未定义。需与 sqlc 的 DBTX/WithTx 对齐。 | feasibility (75) | gated_auto |

---

## FYI 观察点

低置信度观察，不需决策，仅供参考。

**架构与 scope:**
- team_event_counters 专用表对 SQLite 过度工程化，可用 transaction 内 max+1 替代 (scope-guardian, 50)
- 四线架构在证明 team mode 可行性前增加组织开销 (scope-guardian, 50)
- 11 个 team tools 在无真实消费者前就完整定义 (scope-guardian, 50)
- ContentStore 接口为未来 backend 设计但 M5 只有一个实现 (scope-guardian, 50)
- 6 种 mailbox message kinds + 2 reserved + 3 deferred 在无消费者前过度规格化 (scope-guardian, 50)
- Task dependency types 对 M3 的 max 3 members/serial execution 过度规格化 (scope-guardian, 50)
- M2.5 idempotency milestone 在证明 team mode 价值前就建基础设施 (scope-guardian, 50)
- ActorContext 12 个字段，M1 只消费 3 个，其余为 M2+ 预留 (scope-guardian, 50)

**安全:**
- Task claim race condition 可能将同一 task 分配给两个 member——测试计划已承认但无缓解规范 (security-lens, 50)
- M1 delegates 无预算执行——M2 控制不适用于 in-memory delegate groups (security-lens, 50)
- Idempotency key collision 可能泄露 response_ref 信息 (security-lens, 50)

**产品:**
- 机会成本：6 里程碑多 agent vs. 改善单 agent 体验的权衡不可见 (product-lens, 50)
- M3.5 pseudo_diff 格式看起来像 diff 但不是 diff，可能侵蚀信任 (product-lens, 50)

**运行时:**
- Prompt envelope 截断无反馈回路给 leader——被截断的消息不可见 (adversarial, 50)
- Event seq gap 检测假设 SSE 投递顺序等于插入顺序 (adversarial, 50)
- Cross-PR dependencies 无部分 PR 失败的回滚策略 (adversarial, 50)
- TeamWorkspace 组合增加 wiring 复杂度（Workspace 45+ 方法接口）(adversarial/feasibility, 50)
- Feature flag 共享 gate helper 函数签名和位置未指定 (feasibility, 50)
- TurnRunResult 引用 fantasy.AgentResult 类型名需在 spike 中验证 (feasibility, 50)

**UI:**
- Change proposal stale context 警告无解决流程 (design-lens, 50)
- Delegate failed state 缺少 error detail mapping enum (design-lens, 50)
- Team item archive action 缺少确认和归档后渲染 (design-lens, 50)
- M3 expanded item activity/mailbox summary 滚动边界超出 10 行最小 viewport (design-lens, 50)
- 缺少里程碑状态机 per-phase timeline (coherence, 50)

---

## 残余风险

1. **M0.5 spike 是最高风险 gate，无文档化备选方案** — 如果 SessionAgent.Run 无法被外部 loop 安全驱动，整个 M3 架构需要重新设计。(adversarial)
2. **SQLite 写竞争** — 高 mailbox 吞吐量下，team_events + team_audit_events + domain writes 单事务写入可能成为瓶颈。无性能目标指定。(adversarial)
3. **LLM 对 prompt 免责声明的遵从是概率性的** — 对 peer input safety（mailbox、dependency summaries），结构化控制仅依赖 prompt 声明。(security-lens)
4. **Content store 休息时加密延后到 M5 后** — patch artifacts（含源代码）在本地 filesystem 未加密存储。多用户机器上可能暴露。(security-lens)
5. **无 audit log 保留或清理策略** — team_audit_events 将无限增长。(security-lens)
6. **工程优秀但产品空洞** — 每个里程碑有技术退出条件但无产品成功标准。(product-lens)
7. **SessionAgent.Run 递归 queue 处理** — 入队调用被内部递归处理，绕过 MemberRunner 的 max_concurrent_runs=1 控制。(feasibility)
8. **Cost float-to-micros 转换** — session.Cost 是 float64，team domain 用 integer micros。跨域成本聚合的舍入行为未指定。(feasibility)
9. **Crash 后 session 一致性** — member mid-stream crash 时 session messages 可能不一致（部分 assistant message + tool calls 无 tool results）。计划只覆盖 team domain state 恢复。(feasibility)
10. **M6 对 M2-M5 设计决策的隐性影响** — ContentStore 接口、A2A mapping、runtime adapter interfaces 为 M6 设计，如 M6 被砍或重设计则成死重。(scope-guardian)

## 延迟问题

| # | Question | Source |
|---|----------|--------|
| 1 | SessionAgent 实例在 MemberRunner 中的所有权模型？独立实例还是共享？ | adversarial |
| 2 | MemberRunner 如何检测入队的 SessionAgent.Run 执行开始？ | adversarial |
| 3 | M0.5 spike 失败后的备选方案？ | adversarial |
| 4 | M5 patch apply 部分写入失败时的回滚机制？ | adversarial, security-lens |
| 5 | SSE 跨 workspace 隔离的精确实现机制？ | security-lens |
| 6 | mailbox payload_json 的大小限制？ | security-lens |
| 7 | 是否有用户研究验证多 agent 需求？ | product-lens |
| 8 | M0-M3 的预估时间线？ | product-lens |
| 9 | 多 agent mode 可用后现有 /agent tool 的命运？ | product-lens |
| 10 | TUI 应针对什么终端可访问性标准？ | design-lens |
| 11 | TeamAgentCallAdapter 如何处理 SessionAgent.Run 中的 title 生成副作用？ | feasibility |
| 12 | MemberRunner 创建 child session 用 CreateTaskSession 还是新路径？ | feasibility |
| 13 | PermissionBridge 如何处理并发 team permission request——是否绕过 requestMu？ | feasibility |
| 14 | M5 ContentStore filesystem 路径约定（.crush/team-artifacts/）？ | feasibility |
| 15 | M1 的 read-only delegate demo 是否作为独立功能交付，还是纯技术踏脚石？ | scope-guardian |
| 16 | 预期实施团队规模？14 文档 + 19+ PR 暗示多人团队。 | scope-guardian |

---

## 覆盖度

| Persona | Status | Findings | Proposed | Decisions | FYI | Residual |
|---------|--------|----------|----------|-----------|-----|----------|
| coherence | completed | 5 | 0 | 2 | 1 | 3 |
| feasibility | completed | 11 | 3 | 5 | 3 | 5 |
| security-lens | completed | 11 | 0 | 8 | 3 | 5 |
| product-lens | completed | 9 | 0 | 6 | 3 | 4 |
| adversarial | completed | 12 | 2 | 6 | 4 | 5 |
| design-lens | completed | 15 | 0 | 11 | 4 | 5 |
| scope-guardian | completed | 13 | 1 | 5 | 7 | 4 |

---

## 建议优先行动

1. **M0.5 spike 前先回答**: SessionAgent 实例是共享还是独立？queued case 如何检测？失败后怎么办？
2. **添加产品论证**: README 开头加 2-3 段用户痛点、目标用户和成功指标。为每个用户可见里程碑定义 kill criteria。
3. **压缩 scope**: 合并 DelegateRunner 和 MemberRunner（M1 直接用受限 MemberRunner）、减少 store 接口（单一 teamDB struct）、压缩 PR 数量到 8-10 个。
4. **补全安全模型**: grep deny-list（排除敏感文件于搜索范围）、SSE workspace 隔离、mailbox 输出过滤、M5 apply 路径防护。
5. **补全 UI 状态机**: 定义 loading/error/focus 状态、可访问性要求、permission esc 后行为。
6. **解决 PermissionService 瓶颈**: M4 前必须决定是绕过 requestMu 还是重构 permission.Service 支持并发 pending。
