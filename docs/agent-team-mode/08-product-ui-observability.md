# 08 Product UI 与 Observability

UI 目标不是一开始做复杂 dashboard，而是让用户能看懂 team 在做什么、卡在哪里、花了多少、
是否需要授权，以及下一步能做什么。

## UI 原则

- 第一屏仍然是 chat，不把 team mode 做成独立复杂 IDE。
- 每个 team 在 timeline 里表现为一个 compact item，可折叠、可展开、可追踪。
- UI 文案使用 `teammate`，技术字段和 API 使用 `member`。
- 所有需要用户决策的地方必须显示 actor：team、teammate、task、run、tool、resource。
- M1 不展示长期 teammate 概念，避免让用户误以为 delegate 可以互相通信或持续工作。
- M3 后才展示 roster、mailbox/activity、task board。
- M3.5 可以展示 change proposal，但必须明确它不能 apply、不能写文件。
- M5 写作业必须通过 patch review UI，不允许在聊天流里静默改文件。

## UI 信息架构

```text
Chat timeline
  Team compact item
    collapsed summary
    expanded member rows
    task board summary
    mailbox/activity summary
    change proposal summary
    action bar

Dialogs
  permission dialog
  child transcript modal
  change proposal review modal
  debug snapshot modal
  patch review/apply modal
```

## Team mode 入口与发现

第一版不做“静默自动建 team”。用户必须能看见并确认 team mode 入口，避免 leader 在用户没有心理预期时
启动多个 agent。

入口分三层：

| 阶段 | 入口 | 行为 |
| --- | --- | --- |
| M1 | command dialog / action bar: `Start read-only delegates` | 只创建一次性 delegates，不创建 durable team |
| M2 | debug command: `Create team snapshot` | 创建 team/member/task domain，用于验证 DB/API/SSE |
| M3+ | leader proposal item + command dialog: `Start teammate team` | 用户确认后 leader 才调用 `team_create` / `team_spawn_member` |

leader proposal item：

```text
Start teammate team?
reason: this task has parallel research + implementation tracks
teammates: researcher, tester
actions: start team / run as single agent / inspect plan
```

规则：

- flag off 时不显示入口、不注入 leader team guidance。
- M1 delegate 入口文案必须写清楚 `read-only` 和 `one-shot`。
- M3 team 入口必须显示将创建的 teammate 数量、角色和第一批 task summary。
- 用户选择 `run as single agent` 后，本轮不再重复弹出同一 proposal。
- 入口 item 是普通 timeline item；刷新后可从 snapshot 恢复为 started/canceled/declined。

## TUI implementation mapping

第一版不做独立 dashboard，所有状态从 chat timeline 的 team item 派生。

建议 model 结构：

```text
TeamItemModel
  team_id
  name
  collapsed bool
  last_seq
  members map[member_id]MemberRowModel
  tasks TaskBoardSummary
  activity []ActivityRow
  mailbox_unread map[member_id]int
  pending_permissions []PermissionPromptModel
  patch_artifacts []PatchArtifactRow
  cost TeamCostSummary
  loading_state idle | loading_snapshot | reconnecting | stale
  error_state none | seq_gap | snapshot_failed | runtime_unavailable
  viewport TeamViewportState
```

Reducer 输入：

```text
TeamSnapshotLoaded
TeamEventReceived
TeamEventGapDetected
MemberStateChanged
TaskChanged
MailboxReceiptChanged
PermissionRequested
PermissionResolved
PatchArtifactChanged
BudgetChanged
RuntimeHeartbeatStale
```

Reducer 规则：

- `TeamSnapshotLoaded` 覆盖当前 team item，并设置 `last_seq`。
- `TeamEventReceived.seq == last_seq + 1` 时增量更新。
- `seq <= last_seq` 忽略。
- `seq > last_seq + 1` 进入 `seq_gap`，触发 snapshot reload。
- unknown event 不破坏 UI，记录 activity 后请求 snapshot。

键位和焦点：

```text
enter      expand/collapse team item
tab        cycle member/task/activity/action focus
c          cancel focused run/member/team, 需要确认
m          send message to focused member
t          create task
p          open patch review for focused artifact
d          open debug snapshot
esc        close modal / collapse focus
```

展开项高度与滚动：

- collapsed team item 固定 1-2 行，不因 member/task 数量改变 timeline 高度。
- expanded team item 默认最大高度为可用终端高度的 45%，且不超过 18 行；低于 24 行终端时最大高度为 10 行。
- expanded item 内部有独立 viewport；当焦点在 item 内时 `up/down/pageup/pagedown` 滚动内部，
  焦点离开后恢复 chat timeline 滚动。
