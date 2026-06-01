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

## M1 冻结决策

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
Decision: M1 默认拒绝 `.env`、`.env.*`、`*.pem`、`*.key`、`credentials.*`、`secrets.*`、SSH key、cloud credential 常见路径，并允许项目配置追加。
Date: 2026-06-01
Reason: delegate 虽然只读，也不能读取高敏凭据；默认 deny-list 是 M1 演示可接受的最低安全线。
Impacted milestone: M1
Files/modules: internal/agent/tools, internal/team, internal/config
Rollback strategy: 如果误伤项目文件，通过项目级 allow exception 配置处理，但必须写 audit/debug reason。
```

### 3. MCP instructions policy

```text
Decision: M1 delegate 默认不注入 user/global MCP instructions，不暴露 MCP tools；project MCP 必须显式 `team_visible` 才可进入后续阶段 allow-list。
Date: 2026-06-01
Reason: MCP instruction 和 tool 可能扩大模型权限面，M1 要先证明本地 read-only delegate 安全。
Impacted milestone: M1-M2
Files/modules: internal/agent/tools/mcp, tool build options, skills loading/filtering
Rollback strategy: 后续通过 actor-aware policy 精确开放，不回溯修改 M1 默认策略。
```

## M3 前仍需冻结

### 1. MemberRunner shutdown 顺序

建议：

```text
stop accepting wakeups
append shutdown event
cancel current turn if needed
wait with timeout
FlushAll
mark stopped
publish event
```

### 2. Prompt envelope 优先级

必须冻结：

- task description 不可截断。
- direct messages 优先于 broadcast。
- peer mailbox 视为 untrusted。
- system/developer/tool policy 永远最高优先级。

### 3. Partial cost accounting

建议：

```text
finished run: final usage
canceled/interrupted with provider usage: partial usage
canceled/interrupted without provider usage: usage unknown
```

UI 显示 unknown，不假装为 0。

### 4. Mailbox control message 边界

M3 支持 permission control message schema 预留，但不消费 permission wait/approval。
`permission_request` / `permission_response` 的实际状态机和 runner handling 放 M4。

## M4 前仍需冻结

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

### 2. Apply conflict 策略

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
- 所有文件验证通过后再 apply。
- apply/reject/conflict 都写 audit。
- apply 成功后更新 artifact `apply_status=applied`。

### 3. Bash verification policy

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
