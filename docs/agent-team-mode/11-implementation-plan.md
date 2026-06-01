# 11 实施拆分与 PR 顺序

本文件把技术方案拆成可以推进的 PR 序列。每个 PR 应尽量保持一个清晰边界，并带测试。

## Issue-ready checklist

每个 PR 开工前必须拆成 3-8 个 issue，并补齐下面字段：

```text
Issue:
Milestone:
Depends on:
Files/modules:
New types/functions:
DB/API changes:
UI changes:
Tests:
Verification command:
Flag behavior:
Rollback:
```

拆分规则：

- 一个 issue 只拥有一个主要模块或一条垂直 user-visible path。
- 涉及 DB migration 的 issue 不同时做 TUI。
- 涉及 server/client contract 的 issue 必须同 PR 内双端实现。
- 涉及 permission/tool policy 的 issue 必须同时声明 audit 行为。
- flag off 行为是每个 issue 的验收项，不只在 PR 总体验收。

## PR-0：feature flag 与文档入口

目标：

- 新增 `experimental.agent_team_preview=false`。
- 新增 `experimental.agent_team=false`。
- flag off 时无行为变化。
- 保持文档入口清楚。

涉及：

- `internal/config`
- config schema/tests
- docs

验收：

- config 默认 false。
- UI/API/tools/prompt 不因 flag off 改变。

## PR-1：ActorContext 与 read-only policy skeleton

目标：

- 新增 actor identity。
- 新增 ToolPolicyAdapter/metadata registry。
- 给 tool execution、permission、log 预留字段。
- 建立 workspace-scoped path guard。
- 建立 sensitive deny-list。

涉及：

- `internal/actor` 或 `internal/agent/run_context.go`
- `internal/agent/tools`
- `internal/agent/hooked_tool.go`
- `internal/permission`
- `internal/proto/permission`

验收：

- old permission request 兼容。
- 无 metadata 的工具默认不给 delegate/member 使用。
- delegate path guard 可单测。
- `.env` deny 可单测。

## PR-2：MCP/skills actor-aware filtering

目标：

- delegate 默认看不到 user/global MCP instructions。
- MCP tools 默认不暴露给 M1 delegate。
- project MCP 必须显式 `team_visible`。

涉及：

- `internal/agent/tools/mcp`
- tool build options
- skills loading/filtering

验收：

- M1 delegate prompt 不含 user/global MCP instructions。
- MCP tool list 为空或只含 team visible。

## PR-3：DelegateRunner

目标：

- 一次启动 1-3 个 read-only delegates。
- 每个 delegate 独立 child session。
- 支持 group cancel。
- 返回 aggregated results。

涉及：

- `internal/agent/delegate_runner.go`
- `internal/agent/delegate_types.go`
- session child creation

验收：

- 两个 delegate 并行。
- cancel all 生效。
- result 包含 child session id、status、cost、error。

## PR-4：M1 Delegates UI + E2E

目标：

- TUI 显示 delegates compact item。
- 支持 open child transcript、cancel all、insert result。
- E2E 覆盖 M1 演示。

涉及：

- `internal/ui/model`
- `internal/ui/chat`
- workspace app/local wiring

验收：

- M1 演示脚本通过。
- flag off UI 不显示。

## PR-5：Team DB schema + sqlc

目标：

- 建 M2 第一批表。
- 建 queries。
- 建 service transaction helper。

涉及：

- `internal/db/migrations`
- `internal/db/sql/team_*.sql`
- generated sqlc
- `internal/team/models.go`

验收：

- migrations pass。
- task CAS。
- atomic claim。
- event seq unique。

## PR-6：TeamService + snapshot/API

目标：

- `team.Service`。
- local/backend/server/client workspace contract。
- TeamSnapshot/DebugSnapshot。
- `PayloadTypeTeamEvent`。

涉及：

- `internal/team/service.go`
- `internal/proto/team.go`
- `internal/backend/team.go`
- `internal/server/team.go`
- `internal/client/team.go`
- `internal/workspace/*`
- `internal/pubsub/events.go`

