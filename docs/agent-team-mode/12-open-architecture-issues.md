# 12 架构决策与待冻结问题

本文件记录已经冻结的实现决策，以及后续里程碑前仍需冻结的问题。M0/M1 前的条目已经
作为正式决策；后续条目在对应里程碑开工前按同一格式补齐。

## 决策记录格式

```text
Decision:
Date:
Reason:
Impacted milestone:
Files/modules:
Rollback strategy:
```

## M0 冻结决策

### 1. Event 表策略

```text
Decision: 只建 `team_events`，一表承担 domain event 与 outbox 语义。
Date: 2026-06-01
Reason: 减少 M2 表复杂度；snapshot/replay 只依赖一个 per-team seq；未来可在不改上层 contract 的情况下拆出 worker。
Impacted milestone: M2
Files/modules: internal/db/migrations, internal/db/sql, internal/team, internal/pubsub
Rollback strategy: 保持 TeamEvent contract 不变，后续新增 `team_event_outbox` 并从 `team_events` backfill。
```

### 2. 命名

```text
Decision: DB/API/internal/runtime 统一使用 `member`；UI/prompt 文案使用 `teammate`，必要时可用 `mate` 作为口语简称。
Date: 2026-06-01
Reason: 避免 internal 出现 member/mate 双口径；`team_members` 与 DB/API 命名一致，用户界面仍保留 teammate 的自然表达。
Impacted milestone: M0-M6
Files/modules: internal/team, internal/proto, internal/ui, docs/agent-team-mode
Rollback strategy: 如果产品文案需要调整，只改 UI/prompt copy，不改 DB/API/internal type。
```

### 3. Audit 表是否 M2 创建

```text
Decision: M2 创建 `team_audit_events`；M2 只记录 domain-level audit；M4 起强制覆盖 permission/tool/hook。
Date: 2026-06-01
Reason: 审计表晚建会导致 M2/M3 历史无法追溯；但 M2 不强制完成所有 tool-level audit，避免阻塞 domain 落地。
Impacted milestone: M2, M4
Files/modules: internal/db/migrations, internal/team, internal/permission
Rollback strategy: 表可保留空行低成本存在；如果 M2 不接 UI 查询，不影响用户路径。
```

### 3b. M2 DB 范围

```text
Decision: M2 只创建 core durable domain tables：`teams`、`team_members`、`team_tasks`、`team_runs`、`team_events`、`team_event_counters`、`team_audit_events`。
Date: 2026-06-02
Reason: M2 的目标是验证 snapshot/event/API 事实源；mailbox、artifact、session visibility 和 idempotency hardening 还没有真实消费者，提前建会扩大 migration 和 sqlc 负担。
Impacted milestone: M2-M3
Files/modules: internal/db/migrations, internal/db/sql, internal/team
Rollback strategy: M3 创建 `team_session_links`、mailbox、receipt、dependency、artifact 表；PR-6 API hardening 创建 `team_idempotency_keys`。
```

### 3c. Service / Store 边界

```text
Decision: 对外只暴露 `team.Service` use-case facade；内部按里程碑实现 store。M2 只实现 `TeamStore`、`MemberStore`、`TaskStore`、`RunStore`、`EventStore`、`AuditStore`；M3/M3.5/M4/M5 再分别追加 mailbox/proposal/permission/patch 相关 store。
Date: 2026-06-02
Reason: 30+ method 的大 Service interface 会把未来阶段的职责提前压进 M2，造成未消费 schema、空实现和 server/client/UI 误依赖。facade 保持业务用例边界，store 保持 SQL/query 边界，可以让每个里程碑只交付真实可验收能力。
Impacted milestone: M2-M5
Files/modules: internal/team, internal/db/sql, internal/backend, internal/server, internal/client, internal/workspace, internal/ui
Rollback strategy: 如果某个 store 颗粒度过细，可以在 `internal/team` 内合并 store implementation；对外 `team.Service` / `TeamWorkspace` contract 不回退成 DB dump。
```

