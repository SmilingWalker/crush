# AgentTeam Mode 技术设计方案

本目录整合两套输入：

1. `../deprecated/notion-agent-team`：偏完整目标架构，强调 `internal/team`、长期
   `TeamRunner/MateRunner`、DB mailbox/task/audit/outbox、permission bridge、
   compact TUI、A2A gateway。
2. `../deprecated/agent-team-mode`：偏渐进落地，强调四线推进、read-only delegates、
   ActorContext 前置、安全隔离、成本预算、patch artifact、风险登记。

最终路线不是二选一：

- 保留 Notion 的 Phase 0 runtime spike，用来尽早验证长期 teammate runtime。
- 保留当前 repo 的四线推进法，用来组织工程落地。
- 把 Notion 的 `TeamRunner/MateRunner/team_* tools/TeamService` 补进当前方案。
- 把当前 repo 的安全前置、敏感文件 deny-list、MCP 过滤、成本预算、patch-first
  写入策略作为默认门禁。

## 最终路线

```text
M0    方案冻结与边界对齐
M0.5  隐藏 Runtime Spike
M1    Safe Team Preview / Read-only Delegates
M2    Durable Team Domain + TeamService
M3    In-process TeamRunner + Mailbox + Scheduler
M4    Permission Bridge + Audit + Shared Task Board
M5    Safe Patch Artifact Write + File Coordination
M6    Advanced Runtime: Worktree / Process Backend / A2A Gateway
```

## 文档地图

| 文档 | 作用 |
| --- | --- |
| `00-source-map-and-diff.md` | 对比 Notion 与当前 repo 方案的差异。 |
| `01-merged-architecture-principles.md` | 合并后的架构原则和不变量。 |
| `02-runtime-spike-m0-m05.md` | M0/M0.5 runtime spike 设计。 |
| `03-four-plane-roadmap.md` | 四线推进矩阵。 |
| `04-team-domain-data-contract.md` | DB、Service、Event、API contract。 |
| `05-runtime-control-plane.md` | TeamRunner、MateRunner、AgentRegistry。 |
| `06-collaboration-protocol.md` | mailbox、task、team tools。 |
| `07-safety-permission-audit.md` | ActorContext、权限、审计、MCP/tool policy。 |
| `08-product-ui-observability.md` | TUI、debug snapshot、event replay、成本展示。 |
| `09-milestone-plan-m0-m6.md` | M0-M6 详细交付路线。 |
| `10-testing-risk-gates.md` | 测试矩阵、风险门禁。 |
| `11-agent-review-synthesis.md` | 三个审查 agent 的综合结论。 |
| `12-open-architecture-issues.md` | 进入实现前必须钉死的架构问题。 |
