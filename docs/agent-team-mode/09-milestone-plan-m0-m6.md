# 09 里程碑计划 M0-M6

本文件按阶段说明每个里程碑要交付什么功能、怎么实现、UI 长什么样、怎么验收。

## 总览

| 里程碑 | 用户功能 | 工程结果 | 演示能力 |
| --- | --- | --- | --- |
| M0 | 无 | 设计冻结、flag、issue ready | 无 |
| M0.5 | 无 | MemberRunner hidden spike | 内部 debug 演示 |
| M1 | 并行只读 delegates | DelegateRunner + read-only safety | 可产品演示 |
| M2 | team/member/task snapshot | DB/API/SSE/TeamService | debug 演示 |
| M3 | 长期 teammate 收消息和跑任务 | TeamRunner + mailbox + scheduler | 真正 team 演示 |
| M4 | 权限桥和共享任务板 | permission/audit/task CAS | 安全协作演示 |
| M5 | patch artifact 写作业 | patch review/apply | 可控写代码演示 |
| M6 | worktree/process/A2A | external runtime adapter | 高级扩展演示 |

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

- `experimental.agent_team_preview=false` 默认关闭。
- old `/agent` 行为不变。
- M1 issue 可以拆分。
- M0.5 spike 任务明确。

## M0.5：Hidden Runtime Spike

### 功能

无用户功能。只做内部 debug。

### 实现

- `MemberRunner` in-memory loop。
- 独立 member session。
- `Start/Send/CancelCurrentTurn/Stop/Status`。
- app shutdown cancel/wait/flush。

### 交付件

- hidden spike 代码或 spike branch。
- MemberRunner 状态机测试。
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
- cancel 后可继续。
- stop 不泄漏 goroutine。
- `SessionAgent.Run` queued case 不误判 completed。

## M1：Safe Team Preview / Read-only Delegates

### 用户功能

leader 可以一次派出 1-3 个只读 delegates 做并行调研，并把结果聚合回当前 session。

### 实现

- `DelegateRunner`。
- `DelegateRunGroup`。
- child session per delegate。
- read-only tool policy。
- ActorContext skeleton。
- sensitive file deny-list。
- MCP/skills filtering。
- cancel group。

### 交付件

- `DelegateRunner` 和 `DelegateRunGroup`。
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

Actions:

```text
cancel all
open child transcript
insert aggregated result
```

### 退出条件

- 两个 delegate 可并行执行。
- delegate 只能读 workspace 内非敏感文件。
- `.env`、`*.pem`、`credentials.*` 拒绝。
- delegate 不注入 user/global MCP instructions。
- cancel group 生效。
- result 聚合可追踪到 child session。
- flag off 无任何 UI/API/prompt/tool 变化。

## M2：Durable Team Domain + TeamService

### 用户功能

可以创建 team、添加 member、创建 task，并查看 snapshot/debug 状态。M2 不要求长期
teammate 开始工作。

### 实现

- team DB schema。
- sqlc queries。
- `team.Service`。
- `TeamWorkspace` local/client interface。
- HTTP routes。
- `PayloadTypeTeamEvent`。
- `TeamSnapshot` / `DebugSnapshot`。
- AgentRegistry skeleton。
- cost budget fields。

### 交付件

- DB migration 和 sqlc queries。
- `team.Service` transactional API。
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

### 退出条件

- create team/member/task 可用。
- snapshot 包含 team/members/tasks/runs/events/cost。
- event seq 可 replay。
- server/client 和 local mode 都支持。
- old `/agent` 行为不变。

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
- compact team item。

M3b:

- mailbox。
- message receipts。
- task dependencies。
- prompt envelope builder。
- member reporting tools。

### 交付件

- durable mailbox 和 receipt。
- TeamRunner + MemberRunner lifecycle。
- scheduler claim/wakeup/heartbeat/recovery。
- teammate prompt envelope。
- `team_report_status` / `team_send_message` tools。
- M3 compact team item。

### UI

```text
Team parser-migration  1 running / 1 idle / 0 blocked  $0.12
researcher  running  task_12  grep  00:38
tester      idle     -        -     -
```

### 退出条件

- leader spawn member。
- member 有独立 session。
- leader send message 后 member 被唤醒。
- member run one turn。
- member report status。
- heartbeat/recovery 可测。
- mailbox delivered/read 可追踪。
- compact UI 可展示状态。

## M4：Permission Bridge + Audit + Shared Task Board

### 用户功能

teammate 请求权限时，UI 能显示是谁、为什么、对哪个 task、哪个文件、哪个工具发起请求。
用户可以 allow once、allow task、deny。task board 支持 CAS 更新和 blocked 状态。

### 实现

- PermissionBridge。
- scoped grants: call/task/session。
- permission pending state machine。
- team audit coverage。
- tool execution events。
- task get/list/update/claim CAS。
- hook allow audit。

### 交付件

- team-aware permission request source。
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

### 退出条件

- permission UI 展示 team/member/task/run/tool/path。
- deny 后 member blocked 并 report alternative。
- allow task 不扩散到其他 task。
- late response 不影响已结束 run。
- audit 可查询。
- task version conflict 返回 structured error。

## M5：Safe Patch Artifact Write

### 用户功能

teammate 可以生成 patch artifact。leader 在 UI 中 review diff，然后 apply 或 reject。

### 实现

- patch artifact。
- base hash。
- touched files summary。
- verification log artifact。
- conflict artifact。
- apply-only base/hash conflict check。
- apply/reject audit。
- optional patch verification bash under strict policy。

### 交付件

- patch artifact schema 和 content store。
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

### 退出条件

- patch artifact 可生成。
- base mismatch 不覆盖文件。
- conflict artifact 可见。
- M5 不创建 worktree runtime，不给 teammate direct write lease。
- apply/reject 写 audit。
- verification log 可查看。

## M6：Advanced Runtime / Worktree / A2A Gateway

### 用户功能

高级 teammate 可以在 worktree/process 中运行，或通过 A2A gateway 接入外部 agent。

### 实现

- worktree runtime。
- process backend。
- crash recovery。
- permission forwarding。
- A2A AgentCard/Task/Message/Artifact mapping。
- network/direct-write policy。

### 交付件

- runtime adapter interface。
- worktree/process backend proof。
- crash recovery tests。
- A2A gateway mapping doc/code skeleton。
- advanced backend UI state。

### UI

advanced team panel 增加 runtime backend：

```text
member       backend      state
researcher   in-process   running
builder      worktree     testing
remote-docs  a2a          waiting
```

### 退出条件

- worktree task 可独立测试。
- process crash 可恢复。
- A2A gateway 不替代内部 TeamRuntime。
- direct write 受 lease/hash/policy 控制。