约束：

- server/client/UI 不能 import store interface，只能依赖 `team.Service` / `TeamWorkspace`。
- store 只接受 `tx` 或 query executor，不自己开启跨表 transaction。
- `team.Service` method 负责 `domain row + audit + event` 的事务编排。
- 未到阶段的 use case 返回 `feature_disabled` / `not_implemented`，不能创建部分状态。

### 4. `team_task_get` 是否进入 MVP

```text
Decision: `team_task_get` 进入 M2 MVP。
Date: 2026-06-01
Reason: CAS 冲突恢复需要稳定读取当前 task；prompt 和 UI/debug 都可复用同一接口。
Impacted milestone: M2-M4
Files/modules: internal/team, internal/server, internal/client, internal/ui
Rollback strategy: 如果 M2 API 面过大，可保留 service/query，延后 leader tool 暴露。
```

### 5. `team_send_message` recipient

```text
Decision: `recipient_type = direct | broadcast | role`，每个 recipient 都写独立 receipt。
Date: 2026-06-01
Reason: direct 覆盖点对点，broadcast 覆盖 team announce，role 覆盖按职责分发；receipt 表支持 delivered/read 独立追踪。
Impacted milestone: M3
Files/modules: internal/team/mailbox.go, internal/db/sql, internal/team/tools.go
Rollback strategy: M3 可以只启用 direct，broadcast/role schema 保留但 tool policy 暂不暴露。
```

### 6. DebugSnapshot 最小 schema

```text
Decision: DebugSnapshot 最小包含 team、members、tasks、runs、mailbox_depth、pending_permissions、recent_events、artifacts、cost、heartbeat_age、queue_depth、last_seq。
Date: 2026-06-01
Reason: TUI reconnect、seq gap repair、debug modal 和 server/client parity 都需要同一恢复源。
Impacted milestone: M2-M5
Files/modules: internal/team, internal/proto, internal/server, internal/client, internal/ui
Rollback strategy: 字段可追加；已声明字段不能删除，只能允许为空或 unknown。
```

### 7. agent/team package 依赖方向

```text
Decision: `internal/agent` 只暴露接口；`internal/team` 依赖接口；`internal/app` 或 workspace wiring 注入具体实现。
Date: 2026-06-01
Reason: 保持默认 coder 与 team runtime 解耦，避免 Coordinator 变成多 runtime god object。
Impacted milestone: M0.5, M3
Files/modules: internal/agent, internal/team, internal/app, internal/workspace
Rollback strategy: 如果 spike 发现接口不足，扩展接口；禁止让 TeamRunner 访问 coordinator private fields。
```

### 7b. SessionAgent 实例所有权与 queued 语义

```text
Decision: M1 delegate 和 M3 member runner 都必须持有独立 `SessionAgent` 实例，不复用 `Coordinator.currentAgent`。Team runtime 不依赖 `SessionAgent.messageQueue` 作为 mailbox 或 run lifecycle 来源；`SessionAgent.Run` 的 `(nil,nil)` queued case 必须转成 non-terminal `TurnQueued`/busy sentinel。
Date: 2026-06-02
Reason: 现有 `SessionAgent` 的 tools、models、messageQueue、activeRequests 是实例字段，不是按 session 隔离的共享资源；共享 coder runner 会导致工具/模型策略、队列和 cancel 状态互相污染。当前 `Run` 入队后没有 queued-start callback，team runtime 无法可靠追踪被内部递归执行的 turn。
Impacted milestone: M0.5, M1, M3
Files/modules: internal/agent, internal/team, internal/session, internal/app, internal/workspace
Rollback strategy: 如果 M0.5 证明无法安全复用现有 `SessionAgent.Run`，先新增低层 turn runner 或 queued-start lifecycle callback；不得退回到共享 `Coordinator.currentAgent`。
```

约束：

