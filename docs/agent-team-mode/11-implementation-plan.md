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

- 在 `internal/config.Options` 新增 `Experimental *ExperimentalOptions`。
- 新增 `ExperimentalOptions.AgentTeamPreview=false`。
- 新增 `ExperimentalOptions.AgentTeam=false`。
- flag off 时无行为变化。
- team endpoint flag-off 返回 `feature_disabled`。
- 保持文档入口清楚。

涉及：

- `internal/config`
- config schema/tests
- docs

验收：

- config 默认 false。
- UI/API/tools/prompt 不因 flag off 改变。

## PR-0.5：SessionAgent ownership spike

目标：

- 证明 team/delegate runner 可以使用独立 `SessionAgent` 实例，而不是复用
  `Coordinator.currentAgent`。
- 实现最小 `AgentFactory.BuildTeamRunner` 和 `TeamAgentCallAdapter` spike。
- 验证 runner 的 tools/models/messageQueue/activeRequests 隔离。
- 验证 `(nil,nil)` queued case 被转成 `TurnQueued`/busy sentinel，且不会写 completed 状态。
- 验证 flush 由 message service 注入，不放进 `TurnRunner` 接口。

涉及：

- `internal/agent`
- `internal/team`
- `internal/session`
- message service wiring

验收：

- coder `SetTools` / `SetModels` 不影响 member runner。
- member turn busy 时不启动第二个 running team run。
- `SessionAgent.messageQueue` 不作为 team mailbox 或 run lifecycle 来源。
- member cancel 只影响自己的 runner。
- M0.5 不通过时，不进入 PR-8 长期 `MemberRunner` 产品化。

## PR-1：ActorContext 与 read-only policy skeleton

目标：

- 新增 `internal/actor.ActorContext`。
- 新增 M1 delegate read-only allow-list filter：`view`、`grep`、`glob`、`ls`。
- 给 tool execution、permission、log 预留字段。
- 建立 workspace-scoped path guard。
- 建立 sensitive deny-list。

涉及：

- `internal/actor`
- `internal/agent/tools`
- `internal/agent/hooked_tool.go`
- `internal/permission`
- `internal/proto/permission`

验收：

- old permission request 兼容。
- allow-list 之外的工具不进入 delegate tool schema。
- `hooked_tool.go` 仍作为执行前最终 audit/deny。
- delegate path guard 可单测。
- `07-safety-permission-audit.md` 中默认 sensitive deny-list 和绕过路径可单测。

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
- 每个 delegate 通过 `AgentFactory.BuildTeamRunner` 获得独立 `SessionAgent` runner。
- in-memory `DelegateRunGroup`，不依赖 team DB。
- 支持 group cancel。
- 返回 aggregated results。
- 沉淀 M3 可复用的 child session、actor context、tool filtering、cancel/result aggregation primitives。

涉及：

- `internal/team/delegate_runner.go`
- `internal/team/delegate_types.go`
- session child creation

验收：

- 两个 delegate 并行。
- cancel all 生效。
- delegate 不复用 coder runner；coder tools/models 变化不影响 delegate。
- result 包含 child session id、status、cost、error。
- 不写 `team_runs` / `team_events`，不要求 AgentRegistry 持久状态。
- PR-3 输出的 primitives 在 PR-8 `MemberRunner` 设计中有明确复用点。

## PR-4：M1 Delegates UI + E2E

目标：

- TUI 显示 delegates compact item。
- 支持 initializing/running/partial/aggregating/ready/inserted/canceled/failed lifecycle。
- 支持 read-only open child transcript、cancel all、insert result。
- E2E 覆盖 M1 演示。

涉及：

- `internal/ui/model`
- `internal/ui/chat`
- workspace app/local wiring

验收：

- M1 演示脚本通过。
- partial result、aggregating 和 inserted 状态可测。
- child transcript modal 只读，关闭后恢复原焦点。
- flag off UI 不显示。

## PR-5：Team DB schema + sqlc

目标：

- 建 M2 core tables。
- 建 queries。
- 建 M2 store interfaces 与 sqlc-backed implementation。
- 建 service transaction helper，但不暴露给 server/client/UI。

涉及：

- `internal/db/migrations`
- `internal/db/sql/team_*.sql`
- generated sqlc
- `internal/team/models.go`
- `internal/team/store_*.go`

验收：

- migrations pass。
- task CAS。
- atomic claim。
- event seq unique。
- M2 只创建 `teams`、`team_members`、`team_tasks`、`team_runs`、`team_events`、
  `team_event_counters`、`team_audit_events`。
