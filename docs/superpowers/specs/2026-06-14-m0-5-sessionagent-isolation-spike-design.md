# M0.5 Spike：SessionAgent 实例隔离验证

> 设计文档 | 日期：2026-06-14 | 状态：已批准（brainstorming 产出）
> 分支：`agent-team` | 估时：~1 人天
> 关联：`docs/agent-team-mode/plan/00-final-ruling.md`（🔴 致命风险闸门："M0.5 必须验证隔离可行性，不通过不进 M1"）
> 后续：本 spec 批准后交由 `writing-plans` 产出实现计划

---

## 一、背景

整个 M1（SubAgent 单兵地基）的 `AgentFactory` 架构依赖一个前提：**每个 runner 持有独立的 `SessionAgent` 实例，coder 的 `SetTools`/`SetModels` 不能污染 sub-agent。** 设计文档将此标记为 🔴 致命风险，要求在进入 M1 前用 spike 实证验证。

### 代码现状（已读实际代码，非计划文档的引用）

- `buildAgent`（`internal/agent/coordinator.go:438`）**每次调用都 `NewSessionAgent(...)` 创建全新实例**，不复用 `c.currentAgent`。
- `NewSessionAgent`（`internal/agent/agent.go:141`）为每个实例分配**全新**的 `csync` 容器：
  - `tools` → `csync.NewSliceFrom`（agent.go:153）
  - `largeModel`/`smallModel` → `csync.NewValue`（145-146）
  - `messageQueue`/`activeRequests` → `csync.NewMap()`（156-157）
- `SetTools`/`SetModels`（agent.go:1275/1280）只改 receiver 自己的容器。

**因此隔离极可能天然成立。** 本 spike 的真正价值不是"猜结果"，而是：① 实证证明；② 锁定 M1 回归闸门；③ 排查代码阅读排不掉的**隐藏 aliasing**（`buildTools` 是否在两次调用间返回共享的 tool 对象指针）。

---

## 二、范围

### 目标
1. 实证 4 个容器（tools / models / messageQueue / activeRequests）按实例独立。
2. 经 **真实构造路径**（`buildTools`，非 mock）排查 tool 对象指针是否跨 agent 共享；对共享的 tool 判断是否持有 per-agent 可变状态。
3. 产出一个跨平台、无 LLM/VCR、可作 M1 长期回归闸门的测试。

### 非目标
- 不测 `Run()` 执行、并发、LLM 行为（属 M1-05）。
- 不测递归深度防护（属 M1-02）。
- 不接入真实 provider / 网络调用。
- 不验证**共享注入服务**（sessions / messages / permissions 等按设计在多个 agent 间共享，是另一条隔离轴线）——M1 须单独论证对这些服务的并发访问。

---

## 三、Test 1：容器隔离（直接 `NewSessionAgent`）

**位置**：`internal/agent/agent_isolation_test.go`，`package agent`（白盒，可类型断言到 `*sessionAgent` 访问内部字段）。

**步骤**：
1. 构造 `a1 := NewSessionAgent(optsA).(*sessionAgent)`、`a2 := NewSessionAgent(optsB).(*sessionAgent)`。
2. 断言 `a1 != a2`（指针不同）。
3. 突变 `a1`：`a1.SetModels(largeA, smallA)`、`a1.SetTools([]fantasy.AgentTool{stub("t1")})`、`a1.messageQueue.Set("sess-x", …)`、`a1.activeRequests.Set("sess-x", cancelFn)`。
4. 断言 `a2` 完全不受影响：
   - `a2.Model()` 仍为原始值（`SetModels` 未泄漏）。
   - `a2` 的 tools 未变（`SetTools` 未泄漏）。
   - `a2.messageQueue.Get("sess-x")` 返回 `false`（队列未泄漏）。
   - `a2.activeRequests.Get("sess-x")` 返回 `false`（活跃请求未泄漏）。

**预期**：PASS（`NewSessionAgent` 每次分配新 `csync` 容器）。**意义**：把这个"构造即隔离"的不变量钉成回归测试。

---

## 四、Test 2：`buildTools` 别名探测（真实路径，无 provider）

**关键洞察**：`buildTools` 对一个 `AllowedTools` 不含 `"agent"` 的 sub-agent 不会递归 `agentTool`，也**不需要 provider/VCR/LLM** —— 只需 cfg + Agent 配置 + permissions + workingDir + sessions/messages/history/filetracker/lspClients/notify。因此可低成本走真实路径。

