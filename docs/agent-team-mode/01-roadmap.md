# 01 技术路线与迭代进度

本文件给出 Agent Team Mode 的整体技术路线。详细模块实现见后续文档。

## 1. 总路线

```text
M0 设计冻结
  -> M1 Team Preview / Parallel Delegates
  -> M2 Durable Team Skeleton
  -> M3 Mailbox + Scheduler + Recovery
  -> M4 Patch Artifact Write Mode
  -> M5 Direct Write / Worktree / A2A Gateway
```

路线原则：

- 先验证产品价值，再建设完整 team runtime。
- 先只读并行，再长期 teammate。
- 先 DB 状态，再 SSE 通知。
- 先 patch artifact，再 direct write。
- A2A 只做未来 gateway，不做内部第一层协议。

## 2. 四线矩阵

| 里程碑 | A Runtime | B Data/API | C Safety/Isolation | D Product/QA |
| --- | --- | --- | --- | --- |
| M0 | 冻结 `AgentRegistry` 草案 | 冻结 schema 草案 | 冻结 ActorContext 草案 | 冻结 flag/UI 验收 |
| M1 | `DelegateRunner` | 可选内存 `RunGroup` | workspace-scoped read-only | Delegates 面板 |
| M2 | `AgentRegistry` + `RunAgent` | team tables + snapshot/events | ActorContext 全链路 | Team panel 初版 |
| M3 | Scheduler + recovery | mailbox/artifacts/deps | scoped grant + audit | Mailbox/retry UI |
| M4 | patch runner | patch artifact | apply validation | review/apply UI |
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

### PR-1: read-only tool policy hardening

目标：

- 建立 teammate/delegate 的 workspace-scoped read-only 策略。
- 禁写、禁 bash、禁 job tools、禁默认 MCP。

涉及：

- `internal/agent/tools/*`
- `internal/agent/coordinator.go`
- 新增 tool policy helper。

### PR-2: `DelegateRunner`

目标：

- 同一 leader session 下启动 1-3 个 one-shot delegates。
- 每个 delegate 独立 child session。
- 支持 cancel group。

涉及：

- `internal/agent/delegate_runner.go`
- `internal/agent/delegate_types.go`
- `internal/session/session.go`

### PR-3: Delegates UI

目标：

- 展示 delegate status。
- 聚合结果回 leader。
- 支持 cancel all。

涉及：

- `internal/ui/model/*`
- `internal/workspace/app_workspace.go`

### PR-4: `ActorContext` skeleton

目标：

- 增加可选 actor/run context。
- 旧调用为空兼容。

涉及：

- `internal/actor` 或 `internal/agent/run_context.go`
- `internal/agent/agent.go`
- `internal/permission/permission.go`
- `internal/proto/permission.go`

### PR-5: Team DB schema + sqlc

目标：

- 新增 M2 durable skeleton 表。
- 通过 migration/sqlc 测试。

涉及：

- `internal/db/migrations/*_team.sql`
- `internal/db/sql/team_*.sql`
- `sqlc.yaml` 不一定需要改。

### PR-6: `AgentRegistry`

目标：

- coder 通过 registry 运行。
- 注册 teammate runtime。
- per-agent ReadyGate。

涉及：

- `internal/agent/registry.go`
- `internal/agent/runtime.go`
- `internal/agent/coordinator.go`

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

### PR-8: Scheduler + durable run

目标：

- task queued -> claimed -> running -> completed。
- run cancel registry。

涉及：

- `internal/team/scheduler.go`
- `internal/team/coordinator.go`
- `internal/team/recovery.go`

### PR-9: Permission/Audit hardening

目标：

- scoped grant。
- ToolExecutionEvent。
- MCP/skills actor-aware。

涉及：

- `internal/permission`
- `internal/agent/tools`
- `internal/agent/tools/mcp`
- `internal/skills`

### PR-10: Mailbox + recovery UI

目标：

- teammate ask leader。
- direct/broadcast message。
- retry/pause/resume。

涉及：

- `internal/team/message_protocol.go`
- `internal/ui/model/*`

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
- flag off 时无行为变化。

### M2 退出条件

- create team -> spawn member -> assign task -> completed。
- `team_events.seq` 可 replay。
- old `/agent` 行为不变。
- old UI busy 不受 teammate 影响。

### M3 退出条件

- teammate 能向 leader 提问并继续。
- scheduler 重启后能处理 interrupted run。
- permission request 显示 actor。
- 所有 tool call 有 audit event。

### M4 退出条件

- teammate 可产 patch。
- leader apply 前可 review。
- base mismatch 不覆盖文件。
- conflict 生成 artifact。

### M5 退出条件

- direct write 有 file lease。
- worktree task 可独立跑测试。
- A2A 作为 gateway 接入，不替代内部 TeamRuntime。