- `AgentFactory.BuildTeamRunner` 创建新的 runner 实例，并按 actor/tool/instructions policy 固化配置。
- coder 的 `SetTools` / `SetModels` 不影响 delegate/member runner。
- `MemberRunner` 默认 single-flight；busy 时新 wakeup 留在 mailbox/local queue。
- `TurnQueued` 是 non-terminal，不写 completed run/task state。
- flush 通过 message service 注入，不放进 `TurnRunner` 基础接口。

### 8. Feature flag 形状

```text
Decision: 在 `internal/config.Options` 新增 `Experimental *ExperimentalOptions`，字段为 `AgentTeamPreview` 和 `AgentTeam`，默认 nil/false。
Date: 2026-06-02
Reason: 代码库当前没有 feature flag 基础设施；team mode 必须由单一配置源 gate，避免 UI/API/tools/prompt 各自解析。
Impacted milestone: M0-M6
Files/modules: internal/config, internal/app, internal/server, internal/client, internal/ui, internal/team
Rollback strategy: 删除或置 false 即关闭入口；flag-off endpoint 返回 `feature_disabled`，不写 DB、不发 SSE。
```

### 9. TeamAgentCall adapter

```text
Decision: team runtime 使用 `TeamAgentCall` 调用描述符，并通过 `TeamAgentCallAdapter` 映射到现有 `internal/agent.SessionAgentCall`；不重定义、不扩展同名结构体。
Date: 2026-06-02
Reason: 现有 `SessionAgentCall` 已由 `SessionAgent.Run` 直接消费，字段与 team runtime 需求不兼容；直接复用名称会导致实现错误。
Impacted milestone: M0.5, M3
Files/modules: internal/agent, internal/team
Rollback strategy: spike 如证明字段不足，只扩展 `TeamAgentCall` 和 adapter，不破坏现有 `SessionAgentCall`。
```

### 10. ActorContext 包归属

```text
Decision: `ActorContext` 放在 `internal/actor`，由 tools、hooks、permission、team runtime 共享。
Date: 2026-06-02
Reason: actor identity 同时被 permission、hooks、tools、audit 使用，不能放在 `internal/team` 导致底层工具反向依赖 team domain。
Impacted milestone: M1-M5
Files/modules: internal/actor, internal/agent/tools, internal/hooks, internal/permission, internal/team
Rollback strategy: 如果后续发现字段过宽，拆成核心 `actor.Context` + team 扩展 payload，但包归属不回退到 `internal/team`。
```

### 11. TeamWorkspace 接口

```text
Decision: team API 使用独立 `TeamWorkspace` 接口；不直接把 team methods 追加到现有 `workspace.Workspace` 大接口。
Date: 2026-06-02
Reason: 现有 Workspace 已包含 session/agent/permission/MCP/config 等职责；独立接口能让 M2 server/client parity 更清晰，也减少 flag-off 变更面。
Impacted milestone: M2-M5
Files/modules: internal/workspace, internal/backend, internal/server, internal/client, internal/ui
Rollback strategy: 如果 UI wiring 需要统一入口，可在组合结构中嵌入 `Workspace` + `TeamWorkspace`，不改变接口职责。
```

## M1 冻结决策

### 0. M1 scope：铺路但不 durable

```text
Decision: M1 保留 read-only delegate 作为后续 team mode 的底座预演，但 `DelegateRunGroup` 只用内存状态，不写 durable team tables，不依赖 AgentRegistry 持久状态。
Date: 2026-06-02
Reason: M1 的价值不是替代现有 `/agent`，而是提前压实 child session、actor identity、tool filtering、cancel/result aggregation、child transcript 和 flag-off 行为；但 durable team domain 应留到 M2，长期 teammate runtime 留到 M3。
Impacted milestone: M1-M3
Files/modules: internal/team/delegate_runner.go, internal/team/delegate_types.go, internal/actor, internal/agent/tools, internal/ui/chat
Rollback strategy: 如果 M1 demo 价值不足，可关闭 preview flag；已沉淀的 actor/path/tool/cancel primitives 仍可供 M3 复用。
```