- action bar 固定在 expanded item 底部，不随内部 member/task/activity 列表滚走。
- 如果内容被截断，底部显示 `+N more rows`，不能让下一条 chat message 被遮住。
- permission 和 patch modal 打开时，timeline 不响应 member/task/action 焦点键。

焦点优先级：

1. pending permission modal。
2. patch review modal。
3. expanded team item action bar。
4. member rows。
5. task rows。

错误状态矩阵：

| 状态 | UI 表示 | 可用动作 |
| --- | --- | --- |
| `seq_gap` | stale badge + loading snapshot | refresh snapshot, copy trace |
| `snapshot_failed` | inline error row | retry, copy trace |
| `runtime_unavailable` | member/runtime marked unavailable | stop member, debug snapshot |
| `budget_exceeded` | budget reached banner | archive team, increase budget |
| `permission_late` | request expired/ended | view audit |

## 用户主流程

### M1：并行只读调研

```text
user asks complex research question
  -> leader proposes/starts delegates
  -> timeline shows delegate compact item
  -> user can expand and inspect child transcripts
  -> leader inserts aggregated result
```

用户看见的结果：

- 哪些 delegate 正在跑。
- 每个 delegate 当前在做什么。
- 哪个 delegate 被安全策略拦截。
- 最终聚合结果来自哪些 child session。

### M3：长期 teammate 协作

```text
user asks leader to create team
  -> leader creates team and teammate
  -> user sees compact team item
  -> leader creates task or sends message
  -> teammate wakes up and runs one turn
  -> teammate reports status
```

用户看见的结果：

- team 当前有几个 teammate，分别 idle/running/blocked。
- 当前 task 是什么。
- 最近一次 tool/action 是什么。
- 是否需要用户授权或改派任务。

### M3.5：变更提案预览

```text
user asks teammate to propose an implementation
  -> teammate reads code and task context
  -> teammate creates change_proposal artifact
  -> timeline shows proposal ready
  -> leader/user reviews, asks revise, discards, or marks accepted for patch
```

用户看见的结果：

- teammate 建议修改哪些文件。
- 每个文件为什么要改。
- 轻量 hunk / pseudo diff。
- 风险和建议验证命令。
- 明确提示：proposal 不能 apply，不能写 workspace。

### M5：安全写作业

```text
teammate produces patch artifact
  -> timeline shows patch ready
  -> user opens diff review modal
  -> user applies/rejects patch
  -> audit records decision
```

用户看见的结果：

- patch 改了哪些文件。
- base hash 是否匹配。
- verification log 是否通过。
- apply/reject 后 team 和 task 状态如何变化。

## M1：Delegate Preview UI

M1 是最早可演示 UI。

Collapsed:

```text
Delegates  2 running / 1 done  $0.04  00:38
```

Expanded:

```text
name          state     elapsed  cost   note
researcher-a  running   00:21    $0.02  scanning parser
reviewer-b    done      00:34    $0.02  result ready
tester-c      failed    00:10    $0.00  denied .env
```

Actions:

```text
cancel all
open child transcript
insert aggregated result
copy trace id
```

M1 不显示 mailbox，不显示 peer chat，不显示长期 teammate roster。

### Delegate lifecycle states

M1 必须展示 delegate group 的过渡态，而不是只展示静态 done/running：

```text
initializing   group row created, child sessions pending
running        at least one delegate running, no result yet
partial        at least one delegate done, at least one still running
aggregating    all delegates stopped/done, leader is summarizing results
ready          aggregated result available, not inserted
inserted       aggregated result inserted into chat context
canceled       user canceled group
failed         group failed before useful result
```

状态渲染：

```text
Delegates  partial  1 done / 2 running  $0.04  00:38
researcher-a  running   00:21  grep parser
reviewer-b    done      00:34  result ready
tester-c      running   00:18  reading tests
```

规则：

- `partial` 时用户可以 open 已完成 child transcript，也可以 cancel still-running delegates。
- `aggregating` 时 action bar 只保留 `open child transcript`、`copy trace id`，不允许重复 insert。
- `ready` 后 `insert aggregated result` 只执行一次；执行后状态变 `inserted`。
- `failed` 必须保留 child session id、error kind、denied resource summary。

M1 组件数据：