- 不创建 `team_session_links`、mailbox/artifact/dependency 表或 `team_idempotency_keys`。
- 只实现 M2 stores：`TeamStore`、`MemberStore`、`TaskStore`、`RunStore`、`EventStore`、
  `AuditStore`。
- store 只接受 `tx`/query executor，不自己开启跨表 transaction。
- server/client/UI 不 import store interfaces。

## PR-6：TeamService + snapshot/API

目标：

- `team.Service` use-case facade。
- local/backend/server/client workspace contract。
- TeamSnapshot/DebugSnapshot。
- `PayloadTypeTeamEvent`。
- `proto.TeamEvent` 和 `teamEventToProto`。
- `TeamWorkspace` 独立接口。
- M2.5 server/client write API idempotency hardening。

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
- `PayloadTypeTeamEvent` 的 server wrap/client decode 均覆盖测试。
- M2 service/API 可以先不承诺可重试写入；一旦 server/client write API 标记为 retryable，
  必须进入 M2.5 hardening 并使用 `team_idempotency_keys`，相同 key/request 返回原结果。
- snapshot 可恢复状态。
- `team.Service` 只编排 M2 core domain use cases；mailbox/permission/patch 等未到阶段 use case
  返回 `feature_disabled` / `not_implemented`。
- domain row、audit、event 由 Service 在同一 transaction 内编排。
- API route/client/workspace 只依赖 `team.Service` / `TeamWorkspace`，不直接调用 store。

建议拆分：

1. `team.Service` create/list/get/archive 基础事务。
2. `TeamSnapshot` / `DebugSnapshot` proto 与 serialization。
3. HTTP routes + client methods。
4. M2.5 `team_idempotency_keys` + retryable write API contract。
5. `PayloadTypeTeamEvent` + `teamEventToProto` + SSE unknown event compatibility。
6. snapshot replay/gap tests。

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
- 每个 member 独立 `SessionAgent` runner。
- start/stop/cancel。
- minimal direct mailbox wake path。
- minimal prompt envelope。
- run heartbeat。
- startup recovery。
- fixed shutdown/cancel/flush sequence。
- compact team item 基础状态绑定。
- `team_session_links` for member session visibility/recovery。

涉及：

- `internal/team/runner.go`
- `internal/team/member_runner.go`
- `internal/team/mailbox.go` 的 direct/minimal subset
- `internal/team/prompt_builder.go` 的 minimal subset
- `internal/team/recovery.go`

验收：

- M3a runtime E2E。
- shutdown/flush 测试。
- shutdown 顺序固定：stop wakeups -> event/audit -> cancel current turn -> wait timeout -> message service flush
  -> terminal run/member state -> publish event。
- `CancelMemberTurn` 顺序固定：member -> `canceling_turn` + event/audit -> member runner
  `Cancel(session_id)` -> wait/flush -> run `canceled|interrupted` -> member terminal state -> publish。
- duplicate cancel 幂等；late Run result 不能覆盖 `canceled` / `interrupted`。
- `MemberRunner` single-flight gate 可测；busy 时 wakeup 留在 mailbox/local queue，不依赖
  `SessionAgent.messageQueue`。
- `TurnQueued` 是 non-terminal，不写 completed run。
- app startup stale recovery：heartbeat 过期 run 标记 `interrupted`，member 恢复 idle/blocked。
- M3 不消费 permission wait/approval；permission control message 只保留 schema。
- partial cost accounting 覆盖 final/partial/unknown。
- compact item 显示 running/idle/blocked/stale。
- member session link / visibility / recovery path 可测。

建议拆分：

1. `MemberRunner` state machine and lifecycle。
2. `TeamRunner` start/stop/member registry。
3. minimal direct mailbox wake + minimal prompt envelope。
4. run heartbeat and stale recovery。
5. cancel/shutdown/flush semantics + partial cost accounting。
6. runtime E2E with one read-only member。

## PR-9：Mailbox + prompt envelope

目标：

- 新增 M3 durable stores。
- receipt hardening。
- broadcast/role mailbox。
- full prompt envelope builder。
- report status。
- expanded team item internal viewport、activity/mailbox summary。
- task dependencies and artifacts table consumption。

涉及：

- `internal/team/mailbox.go`
- `internal/team/prompt_builder.go`
- `internal/team/tools.go`
- `internal/team/store_mailbox.go`
- `internal/team/store_artifact.go`
- `internal/team/store_session_link.go`