### 1. Delegate tool allow-list

```text
Decision: M1 delegate 默认只允许 view、grep、glob、ls。
Date: 2026-06-01
Reason: M1 目标是只读调研演示，工具面越窄越容易证明安全边界。
Impacted milestone: M1
Files/modules: internal/agent/tools, internal/team/delegate_runner.go
Rollback strategy: 通过 allow-list 配置追加只读工具，不改变默认 deny 策略。
```

### 2. Sensitive deny-list

```text
Decision: M1 默认拒绝 `.env`、`.env.*`、`*.pem`、`*.key`、`*.p8`、`*.p12`、`*.pfx`、`*.jks`、`credentials.*`、`secrets.*`、SSH key、`.kube/config`、`.npmrc`、`.netrc`、`pip.conf`、cloud credential 常见路径，并允许项目配置追加。
Date: 2026-06-01
Reason: delegate 虽然只读，也不能读取高敏凭据；默认 deny-list 是 M1 演示可接受的最低安全线。
Impacted milestone: M1
Files/modules: internal/agent/tools, internal/team, internal/config
Rollback strategy: 如果误伤项目文件，通过项目级 allow exception 配置处理，但必须写 audit/debug reason。
```

路径匹配必须发生在 canonical absolute path 上，并在文件系统访问层执行；symlink、Windows
junction/reparse point、percent-encoded path、Unicode normalization、null byte/control char
和大小写变体都进入 M1 safety tests。

### 3. MCP instructions policy

```text
Decision: M1 delegate 默认不注入 user/global MCP instructions，不暴露 MCP tools；project MCP 必须显式 `team_visible` 才可进入后续阶段 allow-list。
Date: 2026-06-01
Reason: MCP instruction 和 tool 可能扩大模型权限面，M1 要先证明本地 read-only delegate 安全。
Impacted milestone: M1-M2
Files/modules: internal/agent/tools/mcp, tool build options, skills loading/filtering
Rollback strategy: 后续通过 actor-aware policy 精确开放，不回溯修改 M1 默认策略。
```

## M3 冻结决策

### 1. MemberRunner lifecycle

```text
Decision: `MemberRunner` 是长期 runtime object，但不是常驻 LLM 调用。它 idle 等待 mailbox、task、explicit wakeup 或 recovery wake；每次 wake 只启动一轮 `team_run`，并通过 `TeamAgentCallAdapter` 调用现有 `SessionAgent.Run`。
Date: 2026-06-02
Reason: team mode 需要长期 teammate 身份和可恢复 session，但不能让每个 teammate 常驻模型调用或绕开现有 session agent 执行链。
Impacted milestone: M3
Files/modules: internal/team/member_runner.go, internal/team/runner.go, internal/team/recovery.go, internal/agent, internal/session
Rollback strategy: 如果 M3 runtime 不稳定，可关闭 `AgentTeam` flag；M1 delegate primitives 仍可保留。
```

约束：

- 每个 member 默认拥有一个 child session。
- 每个 member 拥有独立 `SessionAgent` runner，不复用 coder runner。
- `max_concurrent_runs_per_member=1`。
- 每轮 run 写 `team_runs`，并按 30-60 秒间隔 heartbeat；不随 token delta 写 DB。
- `MemberRunner` 不直接调用 `Run(sessionID, prompt)`，只能走 `TeamAgentCall` adapter。
- `MemberRunner` 不依赖 `SessionAgent.messageQueue`；queued/busy 是 non-terminal runtime signal。
- M3 不消费 permission wait/approval；permission control message 只保留 schema。

### 2. Shutdown 与 recovery 顺序

