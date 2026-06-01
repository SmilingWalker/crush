# 00 Source Map And Diff

## 输入一：Notion 方案

Notion 方案更像完整目标蓝图，重点是：

- `internal/team` 独立领域层。
- `TeamRunner` / `MateRunner` 长期运行时。
- `team_* tools` 明确协作协议。
- DB-backed mailbox/task/audit/outbox。
- permission bridge。
- compact TUI。
- A2A 作为未来 gateway。

对应材料见 `../notion-agent-team`。

## 输入二：当前 Repo 方案

当前 repo 方案更像安全落地路线，重点是：

- 四线推进：Runtime / Data / Safety / Product。
- M1 read-only parallel delegates。
- ActorContext 前置。
- 敏感文件 deny-list。
- MCP instructions 过滤。
- 成本预算。
- patch artifact 先于 direct write。
- 风险登记表。

对应材料见 `../agent-team-mode`。

## 核心差异

| 主题 | Notion 方案 | 当前 repo 方案 | 合并取舍 |
| --- | --- | --- | --- |
| 第一阶段 | Phase 0 runtime spike | M1 read-only delegates | 增加 M0.5 hidden runtime spike，再保留 M1 delegates |
| 运行时 | TeamRunner/MateRunner 明确 | DelegateRunner -> AgentRegistry | TeamRunner/MateRunner 承接长期 runtime，DelegateRunner 只做 M1 preview |
| 数据层 | teams/mates/tasks/mailbox/audit/outbox 一次定义 | M2/M3/M4 分批引入 | 保留分期，但补齐 Notion schema 字段和接口 |
| 协作协议 | `team_* tools` 明确 | 更偏 data plane 描述 | 新增 collaboration protocol 模块 |
| 安全 | permission bridge 为主 | 安全前置更强 | 采用当前 repo 的安全前置策略 |
| UI | compact team item | Team panel / mailbox UI 分期 | M3 先 compact item，M4+ 扩展 |
| A2A | Phase 9 gateway | M5 与 worktree 放一起 | 拆到 M6，避免过早混入 |

## 合并判断

Notion 方案应作为目标架构和硬协议来源，当前 repo 方案应作为落地节奏来源。
合并时不能让 Notion 的完整性吞掉当前 repo 的可验证性。

必须合入：

- M0.5 hidden runtime spike。
- `TeamRunner/MateRunner` 生命周期和状态机。
- `TeamService` 接口与事务不变量。
- `team_* tools` 协议。
- mailbox envelope、ack/read、correlation、control message。
- PermissionBridge 的 actor identity 流程。

可延后：

- 独立 `team_audit_events` 表。
- 独立 `team_event_outbox` 表。
- 自动 claim pending task。
- `team_file_locks`。
- process backend。
- A2A gateway。

不建议合入：

- 第一阶段直接做完整 DB-backed mailbox + task dependencies + permission bridge。
- 直接用 session messages 当 leader/mate 通信协议。
- 把 TeamRunner 塞进 `Coordinator.currentAgent`。
- 早期开放 `agent/team/workspace` 权限 scope。
- 早期允许 teammate direct write。
- 第一阶段用 A2A 作为内部协议。
- 第一版做复杂 dashboard。