验收：

- local 和 client server 同功能。
- unknown event 不破坏旧 client。
- snapshot 可恢复状态。

建议拆分：

1. `team.Service` create/list/get/archive 基础事务。
2. `TeamSnapshot` / `DebugSnapshot` proto 与 serialization。
3. HTTP routes + client methods。
4. `PayloadTypeTeamEvent` + SSE unknown event compatibility。
5. snapshot replay/gap tests。

## PR-7：AgentRegistry + cost budget

目标：

- 引入 AgentRegistry。
- coder 仍保持旧行为。
- team/member/run cost budget 字段生效。

涉及：

- `internal/agent/registry.go`
- `internal/agent/coordinator.go`
- `internal/team`

验收：

- `AgentIsBusy` 不被 teammate 影响。
- budget exceeded 阻止新 run。

## PR-8：TeamRunner + MemberRunner + heartbeat

目标：

- 长期 teammate runtime。
- start/stop/cancel。
- minimal direct mailbox wake path。
- minimal prompt envelope。
- run heartbeat。
- startup recovery。

涉及：

- `internal/team/runner.go`
- `internal/team/member_runner.go`
- `internal/team/mailbox.go` 的 direct/minimal subset
- `internal/team/prompt_builder.go` 的 minimal subset
- `internal/team/recovery.go`

验收：

- M3a runtime E2E。
- shutdown/flush 测试。

建议拆分：

1. `MemberRunner` state machine and lifecycle。
2. `TeamRunner` start/stop/member registry。
3. minimal direct mailbox wake + minimal prompt envelope。
4. run heartbeat and stale recovery。
5. cancel/shutdown/flush semantics。
6. runtime E2E with one read-only member。

## PR-9：Mailbox + prompt envelope

目标：

- receipt hardening。
- broadcast/role mailbox。
- full prompt envelope builder。
- report status。

涉及：

- `internal/team/mailbox.go`
- `internal/team/prompt_builder.go`
- `internal/team/tools.go`

验收：

- message delivered/read。
- member report status。
- peer input marked untrusted。

## PR-10：PermissionBridge + audit

目标：

- team-aware permission source。
- scoped grants。
- permission pending state machine。
- audit coverage。

涉及：

- `internal/team/permission_bridge.go`
- `internal/permission`
- `internal/ui/dialog/permissions.go`
- audit queries

验收：

- M4 permission E2E。
- deny/late/orphaned state covered。

建议拆分：

1. team-aware permission request source。
2. scoped grants: call/task/session data model。
3. pending/allowed/denied/expired/canceled/orphaned state machine。
4. UI permission dialog fields。
5. audit query coverage and late response tests。

## PR-11：Shared task board hardening

目标：

- task dependencies。
- task CAS UI/debug。
- member claim/update rules。

涉及：

- `internal/team/tasks.go`
- TUI task board view

验收：

- version conflict recovery。
- blocked task visible。

## PR-12：Patch artifact write mode

目标：

- patch artifact。
- patch review/apply UI。
- base hash conflict。
- verification logs。

涉及：

- `internal/team/artifact_patch.go`
- `internal/team/apply_patch.go`
- `internal/ui/diffview`
- audit

验收：

- M5 patch E2E。
- no overwrite on mismatch。

建议拆分：

1. patch artifact schema and content storage。
2. patch review/apply service with base hash check。
3. apply conflict artifact / `team_apply_conflicts`。
4. patch review UI state and actions。
5. text add/modify/delete/rename apply support；binary artifact is review-only。
6. verification log artifact under leader-triggered strict bash policy。

## PR-13：Advanced runtime adapter

目标：

- worktree runtime。
- process backend。
- A2A gateway skeleton。

仅在 M1-M5 稳定后推进。

## 合并规则

- 每个 PR 必须可单独回滚。
- 每个 PR 必须保持 feature flag off 无变化。
- 涉及 server/client 的 PR 必须双端实现。
- 涉及权限的 PR 必须有 audit。
- 涉及 UI 的 PR 必须有 reducer/unit test 或 E2E。
