# 01 技术路线与迭代进度

本文件给出 Agent Team Mode 的整体技术路线。详细模块实现见后续文档。

## 1. 总路线

```text
M0 设计冻结
  -> M1 Team Preview / Parallel Delegates
  -> M2 Durable Team Skeleton
  -> M3a Scheduler + Recovery
  -> M3b Mailbox + Dependencies
  -> M4 Patch Artifact Write Mode
  -> M5 Direct Write / Worktree / A2A Gateway
```

路线原则：

- 先验证产品价值，再建设完整 team runtime。
- 先只读并行，再长期 teammate。
- 先 DB 状态，再 SSE 通知。
- 先 patch artifact，再 direct write。
- A2A 只做未来 gateway，不做内部第一层协议。
- 安全基础（ActorContext、workspace scope、敏感文件过滤）前置于功能开发。
- 成本控制（token/cost budget）随 team 规模同步引入。

## 2. 四线矩阵

| 里程碑 | A Runtime | B Data/API | C Safety/Isolation | D Product/QA |
| --- | --- | --- | --- | --- |
| M0 | 冻结 `AgentRegistry` 草案 | 冻结 schema 草案 | 冻结 ActorContext 草案 | 冻结 flag/UI 验收 |
| M1 | `DelegateRunner` | 可选内存 `RunGroup` | `ActorContext` skeleton + workspace-scoped read-only + 敏感文件 deny-list + MCP 过滤 | Delegates 面板 + E2E 冒烟测试 |
| M2 | `AgentRegistry` + `RunAgent` | team tables + snapshot/events | ActorContext 全链路 + cost budget | Team panel 初版 + 成本监控 |
| M3a | Scheduler + recovery | heartbeat/run state | Tool 四钩子 | Recovery UI |
| M3b | Mailbox + deps | mailbox/artifacts/deps | scoped grant（call/task/session 三级）+ audit | Mailbox/retry UI |
| M4 | patch runner | patch artifact | apply validation + bash 安全 | review/apply UI |
| M5 | worktree/remote adapter | lease/worktree/A2A mapping | direct write/network policy | worktree/A2A UI |

## 3. 推荐 PR 顺序

### PR-0: 文档与 feature flag 骨架

目标：

- 新增 `experimental.agent_team_preview=false`。
- flag off 不改变任何行为。
- 加开发文档入口。

涉及：

- `internal/config/config.go`
- `internal/config/load.go`
- config schema/tests

### PR-1: `ActorContext` skeleton + 敏感文件过滤

目标：

- 增加可选 actor/run context，旧调用为空兼容。
- 建立敏感文件默认 deny-list（`.env`、`credentials.*`、`*.key`、`*.pem`、`*.p12`）。
- 支持用户配置覆盖 deny-list。
- 为日志和 permission request 提供最小 actor 归因。

涉及：

- `internal/actor` 或 `internal/agent/run_context.go`
- `internal/agent/agent.go`
- `internal/permission/permission.go`
- `internal/proto/permission.go`

> **变更说明**：从原 PR-4 前移至 PR-1。ActorContext 是 debug 和安全审计的前提，必须在 delegate 运行前就位。

### PR-2: read-only tool policy + MCP 过滤

目标：

- 建立 teammate/delegate 的 workspace-scoped read-only 策略。
- 禁写、禁 bash、禁 job tools、禁默认 MCP。
- delegate 默认不注入 user/global MCP instructions。
- 按 actor policy 过滤 MCP tool list。

涉及：

- `internal/agent/tools/*`
- `internal/agent/coordinator.go`
- 新增 tool policy helper。
- MCP instructions 注入逻辑。

### PR-3: `DelegateRunner`

目标：

- 同一 leader session 下启动 1-3 个 one-shot delegates。
- 每个 delegate 独立 child session。
- 支持 cancel group。
- 每个 delegate 带 `ActorContext`。

涉及：

- `internal/agent/delegate_runner.go`
- `internal/agent/delegate_types.go`
- `internal/session/session.go`

### PR-4: Delegates UI + E2E 冒烟测试

目标：

- 展示 delegate status。
- 聚合结果回 leader。
- 支持 cancel all。
- 建立 E2E 测试框架（启动两个 delegate，验证结果聚合）。

涉及：

- `internal/ui/model/*`
- `internal/workspace/app_workspace.go`
- `internal/e2e/` 或集成测试目录

### PR-5: Team DB schema + sqlc

目标：

- 新增 M2 durable skeleton 表。
- 通过 migration/sqlc 测试。

涉及：

- `internal/db/migrations/*_team.sql`
- `internal/db/sql/team_*.sql`
- `sqlc.yaml` 不一定需要改。

