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
- M5 写作业必须通过 patch review UI，不允许在聊天流里静默改文件。

## UI 信息架构

```text
Chat timeline
  Team compact item
    collapsed summary
    expanded member rows
    task board summary
    mailbox/activity summary
    action bar

Dialogs
  permission dialog
  debug snapshot modal
  patch review/apply modal
```

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

## M4：Permission 与 Task Board UI

Permission dialog 必须显示：

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
- late response 要显示“请求已结束”，不能默默应用到新 run。
- deny 后 member 状态必须转为 blocked 或收到 structured denial。

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
