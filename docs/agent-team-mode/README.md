# AgentTeam Mode 技术方案

本文档集是 `crush` AgentTeam Mode 的正式实施方案，是后续拆 issue、写代码、做验收
和演示的唯一工程口径。

本目录的每份文档都必须回答一个落地问题：改哪些模块、增加哪些数据结构、暴露哪些 API、
UI 怎么承载、每个里程碑交付什么，以及如何证明可以进入下一阶段。

## 目标

AgentTeam Mode 要让一个 leader agent 可以创建、管理和协作多个 teammate。第一阶段
不追求自动全能，而是把多智能体能力拆成可验证的阶段：

```text
M0    方案冻结与边界对齐
M0.5  隐藏 Runtime Spike
M1    Safe Team Preview / Read-only Delegates
M2    Durable Team Domain + TeamService
M3    In-process TeamRunner + Mailbox + Scheduler
M4    Permission Bridge + Audit + Shared Task Board
M5    Safe Patch Artifact Write + Apply Conflict Check
M6    Worktree / Process Backend / A2A Gateway
```

## 设计不变量

1. 旧 `/agent` API 继续代表默认 coder，不改成多 agent 路由。
2. `SessionAgent.messageQueue` 不是 team mailbox。
3. Team 状态以 SQLite 为事实源，SSE/pubsub 只做通知。
4. leader 和 teammate 不共享完整上下文，只通过 typed mailbox、shared task board、artifact 通信。
5. teammate 在 M5 前不直接写主工作区；写作业先产 patch artifact，由 leader review/apply。
6. 权限默认最小授权；任何写工具、bash、MCP write 都必须有 actor identity、scope 和 audit。
7. A2A 是 M6 gateway，不是内部第一版协议。

## 文档地图

| 文档 | 作用 | 读者 |
| --- | --- | --- |
| `00-technical-design.md` | 全局技术设计、代码落点、端到端链路 | 所有人 |
| `01-architecture-principles.md` | 架构原则、模块边界、关键不变量 | 架构/实现 |
| `02-runtime-spike-m0-m05.md` | M0/M0.5 的 runtime spike 任务和验收 | runtime 实现 |
| `03-four-plane-roadmap.md` | 四线推进和 M0-M6 功能矩阵 | 计划/研发 |
| `04-team-domain-data-contract.md` | DB schema、Service、API、event、snapshot contract | backend/data |
| `05-runtime-control-plane.md` | AgentRegistry、TeamRunner、MemberRunner、scheduler、recovery | runtime |
| `06-collaboration-protocol.md` | team tools、mailbox、task、prompt injection、peer safety | protocol |
| `07-safety-permission-audit.md` | ActorContext、tool policy、permission bridge、audit、MCP/bash 安全 | safety |
| `08-product-ui-observability.md` | TUI 信息架构、各阶段 UI、debug/observability | product/UI |
| `09-milestone-plan-m0-m6.md` | 每个里程碑的功能、实现、交付、退出条件 | PM/研发 |
| `10-testing-risk-gates.md` | 测试矩阵、风险门禁、E2E 验收 | QA/研发 |
| `11-implementation-plan.md` | 实施拆分、PR 顺序、跨模块依赖 | tech lead |
| `12-open-architecture-issues.md` | 开工前和阶段前必须冻结的问题 | tech lead |

## 推进口径

研发推进时按下面顺序读文档：

1. 先读 `00-technical-design.md` 和 `01-architecture-principles.md`，确认边界和不变量。
2. 按 `09-milestone-plan-m0-m6.md` 选择当前里程碑，只实现该阶段允许的功能。
3. 对照 `03-four-plane-roadmap.md` 检查 runtime、data、safety、product 四条线是否齐平。
4. 实现前读对应专题文档：data 看 `04`，runtime 看 `05`，协作协议看 `06`，权限安全看 `07`，UI 看 `08`。
5. 每个 PR 按 `11-implementation-plan.md` 切片，完成后用 `10-testing-risk-gates.md` 做退出检查。

如果某个实现想跳阶段，必须先更新 `12-open-architecture-issues.md` 并记录决策原因。

## 最早可演示版本

M1 可以演示“leader 并行派出多个只读 delegate 并聚合结果”。这不是完整 team mode，
但可以证明产品价值和安全边界：

```text
leader session
  -> delegate A: read-only research
  -> delegate B: read-only review
  -> delegate C: read-only test analysis
  <- aggregate result into leader
```

真正的长期 teammate 演示在 M3：

```text
leader creates team
leader spawns teammate
leader sends task/message
teammate receives mailbox/task
teammate runs one turn
teammate reports status
TUI shows team/member state
```

## 推荐推进顺序

1. 先做 M0/M0.5，证明长期 MemberRunner 与现有 `SessionAgent.Run` 能安全共存。
2. 再做 M1，用只读 delegates 给用户一个可见演示，同时完成 ActorContext 和只读安全底座。
3. M2 建 durable domain，不急着跑完整 teammate。
4. M3 把 TeamRunner、scheduler、mailbox 串成第一个真实 team loop。
5. M4 补权限、审计和 shared task board 的硬约束。
6. M5 才开放 patch artifact 写作业。
7. M6 再考虑 worktree、process backend 和 A2A gateway。