验收：

- message delivered/read。
- member report status。
- peer input marked untrusted。
- prompt envelope section order 可单测：task full text 不截断，direct before broadcast，tool policy
  before mailbox。
- 24 行终端下 expanded team item 不遮挡 chat timeline。
- 新增并使用 `MailboxStore`、`ReceiptStore`、`DependencyStore`、`ArtifactStore`、
  `SessionLinkStore`。
- store 仍不暴露给 API/UI；跨表事务仍由 `team.Service`/TeamRunner use case 编排。

## PR-9b：Change proposal preview

目标：

- 新增 `change_proposal` artifact kind。
- teammate 在 read-only runtime 下生成实现提案。
- proposal review UI。
- request revision / discard / accept for patch 状态流。
- 明确 no-write / no-apply guard。
- 复用 M3 `ArtifactStore` 的 proposal subset，或新增独立 `ProposalStore`。

涉及：

- `internal/team/artifacts.go`
- `internal/team/tools.go`
- `internal/ui/chat`
- `internal/ui/dialog`

验收：

- M3.5 change proposal E2E。
- proposal 包含 target files、轻量结构化 proposed hunks、pseudo diff、risk、verification suggestions。
- hard validation 只覆盖 JSON shape、size、status/intent enum 和 workspace-relative path；不校验
  line/anchor/pseudo diff 精确性。
- accept for patch 只更新 proposal 状态，不调用 patch apply。
- no workspace file is written。
- proposal store/subset 不依赖 M5 `PatchStore` / `ContentStore`。

## PR-10：PermissionBridge + audit

目标：

- team-aware permission wrapper over existing `permission.Service`。
- 新增 M4 `PermissionStore` / `GrantStore`。
- scoped grants。
- permission pending state machine。
- permission queue / timeout UI。
- audit coverage。

涉及：

- `internal/team/permission_bridge.go`
- `internal/permission`
- `internal/ui/dialog/permissions.go`
- audit queries

验收：

- M4 permission E2E。
- non-team permission behavior unchanged。
- bridge does not bypass `hooked_tool.go` PreToolUse deny/allow semantics。
- deny/late/orphaned state covered。
- 多 pending request 队列、expired actions disabled、late response UI 均覆盖。
- permission request、resolve、grant lookup 通过 M4 stores 实现；M3 mailbox control message
  只作为输入/唤醒路径，不承担授权事实源。
- grant 创建、late response、orphaned resolution 必须在 Service/PermissionBridge 事务中写 audit。
- `allow task` 只创建 team-scoped grant；不得调用现有 `GrantPersistent` 形成缺少 team/task
  constraints 的 session-wide grant。

建议拆分：

1. team-aware permission service wrapper and ActorContext extraction。
2. scoped grants: call/task/session data model。
3. pending/allowed/denied/expired/canceled/orphaned state machine。
4. existing permission dialog extension + queue/timeout behavior。
5. audit query coverage, hook allow/deny audit, and late response tests。

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
- 新增 M5 `PatchStore` / `ContentStore` / `ApplyConflictStore`。
- content store with hash verification and workspace-scoped access。
- app data filesystem blob store first implementation。
- patch review/apply UI with unified diff viewer。
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
- file/hunk navigation、大 diff summary mode、binary review-only 均覆盖。
- `change_proposal` 不能直接升级为 patch row；M5 必须重新生成 `patch` artifact。
- `ContentStore` 使用 opaque ref + workspace ownership + hash verification，不接受路径型 ref。
- filesystem backing path 不出现在 API/DB/UI；store root 不在 editable workspace path。
- apply conflict 写 `ApplyConflictStore` / conflict artifact；任一 base mismatch 整体 no-write。
- apply service 必须实现 pre-write recheck、per-path serialize guard、temp file + atomic rename。
- 多文件 apply 失败必须尝试 rollback；rollback 失败写 partial apply conflict 和 audit。

建议拆分：

1. patch artifact schema and filesystem content storage。
2. patch review/apply service with base hash check, pre-write recheck, and per-path serialize guard。
3. apply conflict artifact / `team_apply_conflicts`。
4. patch review UI state and actions。
5. temp file + atomic rename write path and multi-file rollback/partial conflict handling。
6. text add/modify/delete/rename apply support；binary artifact is review-only。
7. retention/orphan cleanup hooks with audit/debug on failure。
8. verification log artifact under leader-triggered strict bash policy。

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