**步骤**：
1. 构造一个**最小** `*coordinator`，只填 `buildTools` 会访问的字段（实现计划阶段从 `buildTools` 函数体精确枚举字段清单）。
2. 调 `c.buildTools(ctx, subAgentCfg, true)` **两次** → `tools1`、`tools2`。
3. 对同时出现在两个切片里的每个 tool 名，比较**对象指针同一性**（`fantasy.AgentTool` 接口值的指针/`reflect` 比较）。
4. 对任一**共享**的 tool，检查它是否持有 per-agent 可变状态（对比仅持有无状态 service 引用）。

**输出**：一张表 —— tool 名 → 是否共享 → 是否有可变状态。

---

## 五、判定标准与 M1 决策

| 探测结果 | 判定 | M1 动作 |
|---|---|---|
| Test 1：4 个容器全部独立 | **必须通过** | 若失败 → `NewSessionAgent` 本身有问题 → M1 需重构（按读码结果预期会过） |
| Test 2：所有 tool 指针全新 | 🟢 绿灯 | AgentFactory 可共享或重建；**采用每 runner 重建**（安全默认） |
| Test 2：部分 tool 共享，但全部**无状态** | 🟡 可接受 | AgentFactory 仍每 runner 重建 tools（廉价的 belt-and-suspenders） |
| Test 2：部分 tool 共享**且持可变状态** | 🔴 红 | AgentFactory **必须**每 runner 重建 tools；文档记录哪些 tool 是有状态的 |

**无论探测结果如何的净契约**：M1 的 `AgentFactory.BuildRunner` 每次为 runner 重建 tools + models。spike 只是告诉我们这是"必须"还是"仅 prudent"。

---

## 六、产物与处置

- **文件**：`internal/agent/agent_isolation_test.go`（`package agent`，白盒）。
- **跨平台**：无 Windows skip、无 API key、无 VCR 磁带 → CI 友好。
- **角色**：提交后成为 **M1 的长期回归闸门**。
- **gate 条件**：两个测试均绿 + Test 2 的 aliasing 表已记录 → 进入 **M1-01**。
- **若 Test 2 发现红色 aliasing**：暂停 M1，先在本 spec 附件记录有状态 tool 清单，并把"AgentFactory 每 runner 重建 tools"提升为 M1 的硬约束写进 M1-04/M1-05 的任务描述。

### Spike 结果（2026-06-14 执行，commit `66512ae5` + `815cbfe`）

- **Test 1（容器隔离）**：PASS。两个 `NewSessionAgent` 实例的 4 类容器（tools / large+smallModel / messageQueue / activeRequests）互不影响——突变 `a1` 后 `a2` 完全不变。
- **Test 2（buildTools aliasing）**：PASS。15 个 sub-agent tool（bash / edit / multiedit / glob / grep / view / write / ask_user_questions / download / fetch / todos / crush_info / crush_logs / job_output / job_kill）在两次 `buildTools` 调用后**全部为不同对象指针**（全 `false`），无 aliasing。
- **进一步结论（code review 核实源码）**：buildTools 路径上**无任何 memoization**——连被排除的 `agent` / `agentic_fetch` 也是方法（`c.agentTool` / `c.agenticFetchTool`），每次调用 `fantasy.NewParallelAgentTool(...)` 产出新指针；所有 tool 的具体类型均为 `*funcToolWrapper[T]` 指针，故 `sameTool` 始终走指针比较分支。
- **判定**：🟢 绿灯。
- **M1 待办（carry-forward）**：在 M1-04/M1-05 前补测 `agent` / `agentic_fetch` 的 aliasing（二者为 coordinator 方法 `c.agentTool` / `c.agenticFetchTool`，是未来最可能被加 `sync.Once` 缓存的形状）；并按上面非目标所述，单独论证对**共享注入服务**（sessions/messages/permissions）的并发访问。
- **净契约**：M1 的 `AgentFactory.BuildRunner` 每 runner 重建 tools + models 仍为**安全默认**（spec 第五节），非强制 → **可进入 M1-01**。

---

## 七、风险

- `buildTools` 实际访问的 coordinator 字段比预期多 → 实现阶段可能需要补最小 harness（不影响 spike 结论，只影响测试搭建成本）。
- `fantasy.AgentTool` 接口值的指针比较语义需在实现时确认（用 `reflect` 还是取地址），属实现细节，不改变探测意图。