```text
Decision: Member shutdown 顺序固定为 stop accepting wakeups -> append shutdown event/audit -> cancel current turn if needed -> wait with timeout -> message service flush -> mark run canceled/interrupted if needed -> mark member stopped -> publish event。Startup recovery 标记 stale run 为 `interrupted`，再根据 task retry policy 恢复 idle/blocked/requeue。
Date: 2026-06-02
Reason: 只 cancel goroutine 会丢失 durable run/member 状态；长期 teammate 必须能在 app shutdown、crash、重启后被解释和恢复。
Impacted milestone: M3
Files/modules: internal/team/member_runner.go, internal/team/recovery.go, internal/team/service.go, internal/db/sql, internal/pubsub
Rollback strategy: 如果 graceful shutdown 超时频繁，可调 timeout 或退化到 force path，但仍必须写 event/audit 和 run/member terminal state。
```

恢复规则：

- heartbeat 过期的 `running` run 标记 `interrupted`。
- `waiting_permission` 在 M3 不恢复等待；标记 `interrupted` 或 `blocked`，M4 才支持 permission resume。
- active member 没有 active run 时恢复为 `idle`；不可重试 task 使 member 进入 `blocked`。
- 未读 direct mailbox、queued/assigned task、dependency completion 重新触发 wakeup。
- recovery 不重放普通 assistant output，只依据 durable team state。

### 2b. Team cancel contract

```text
Decision: team cancel 由 `TeamRunner.CancelMemberTurn(ctx, req)` 编排；`TurnRunner.Cancel(sessionID)` 只是对独立 member runner 发送 cancel signal 的 primitive。CancelMemberTurn 必须先写 member `canceling_turn`、cancel requested event 和 audit，再调用对应 member runner 的 `Cancel(sessionID)`，随后等待/flush 并写 `team_runs` terminal state。
Date: 2026-06-02
Reason: 现有 `Coordinator.Cancel(sessionID)` 只转发到默认 coder 的 currentAgent，且现有 `SessionAgent.Cancel` 只取消 context/queue，不写 team run、member state、audit/event。长期 teammate 的 cancel 必须可恢复、可审计、可在 UI/SSE 中解释。
Impacted milestone: M3-M4
Files/modules: internal/team/runner.go, internal/team/member_runner.go, internal/team/service.go, internal/agent, internal/db/sql, internal/pubsub, internal/ui
Rollback strategy: 如果 cancel race 太复杂，M3 可先只支持 cancel current turn；stop member/team 仍走 shutdown 顺序。不得退回到直接调用 `Coordinator.Cancel(sessionID)`。
```

约束：

- 无 active run 时 cancel 为幂等 no-op，可写 audit/debug，但不写 terminal run event。
- cancel current turn 不清 mailbox/task queue。
- repeated cancel 不重复写 terminal event。
- cancel 成功后 run -> `canceled`；timeout、runner lost、crash recovery 后 run -> `interrupted`。
- 如果 run 在 cancel 请求前后已经完成，completed 结果可保留，但必须写 `cancel_late` audit。
- late Run result 不能覆盖已经写入的 `canceled` / `interrupted` terminal state。
- M4 起 cancel 必须把该 run 的 pending permission requests 转为 `canceled`。

### 3. Prompt envelope 优先级

```text
Decision: M3 prompt envelope 必须按固定 section order 构建：system/developer/tool policy -> member identity -> current task full text -> direct unread messages -> dependency result summaries -> leader latest instruction -> broadcast/role messages -> own session summary -> reporting rules。task description 不可截断；peer mailbox 永远是 untrusted input。
Date: 2026-06-02
Reason: teammate 之间通过 mailbox 协作，如果 prompt envelope 没有固定优先级和 untrusted 边界，peer input 可能覆盖任务、安全和工具策略。
Impacted milestone: M3
Files/modules: internal/team/prompt_builder.go, internal/team/mailbox.go, internal/team/member_runner.go
Rollback strategy: 如果上下文预算不足，截断低优先级 section 或拒绝启动本轮；不能截断 task/system/tool policy。
```

约束：

- direct messages 优先于 broadcast/role messages。
- mailbox、dependency summaries、peer status 都标记 untrusted。
- 被截断时插入 `[truncated: N lower-priority messages omitted]`。
- envelope builder 必须可单测 section order。

