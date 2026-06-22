# 09 里程碑计划 M0-M6

本文件按阶段说明每个里程碑要交付什么功能、怎么实现、UI 长什么样、怎么验收。

## 总览

| 里程碑 | 用户功能 | 工程结果 | 演示能力 |
| --- | --- | --- | --- |
| M0 | 无 | 设计冻结、flag、issue ready | 无 |
| M0.5 | 无 | MemberRunner hidden spike | 内部 debug 演示 |
| M1 | 并行只读 delegates | DelegateRunner + read-only safety | 可产品演示 |
| M2 | team/member/task snapshot | DB/API/SSE/TeamService | debug 演示 |
| M2.5 | 可重试 team write API | idempotency + API hardening | server/client 稳定性演示 |
| M3 | 长期 teammate 收消息和跑任务 | TeamRunner + mailbox + scheduler | 真正 team 演示 |
| M3.5 | change proposal 预览 | proposal artifact + review UI | 实现型价值演示 |
| M4 | 权限桥和共享任务板 | permission/audit/task CAS | 安全协作演示 |
| M5 | patch artifact 写作业 | patch review/apply | 可控写代码演示 |
| M6 | worktree/process | external runtime adapter | 高级扩展演示 |

## 阶段推进模板

每个里程碑都按同一套推进方式执行，避免出现“写了很多底层代码但无法演示”或“UI 有了但
runtime 不可恢复”的情况。

| 检查项 | 要求 |
| --- | --- |
| 功能边界 | 明确用户在本阶段能做什么、不能做什么 |
| 工程交付 | 有 DB/API/runtime/UI/test 的具体产物，不能只写概念 |
| 演示脚本 | 至少有一个从用户动作到可见结果的脚本 |
| 安全门禁 | 说明本阶段允许的 tool、权限、写入范围和审计要求 |
| 退出条件 | 可以自动化验证的尽量自动化，不能自动化的给手动验收步骤 |
| 回滚边界 | 通过 feature flag 或独立 PR 保证可回滚 |

阶段报告格式：

```text
Milestone: Mx
Delivered:
  - runtime:
  - data:
  - safety:
  - product/ui:
Demo:
  - script:
  - expected result:
Blocked/Deferred:
  - item:
Next:
  - issue/PR:
```

## M0：方案冻结

### 功能

无用户功能。

### 实现

- 确认正式 docs 目录。
- 新增 feature flags 草案。
- 建立 `internal/team` package skeleton。
- 冻结命名：implementation 用 `member`，UX 用 `teammate`。
- 冻结 M1/M2/M3 的边界。

### 交付件

- `docs/agent-team-mode` 作为唯一正式方案目录。
- feature flag 设计说明。
- `internal/team` package skeleton 或 issue 级别 skeleton plan。
- M1-M3 issue 列表，每个 issue 指向对应专题文档。

### UI

无 UI，仅隐藏配置。

### 退出条件

- `Options.Experimental.AgentTeamPreview=false` 且 `Options.Experimental.AgentTeam=false` 默认关闭。
- old `/agent` 行为不变。
- M1 issue 可以拆分。
- M0.5 spike 任务明确。

## M0.5：Hidden Runtime Spike

### 功能

无用户功能。只做内部 debug。

### 实现

- `MemberRunner` in-memory loop。
- 独立 member child session。
- 独立 `SessionAgent` runner，不复用 `Coordinator.currentAgent`。
- member runner single-flight gate；不依赖 `SessionAgent.messageQueue` 追踪 team lifecycle。
- `Start/Send/CancelCurrentTurn/Stop/Status`。
- app shutdown cancel/wait，并通过 message service flush。

### 交付件

- hidden spike 代码或 spike branch。
- MemberRunner 状态机测试。
- AgentFactory 独立 runner ownership 测试。
- shutdown/cancel goroutine 泄漏检查。
- spike 结论：能否复用 `SessionAgent.Run`，需要抽取哪些接口。

### UI

无正式 UI。允许 debug log：

```text
[team-spike] mate started session=s...
[team-spike] prompt queued
[team-spike] turn canceled
[team-spike] stopped flushed=true
```

### 退出条件

- mate run 不阻塞 leader。
- mate runner 与 coder runner 的 tools/models/messageQueue/activeRequests 隔离。
- cancel 后可继续。
- stop 不泄漏 goroutine。
- `SessionAgent.Run` queued case 不误判 completed。
- 没有 queued-start callback 时，不把 `SessionAgent.messageQueue` 用作 team mailbox。

## M1：Safe Team Preview / Read-only Delegates

### 用户功能