```text
delegate_group_id
leader_session_id
child_session_id[]
delegate_name
state
started_at
finished_at
elapsed
cost
last_activity
error_kind
```

### Open child transcript

`open child transcript` 使用只读 modal，而不是切换当前 chat session。

行为：

- 打开时显示 child session title、delegate name、state、cost、started/finished time。
- child 正在运行时，modal 可以跟随最新 transcript snapshot；关闭 modal 不 cancel child。
- transcript 内容只读，不显示输入框，不允许在 child modal 中继续对话。
- `esc` 返回原 team item；焦点回到打开前的 delegate row。
- modal 顶部显示 `read-only child transcript`，避免用户以为进入了新主 session。
- 如果 child transcript 无法加载，显示 trace id 和 `open session from session list` fallback。

## M2：Team Debug / Snapshot UI

M2 可用 debug-first UI，不需要完整产品面板。

```text
Team: parser-migration
members: 3 created / 0 running
tasks: 5 queued / 0 running / 0 done
events: last seq 42
budget: $0.00 / $2.00
```

Actions:

```text
refresh snapshot
copy snapshot json
copy team id
archive team
```

M2 UI 只承诺 debug 可见性，不承诺完整协作体验。这个限制很重要：M2 的成功标准是
durable domain 可恢复，不是 teammate 已经能工作。

## M3：Compact Team Item

M3 是真实长期 teammate 演示。

Collapsed:

```text
Team parser-migration  1 running / 1 idle / 1 blocked  $0.23  last: researcher grep parser
```

Expanded:

```text
member       role        state               task             tool   elapsed  cost
researcher   analysis    running             task_12          grep   00:38    $0.05
tester       testing     idle                -                -      -        $0.02
writer       docs        blocked             task_14          write  01:12    $0.08
```

Actions:

```text
send message
create task
cancel team
cancel member
debug snapshot
copy trace id
```

Activity summary：

```text
last 5 events
seq 42  researcher  task_claimed        task_12
seq 43  researcher  tool_started        grep
seq 44  researcher  status_reported     found parser entry
seq 45  writer      permission_needed   write docs/foo.md
seq 46  leader      message_sent        reassign task_14
```

Mailbox summary：

```text
unread by member
researcher  0
tester      2
writer      1
```

## M3.5：Change Proposal UI

M3.5 是实现型价值预览，不是 patch apply。UI 必须把 `change_proposal` 和 `patch artifact`
分开呈现。

Timeline row：

```text
Proposal proposal_01  writer  task_14  3 files  review only
```

Review modal：

```text
Change proposal proposal_01
member: writer
task: task_14
status: pending_review

files:
docs/foo.md        update API section     risk: low
internal/bar.go    adjust parser branch   risk: medium

pseudo diff:
@@ docs/foo.md
- current behavior summary
+ proposed behavior summary

suggested verification:
go test ./internal/parser/...

actions: request revision / discard / accept for patch / copy proposal
```

规则：

- proposal hunk 是轻量结构化的 review hint，不是精确 diff。UI 可以展示 file、intent、anchor、
  approximate line、before/after summary 和 pseudo diff；缺少 anchor 或 line 不阻止展示。
- `accept for patch` 不写文件，只把 proposal 状态改为 `accepted_for_patch`。
- `request revision` 通过 mailbox/task 给 teammate 发送 structured feedback。
- `discard` 写 artifact 状态和 audit，不删除历史。
- modal 标题和 action bar 必须显示 `review only`。
- proposal 的 pseudo diff 不能传给 M5 apply service；M5 必须重新生成正式 patch artifact。
- 如果 target file 在 proposal 后发生变化，UI 显示 `context may be stale`，但不做 base hash apply 判断。

## M4：Permission 与 Task Board UI

M4 复用并扩展现有 permission dialog，不做第二套完全独立弹窗。Permission dialog 必须显示：

```text
member: writer
role: docs
team: parser-migration
task: task_14 update docs
session: ses_...
tool: write
path: docs/foo.md
scope choices: allow once / allow for task / deny
reason: model-provided summary
```

Task board compact view：

```text
Tasks
queued       3
in progress 2
blocked     1
done        4
```

Expanded task rows：

```text
id       status       assignee    version  summary
task_12  in_progress  researcher  3        parser recovery
task_14  blocked      writer      2        needs write permission
```

Permission dialog 行为：