### 4. Partial cost accounting

```text
Decision: finished run 使用 final usage；canceled/interrupted 且 provider 返回 usage 时记录 partial usage；canceled/interrupted 且无 provider usage 时记录 usage unknown。UI 显示 unknown，不能假装为 0。
Date: 2026-06-02
Reason: 长期 teammate 可能被 cancel、shutdown 或 crash 打断；错误地把未知成本当 0 会误导 budget 和用户判断。
Impacted milestone: M3
Files/modules: internal/team/member_runner.go, internal/team/service.go, internal/ui, internal/db/sql
Rollback strategy: 如果 provider usage 不稳定，先统一记录 unknown；不要写 0 作为占位。
```

### 5. Mailbox control message 边界

```text
Decision: M3 支持 `permission_request` / `permission_response` schema 预留，但不消费 permission wait/approval；实际状态机和 runner handling 放 M4。M3 只消费 message、task_assignment、task_status、shutdown_request、shutdown_ack。
Date: 2026-06-02
Reason: M3 目标是验证长期 teammate runtime 和 mailbox loop；提前消费 permission response 会绕过尚未冻结的 PermissionBridge。
Impacted milestone: M3-M4
Files/modules: internal/team/mailbox.go, internal/team/member_runner.go, internal/team/permission_bridge.go
Rollback strategy: M4 启用 permission handling 时复用 schema，不回改 M3 message contract。
```

## M3.5 冻结决策

### 1. Change proposal 不是 patch

```text
Decision: M3.5 新增 `change_proposal` artifact，用于展示 teammate 的实现提案；它不能 apply，不能写 workspace，不能进入 M5 patch apply service。
Date: 2026-06-02
Reason: 需要在 M5 前验证长期 teammate 的实现型协作价值和 review UI，但不能提前绕过 M4 permission/audit 与 M5 content store/apply conflict 安全边界。
Impacted milestone: M3.5, M5
Files/modules: internal/team/artifacts.go, internal/team/tools.go, internal/ui/chat, internal/ui/dialog
Rollback strategy: 如果 proposal 价值不足，可关闭 M3.5 UI，不影响 M4 permission 或 M5 patch artifact contract。
```

字段要求：

```text
target_files
proposed_hunks         lightweight: file, intent, anchor, line_hint, before_summary, after_summary, pseudo_diff
risk_summary
verification_suggestions
proposal_status        pending_review | revise_requested | discarded | accepted_for_patch
generated_by_run_id
```

`proposed_hunks` 做轻量结构化，不做严格 patch contract。硬校验只覆盖 JSON shape、size、
status/intent enum、workspace-relative path 和 path traversal；`line_hint`、`anchor`、`pseudo_diff`
都是 review hint，不要求命中当前文件，也不要求符合 unified diff。

`accepted_for_patch` 只表示 leader 接受该提案作为后续 patch 输入；正式 patch 必须在 M5 重新生成
`patch` artifact，并经过 content store、hash、base conflict 和 review/apply 流程。

## M4 前仍需冻结

### 0. PermissionBridge 集成方式

```text
Decision: M4 `PermissionBridge` 作为现有 `permission.Service` / PreToolUse hook flow 的 team-aware wrapper；不新建一套平行 permission subsystem。team 表记录 team/member/task/run 事实源、scoped grant、late response、orphaned recovery 和 audit，工具执行仍通过现有 permission/hook 等待与 resolve 链路。
Date: 2026-06-02
Reason: 当前工具直接依赖 `permission.Service.Request`，UI 已有 `internal/ui/dialog/permissions.go`，`hooked_tool.go` 已负责 PreToolUse allow/deny。若 team mode 另建一套审批系统，会造成工具执行链、UI 行为、hook 语义和审计分叉。
Impacted milestone: M4
Files/modules: internal/team/permission_bridge.go, internal/permission, internal/agent/hooked_tool.go, internal/ui/dialog/permissions.go, internal/db/sql
Rollback strategy: M4 可关闭 team write/permission flag；非 team session 继续使用原 permission service，不依赖 team 表。
```