leader 可以一次派出 1-3 个只读 delegates 做并行调研，并把结果聚合回当前 session。

### 实现

- `DelegateRunner`。
- in-memory `DelegateRunGroup`。
- child session per delegate。
- independent `SessionAgent` runner per delegate。
- read-only tool allow-list：`view`、`grep`、`glob`、`ls`。
- `internal/actor.ActorContext` skeleton。
- sensitive file deny-list。
- MCP/skills filtering。
- cancel group。
- M3-reusable primitives: child session creation, actor context propagation, tool filtering, cancel/result aggregation。

### 交付件

- `DelegateRunner` 和 in-memory `DelegateRunGroup`。
- actor-aware read-only tool policy。
- sensitive deny-list tests。
- delegate result 聚合结构。
- TUI delegate compact item。
- M1 演示脚本。

### UI

```text
Delegates  2 running / 1 done  $0.04
researcher-a  running  00:21  grep parser
reviewer-b    done     00:34  result ready
```

必须支持 lifecycle state：

```text
initializing -> running -> partial -> aggregating -> ready -> inserted
                         -> canceled / failed
```

Actions:

```text
cancel all
open child transcript
insert aggregated result
```

### 退出条件

- 两个 delegate 可并行执行。
- delegate 只能读 canonical workspace root 内非敏感文件。
- delegate 拒绝 `07-safety-permission-audit.md` / `12-open-architecture-issues.md`
  定义的敏感文件 deny-list 中所有默认模式，并覆盖 path 绕过测试。
- delegate 不注入 user/global MCP instructions。
- cancel group 生效。
- result 聚合可追踪到 child session。
- partial/aggregating/ready/inserted 状态可见且 action 不重复执行。
- open child transcript 是只读 modal，关闭后回到原 delegate row。
- M1 不创建 durable team tables，不写 `team_runs` / `team_events`，不依赖 AgentRegistry 持久状态。
- M1 primitives 在 M3 `MemberRunner`/tool filtering/cancel path 中可复用。
- flag off 无任何 UI/API/prompt/tool 变化。

## M2：Durable Team Domain + TeamService

### 用户功能

可以创建 team、添加 member、创建 task，并查看 snapshot/debug 状态。M2 不要求长期
teammate 开始工作。

### 实现

- team DB schema。
- sqlc queries。
- `team.Service`。
- M2 core stores: `TeamStore`、`MemberStore`、`TaskStore`、`RunStore`、`EventStore`、`AuditStore`。
- `TeamWorkspace` local/client interface。
- HTTP routes。
- `PayloadTypeTeamEvent`。
- `TeamSnapshot` / `DebugSnapshot`。
- AgentRegistry skeleton。
- cost budget fields。

M2 core tables:

```text
teams
team_members
team_tasks
team_runs
team_events
team_event_counters
team_audit_events
```

M2 不创建 mailbox、artifact、session links 或 idempotency tables；这些进入 M3/M2.5。

### 交付件

- DB migration 和 sqlc queries。
- `team.Service` transactional API。
- M2 store implementation，不包含 mailbox/artifact/permission/patch store。
- local/server/client workspace team contract。
- `PayloadTypeTeamEvent` 与 snapshot replay。
- debug snapshot UI。

### UI

debug/snapshot view：

```text
Team parser-migration
members: 2 created
tasks: 4 queued
events: seq 18
budget: $0.00 / $2.00
```

Actions:

```text
refresh snapshot
copy snapshot json
copy team id
archive team
```

### 退出条件

- create team/member/task 可用。
- snapshot 包含 team/members/tasks/runs/events/cost。
- event seq 可 replay。
- M2 migration 只包含 core tables；`team_session_links`、mailbox/artifact/dependency 表和
  `team_idempotency_keys` 不在 M2 创建。
- M2 `team.Service` 不暴露未到阶段的 mailbox/permission/patch use case；调用返回
  `feature_disabled` / `not_implemented`。
- server/client 和 local mode 都支持。
- debug snapshot UI 可从 seq gap/stale state 恢复。
- old `/agent` 行为不变。

## M2.5：Server/client API Hardening

### 用户功能

用户不直接感知新功能；这一阶段的价值是让 server/client mode 的 team 写操作可以安全重试，
避免网络断开、client 重连或 SSE gap repair 时重复创建 team/member/task。

### 实现

- `team_idempotency_keys` migration。
- retryable write API contract。
- request hash / response ref 存储。
- idempotency conflict structured error。
- server/client tests for duplicate request and mismatched request hash。

### 交付件