### PR-6: `AgentRegistry` + cost budget

目标：

- coder 通过 registry 运行。
- 注册 teammate runtime。
- per-agent ReadyGate。
- `team_members` 增加 `max_cost`/`max_tokens` 字段。
- `team_runs` 增加 `max_cost` 字段。
- scheduler 层强制执行 budget。

涉及：

- `internal/agent/registry.go`
- `internal/agent/runtime.go`
- `internal/agent/coordinator.go`
- team DB schema（budget 字段）

> **变更说明**：将 cost budget 合并到 M2 的 AgentRegistry PR，避免 M2 交付时没有成本控制。

### PR-7: Team API + SSE

目标：

- Team snapshot/events API。
- `PayloadTypeTeamEvent`。
- client 识别 team event。

涉及：

- `internal/proto/team.go`
- `internal/server/team.go`
- `internal/backend/team.go`
- `internal/client/team.go`
- `internal/pubsub/events.go`

### PR-8: Scheduler + durable run + heartbeat（M3a）

目标：

- task queued -> claimed -> running -> completed。
- run cancel registry。
- heartbeat 机制：streaming 回调中每 30-60s 更新 `team_runs.heartbeat_at`。
- startup recovery：检测 heartbeat 过期判断 interrupted。
- Tool 四钩子（checkPermissions/validateInput/isReadOnly/isDestructive）。

涉及：

- `internal/team/scheduler.go`
- `internal/team/coordinator.go`
- `internal/team/recovery.go`
- `internal/agent/tools`（四钩子）

> **变更说明**：从原 PR-8 拆分。本 PR 只做 scheduler + recovery + heartbeat，不做 mailbox。

### PR-9: Mailbox + task dependencies（M3b）

目标：

- teammate ask leader。
- direct/broadcast message。
- message consumption prompt 构建算法。
- task dependencies。

涉及：

- `internal/team/message_protocol.go`
- `internal/team/prompt_builder.go`
- `internal/ui/model/*`

> **变更说明**：从原 PR-10 拆分到 M3b，明确不包含 scheduler 逻辑。

### PR-10: Permission/Audit hardening（M3b）

目标：

- scoped grant（call/task/session 三级，不做 agent/team/workspace）。
- ToolExecutionEvent。
- MCP/skills actor-aware。
- hook allow 也有 audit。

涉及：

- `internal/permission`
- `internal/agent/tools`
- `internal/agent/tools/mcp`
- `internal/skills`

> **变更说明**：Grant scope 从 7 级简化为 3 级（call/task/session），降低实现复杂度。剩余级别留到 M4+。

### PR-11: Patch artifact write mode

目标：

- teammate 产 patch artifact。
- leader review/apply。
- conflict artifact。

涉及：

- `internal/team/artifact_patch.go`
- `internal/team/apply_patch.go`
- UI diff/review。

## 4. 阶段退出条件

### M1 退出条件

- 两个 delegate 可并行跑 read-only task。
- result 能聚合回 leader。
- cancel all 生效。
- delegate 不能读 workspace 外路径。
- delegate 不能读敏感文件（`.env`、`credentials.*`、`*.key`、`*.pem`）。
- delegate 不注入 user/global MCP instructions。
- 日志和 permission request 包含 actor 归因。
- E2E 冒烟测试通过（启动两个 delegate，验证结果聚合）。
- flag off 时无行为变化。

### M2 退出条件

- create team -> spawn member -> assign task -> completed。
- `team_events.seq` 可 replay。
- team 级 token/cost budget 可配置且强制执行。
- old `/agent` 行为不变。
- old UI busy 不受 teammate 影响。
- ActorContext 全链路注入（permission、hook、audit）。

### M3a 退出条件

- task claim/lease 原子操作正确。
- heartbeat 过期 run 被标记 interrupted。
- startup recovery 重新入队 retry-able task。
- Tool 四钩子（isReadOnly/isDestructive）覆盖 delegate 可用工具。
- member 并发度可配置（`max_concurrent_runs`）。

### M3b 退出条件

- teammate 能向 leader 提问并继续。
- direct/broadcast message 投递正确。
- scoped grant（call/task/session）不扩散。
- 所有 tool call 有 audit event。
- message consumption 算法在 context window 溢出时优先保留 direct message + task description。

### M4 退出条件

- teammate 可产 patch。
- leader apply 前可 review。
- base mismatch 不覆盖文件。
- conflict 生成 artifact。
- bash 安全分析覆盖 teammate 可用 bash 命令。

### M5 退出条件

- direct write 有 file lease。
- worktree task 可独立跑测试。
- A2A 作为 gateway 接入，不替代内部 TeamRuntime。