约束：

- 非 team session 行为完全不变，直接 delegated 到现有 permission service。
- `hooked_tool.go` 仍是执行前 hook gate；bridge 不绕过 hook deny，也必须审计 hook allow。
- `allow once` 只 resolve 当前 tool call。
- `allow for task` 创建 team-scoped grant，并 resolve 当前 tool call；不得调用现有 `GrantPersistent`
  生成缺少 team/task constraints 的 session-wide grant。
- `allow for session` 不在默认 UI 展示；如果作为高级项出现，也必须通过 team grant 表约束。
- UI 第一版扩展现有 permission dialog，不做第二套弹窗。

### 1. Permission pending 状态机

必须支持：

```text
pending
allowed
denied
expired
canceled
orphaned
```

### 2. Late response

```text
Decision: 如果 run/tool_call 已结束，late response 不生效，只更新 team_permission_requests 决策状态并写 audit，不创建 grant。
Date: 2026-06-01
Reason: late response 不能授权新的 run/tool_call，否则权限会跨执行边界泄露。
Impacted milestone: M4
Files/modules: internal/team/permission_bridge.go, internal/db/sql, internal/permission
Rollback strategy: 如果 UI 需要显示 late response，读取 audit/request 状态，不改变 grant 规则。
```

### 3. Scope UI

默认 UI：

```text
allow once
allow for task
deny
```

`allow for session` 需要二级确认或 hidden advanced。

### 4. Tool permission matrix

初始：

| Tool | Leader | Member |
| --- | --- | --- |
| `team_create` | yes | no |
| `team_spawn_member` | yes | no |
| `team_send_message` | yes | yes |
| `team_task_create` | yes | no before M4 |
| `team_task_get` | yes | yes |
| `team_task_list` | yes | yes |
| `team_task_update` | yes | own task only |
| `team_task_claim` | yes | yes |
| `team_report_status` | yes | yes |
| `team_shutdown_member` | yes | no |

## M5 冻结决策

### 1. Patch artifact schema

```text
Decision: patch artifact 使用 content store 引用，不把完整 patch text 写入 DB；metadata 必须包含 base_hash、base_ref、touched_files、change_kinds、generated_by_run_id、apply_status、verification_log_artifact_id。
Date: 2026-06-01
Reason: patch 内容可能较大，DB 只保留索引、hash 和状态；content store 便于 diff viewer、conflict artifact 和审计关联。
Impacted milestone: M5
Files/modules: internal/team/artifact_patch.go, internal/team/apply_patch.go, internal/ui/diffview
Rollback strategy: 如果 content store 不稳定，M5 可限制 patch size 并临时内联小 patch，但 API contract 保持 content_ref。
```

字段要求：

```text
base_ref              commit hash or workspace state hash
base_hash             hash of touched file set at generation time
touched_files         path, old_hash, new_hash, change_kind
change_kinds          add | modify | delete | rename | binary
patch_content_ref
patch_content_hash
generated_by_run_id
apply_status          pending | applied | rejected | conflict | failed
apply_result
verification_log_artifact_id
```

M5 第一版支持 text add/modify/delete/rename；binary change 只允许生成 artifact，不允许 apply。

### 2. Content store 访问控制、完整性与落点

```text
Decision: M5 第一版 `ContentStore` 使用 app data 下的 workspace-scoped filesystem blob store。`content_ref` 必须由 store 生成且保持 opaque；读取和 apply 前强制验证 `content_hash`；member 不能构造路径型 ref。接口保持可替换，后续可演进到 DB blob、CAS/object store、remote backend 或加密 store。
Date: 2026-06-02
Reason: M5 需要一个实现成本低、可审计、可做 hash verification 的 durable content backend；filesystem blob store 足够支撑本地 patch review/apply。opaque `ContentStore` API 能避免把第一版落点绑定到上层 contract。
Impacted milestone: M5
Files/modules: internal/team/artifact_patch.go, internal/team/content_store.go, internal/team/apply_patch.go, internal/ui/diffview
Rollback strategy: 如果 filesystem store 不稳定，M5 只能启用小 patch inline fallback；仍必须保持 opaque ref/hash verification API，不允许直接文件路径 ref。后续替换 backend 不改 DB/API/UI contract。
```