- `team_idempotency_keys` 表与 sqlc queries。
- CreateTeam / SpawnMember / CreateTask idempotency enforcement。
- API wrapper 对 idempotency conflict 的错误编码。
- client retry path 测试。

### 退出条件

- 相同 key + 相同 request 返回原 response ref，不重复写 domain row/event/audit。
- 相同 key + 不同 request 返回 `idempotency_conflict`。
- 未带 key 的 write API 要么明确不可重试，要么被拒绝。
- local/debug path 可以保留 optional/no-op，但 server/client retryable contract 必须强制执行。
- M2 core migration 不被回改；M2.5 migration 独立可回滚。

## M3：In-process TeamRunner + Mailbox + Scheduler

### 用户功能

leader 可以 spawn 一个长期 teammate，给它发消息或分配任务。teammate 收到任务后运行
一轮，并通过 `team_report_status` 或 `team_send_message` 回报。

### 实现

M3a:

- TeamRunner。
- MemberRunner。
- scheduler claim。
- run heartbeat。
- startup recovery。
- fixed shutdown/cancel/flush order。
- compact team item。

M3b:

- mailbox。
- message receipts。
- session links for member visibility/recovery。
- task dependencies。
- M3 stores: `MailboxStore`、`ReceiptStore`、`DependencyStore`、`ArtifactStore`、`SessionLinkStore`。
- prompt envelope builder。
- member reporting tools。

### 交付件

- durable mailbox 和 receipt。
- TeamRunner + MemberRunner lifecycle。
- scheduler claim/wakeup/heartbeat/recovery。
- graceful shutdown and startup stale recovery policy。
- teammate prompt envelope。
- `team_report_status` / `team_send_message` tools。
- M3 compact team item。

### UI

```text
Team parser-migration  1 running / 1 idle / 0 blocked  $0.12
researcher  running  task_12  grep  00:38
tester      idle     -        -     -
```

expanded team item 必须有内部 viewport 和 action bar，不能把 chat timeline 挤出可读范围。

### 退出条件

- leader spawn member。
- member 有独立 child session 和 runner。
- leader send message 后 member 被唤醒。
- member run one turn。
- member report status。
- heartbeat/recovery 可测。
- cancel member turn 可测：member 进入 `canceling_turn`，当前 run 进入 `canceled` 或
  `interrupted`，late result 不覆盖 terminal state。
- shutdown 顺序可测：stop wakeups -> event/audit -> cancel/wait -> message service flush -> mark stopped -> publish。
- stale run recovery 可测：heartbeat 过期 run 标记 `interrupted`，member 恢复 idle/blocked。
- M3 不消费 permission wait/approval；permission control message 只保留 schema。
- prompt envelope priority 可测：task 不截断、direct 优先 broadcast、peer input untrusted。
- mailbox delivered/read 可追踪。
- compact UI 可展示状态。
- expanded item 在 24 行终端下不遮挡后续 chat content，内部滚动可测。

## M3.5：Change Proposal Preview

### 用户功能

teammate 可以在只读能力下生成 `change_proposal` artifact。leader/user 可以 review、request revision、
discard 或 accept for patch。M3.5 不允许 apply，不写 workspace。

### 实现

- `change_proposal` artifact kind。
- proposal metadata: target files、轻量结构化 proposed hunks、risk、verification suggestions。
- proposal status: pending_review / revise_requested / discarded / accepted_for_patch。
- proposal review modal。
- mailbox feedback path for request revision。

### 交付件

- `AppendArtifact(kind=change_proposal)`。
- proposal list/review UI。
- proposal status update service。
- M3.5 演示脚本。

### UI

```text
Proposal proposal_01  writer  task_14  3 files  review only
Actions: review / request revision / discard / accept for patch
```

review modal 必须显示 target files、轻量 hunk/pseudo diff、risk、suggested verification，并明确 `review only`。

### 退出条件

- teammate 能生成 change proposal。
- proposal 不使用 write/edit/bash，不写 workspace。
- review modal 可展示 target files、intent、anchor/line hint、before/after summary 和 pseudo diff。
- proposal 只做轻量结构校验；line/anchor/pseudo diff 不要求精确可应用。
- request revision 会回到 mailbox/task feedback path。
- accept for patch 只更新 proposal 状态，不生成可 apply patch。
- discard 写 audit/debug history。

## M4：Permission Bridge + Audit + Shared Task Board

### 用户功能

teammate 请求权限时，UI 能显示是谁、为什么、对哪个 task、哪个文件、哪个工具发起请求。
用户可以 allow once、allow task、deny。task board 支持 CAS 更新和 blocked 状态。