- 默认焦点在 `deny` 或最小授权，不默认扩大到 task scope。
- `allow once` 只对当前 tool call 生效。
- `allow for task` 只对同一 task、同一 member、同类 resource 生效。
- `allow for task` 只写 team-scoped grant；不能变成现有 session-wide persistent allow。
- `allow for session` 不在默认 action bar 展示；如作为 advanced action，需要二级确认。
- late response 要显示“请求已结束”，不能默默应用到新 run。
- deny 后 member 状态必须转为 blocked 或收到 structured denial。

### Permission queue / timeout

权限请求可能并发出现，UI 必须有队列规则：

- 同一时间只展示一个 permission modal。
- 排序优先级：当前聚焦 team 的 pending request > oldest `created_at` > higher risk resource。
- modal footer 显示队列摘要：`2 more permission requests pending`。
- 当前 request resolved/expired/canceled 后，自动打开下一个 pending request；如果用户按 `esc`，
  只关闭当前 modal，不自动 deny。
- request 过期时，modal actions 变 disabled，状态显示 `expired`，用户只能 `view audit` 或 `close`。
- 达到 `max_permission_pending_per_team=3` 时，team item 顶部显示 warning，并阻止新 request 创建；
  member 收到 structured denial/blocked reason。
- late response 到达时，如果 modal 仍开着，显示 `request already ended`，关闭后不创建 grant。

队列行格式：

```text
pending permissions
writer   write docs/foo.md      expires in 01:12
tester   bash go test ./...     blocked by policy
```

## M5：Patch Review UI

Patch artifact list：

```text
Patch artifacts
patch_01  writer  task_14  3 files  base ok       ready
patch_02  tester  task_20  1 file   base mismatch conflict
```

Review modal：

```text
files changed
base hash status
diff viewer
verification logs
actions: apply / reject / save conflict / copy patch
```

### Diff viewer 交互

M5 第一版使用 unified diff，不做 side-by-side。原因是 TUI 横向空间有限，unified diff 更容易在
80-120 列终端保持可读。

布局：

```text
Patch patch_01  writer  task_14  base ok  3 files  +42 -8
[1/3] docs/foo.md        modify   +20 -3
[2/3] internal/bar.go    modify   +18 -5
[3/3] assets/logo.png    binary   review-only

@@ hunk 1/4
- old line
+ new line
```

键位：

```text
j/k        next/previous line
n/p        next/previous file
] / [      next/previous hunk
f          focus file list
v          focus verification log
a          apply, requires confirm
r          reject, requires reason optional
c          copy patch/ref
esc        close review
```

大 diff 策略：

- 超过 12 files 或 2,000 changed lines 时默认进入 summary mode，只展开 file list 和 hunk headers。
- 用户可以逐文件 expand；一次最多渲染一个文件的完整 hunks。
- 单行超过 viewport width 时默认 soft wrap；可切换 horizontal scroll，但第一版不要求 side-by-side。
- 二进制文件只展示 metadata、hash、size、change kind；不能 apply binary change，除非后续阶段显式开放。
- verification log 长输出默认折叠，只显示 command、exit status、前后各 20 行；可打开完整 log modal。

Apply rules:

- base hash mismatch 不覆盖文件。
- apply 成功写 audit。
- reject 写 audit。
- conflict 生成 conflict artifact。

Patch review modal 必须展示：

```text
patch_id
member
task
base_commit_or_hash
base_status
files_changed
additions/deletions
verification_status
conflict_status
audit_link
```

M5 不做“自动 apply”。即使 leader agent 认为 patch 很安全，也必须通过可见 review/apply
动作进入主工作区。

## Observability

所有日志和 event 应带：

```text
workspace_id
team_id
member_id
task_id
run_id
session_id
tool_call_id
event_seq
trace_id
```

指标：

```text
active teams
active members
active runs
queue depth
mailbox unread
heartbeat age
run duration
permission wait duration
token/cost
cancel/retry count
patch conflict count
tool execution count by actor
```

## Event replay

TUI reducer 不依赖事件绝对可靠：

```text
load snapshot(last_seq)
subscribe SSE
apply event if seq == last_seq + 1
ignore duplicate if seq <= last_seq
reload snapshot if seq gap
```

## 空状态和错误状态

空 team：

```text
No teammates yet.
Create a teammate or start with read-only delegates.
```

blocked member：

```text
writer is blocked: write permission denied for docs/foo.md
Action: send message / reassign task / view audit
```

budget exceeded：

```text
Team budget reached: $2.00 / $2.00
New runs are paused. Increase budget or archive team.
```