要求：

- `ContentStore.Put/Get/Verify/Delete` 均接收 `workspace_id` 并校验 artifact row ownership。
- `content_ref` 是 opaque store id，不接受绝对路径、相对路径、`file://` 或用户提供 URI。
- `Get` 和 apply 前重新计算 hash；不匹配返回 `artifact_integrity_failed` 并写 audit。
- store root 放在受控 app data 目录，不放在普通 editable workspace path 中；backing filesystem path
  不能出现在 API、DB `content_ref`、UI 或 member-visible payload 中。
- team archive/delete 执行 retention cleanup；失败写 audit/debug。

后续改进必须保留上层 contract：

- M5.1 orphan blob sweep：清理没有 `team_artifacts` 引用的 blob，失败写 audit/debug。
- M5.2 quota/retention：workspace/team blob 总量、单 artifact size limit、保留周期。
- M6+ backend adapter：DB blob、content-addressed object store、本地/远端混合 store。
- encryption at rest：在 ContentStore implementation 内完成，不改变 `content_ref` 语义。

### 3. Apply conflict 策略

```text
Decision: apply 必须先验证 base_hash；base mismatch 不写文件，生成 conflict artifact 和 `team_apply_conflicts` row。
Date: 2026-06-01
Reason: teammate patch 不能覆盖用户或 leader 的并发改动；冲突必须显式暴露给 leader。
Impacted milestone: M5
Files/modules: internal/team/apply_patch.go, internal/team/artifact_patch.go, internal/ui/diffview
Rollback strategy: 如果自动冲突 artifact 实现延后，仍必须 no-write 并返回 structured conflict error。
```

apply service 原子性：

- 先验证所有 touched files base hash。
- 任一文件 mismatch，整体不写。
- 所有文件验证通过后，写入前对每个 touched file 做 pre-write recheck；recheck mismatch
  视为 conflict，整体不写。
- text add/modify/delete/rename 使用 workspace-scoped per-path mutex 或等价 serialize guard，避免
  两个 apply 在同一进程内交错写同一文件。
- 每个文件写入使用 temp file + fsync/close + atomic rename；rename 前再次确认目标 current hash。
- 多文件 apply 第一版不宣称 filesystem-level atomic。实现必须为已写文件保留 before-image backup；
  后续文件失败时尝试 rollback，rollback 失败则生成 `partial_apply` / `rollback_failed` conflict
  artifact、写 `team_apply_conflicts` 和 audit，并让 UI 显示需要人工处理。
- apply/reject/conflict 都写 audit。
- apply 成功后更新 artifact `apply_status=applied`。

### 4. Bash verification policy

```text
Decision: M5 只允许 leader-triggered verification bash；member 不能直接执行任意 bash。
Date: 2026-06-01
Reason: patch verification 是 leader review 的辅助能力，不是 teammate direct execution 权限。
Impacted milestone: M5
Files/modules: internal/team/artifact_patch.go, internal/agent/tools/bash, internal/permission
Rollback strategy: 如果 bash policy 未完成，M5 patch review 仍可上线，但 verification log 标记 unavailable。
```

allow-list 第一版：

```text
go test <package>
go test ./...
npm test
pnpm test
bun test
cargo test
```

命令必须由 leader review action 触发，并通过 workspace/path/env redaction policy。

## 仍然延后的问题

以下问题不阻塞 M1-M5：

- 多 team 同时自动调度。
- direct write。
- cross-workspace team。
- remote process teammate。
- A2A auth 和 AgentCard 完整暴露。
- 自动 leader 权限审批。