### 实现

- PermissionBridge as team-aware wrapper over existing `permission.Service` / PreToolUse hook flow。
- `PermissionStore` / `GrantStore`。
- scoped grants: call/task/session。
- permission pending state machine。
- team audit coverage。
- tool execution events。
- task get/list/update/claim CAS。
- hook allow audit。

### 交付件

- team-aware permission request source。
- existing permission dialog extension, not a parallel modal。
- scoped grant state machine。
- permission dialog with member/task/run context。
- task CAS/debug UI。
- audit query/report。

### UI

```text
Permission request
member: writer
task: task_14
tool: write
path: docs/foo.md
scope: once | task | deny
```

队列/超时：

```text
pending permissions: 2 more
expires in: 01:12
expired -> actions disabled -> view audit / close
```

### 退出条件

- permission UI 展示 team/member/task/run/tool/path。
- 非 team session 仍走现有 permission service，行为不变。
- `allow once` 只 resolve 当前 tool call；`allow task` 创建 team-scoped grant，不写现有 session-wide
  persistent grant。
- 多个 pending permission 按队列展示，当前请求 resolve/expire/cancel 后可进入下一条。
- expired/late response 不创建 grant，UI 显示 request ended。
- deny 后 member blocked 并 report alternative。
- allow task 不扩散到其他 task。
- hook deny 仍阻止执行；hook allow 仍必须写 team audit。
- late response 不影响已结束 run。
- audit 可查询。
- task version conflict 返回 structured error。

## M5：Safe Patch Artifact Write

### 用户功能

teammate 可以生成 patch artifact。leader 在 UI 中 review diff，然后 apply 或 reject。

### 实现

- patch artifact。
- `PatchStore` / `ContentStore` / `ApplyConflictStore`。
- app data filesystem blob store as M5 first implementation。
- base hash。
- touched files summary。
- verification log artifact。
- conflict artifact。
- apply-only base/hash conflict check。
- apply/reject audit。
- optional patch verification bash under strict policy。

### 交付件

- patch artifact schema 和 content store。
- filesystem blob store with opaque ref/hash contract。
- patch review/apply service。
- base hash conflict handling。
- `team_apply_conflicts` 记录 apply 失败原因。
- patch review UI。
- verification log artifact。
- apply/reject audit。

### UI

```text
Patch patch_01  3 files  base ok  ready
Actions: review / apply / reject
```

review modal 使用 unified diff，支持 file/hunk 导航、大 diff summary mode、binary review-only。

### 退出条件

- patch artifact 可生成。
- content store 第一版落在 app data，不落在 editable workspace；API/DB/UI 只暴露 opaque ref。
- base mismatch 不覆盖文件。
- conflict artifact 可见。
- patch review UI 可展示 file list、hunk、verification log、binary review-only 状态。
- 大 diff 不撑爆 TUI；超过阈值默认 summary mode。
- M5 不创建 worktree runtime，不给 teammate direct write lease。
- apply/reject 写 audit。
- verification log 可查看。

### 后续改进

- M5.1 orphan blob sweep。
- M5.2 workspace/team quota、单 artifact size limit、retention policy。
- M6+ DB blob / CAS object store / remote backend adapter。
- 可选 content encryption；不改变 `ContentStore` API。

## M6：Advanced Runtime / Worktree / Process Gateway

> **⚠️ A2A 已暂缓（2026-06-22 决定）**：本里程碑后续不再实现 A2A（Agent-to-Agent）协议网关。
> runtime backend 范围收窄为 `in_process` / `worktree` / `process` 三种。A2A 的
> AgentCard/Task/Message/Artifact 映射、外部 agent 接入相关条目从本里程碑移除，待将来
> 重新评估需求后再单独立项。M7-05 A2A Gateway 任务标记为 deferred。

### 用户功能

高级 teammate 可以在 worktree/process 中运行。

### 实现

- worktree runtime。
- process backend。
- crash recovery。
- permission forwarding。
- network/direct-write policy。

> ~~A2A AgentCard/Task/Message/Artifact mapping。~~ （暂缓，见上方说明）

### 交付件

- runtime adapter interface。
- worktree/process backend proof。
- crash recovery tests。
- advanced backend UI state。

> ~~A2A gateway mapping doc/code skeleton。~~ （暂缓，见上方说明）

### UI

advanced team panel 增加 runtime backend：

```text
member       backend      state
researcher   in-process   running
builder      worktree     testing
```

### 退出条件

- worktree task 可独立测试。
- process crash 可恢复。
- direct write 受 lease/hash/policy 控制。
