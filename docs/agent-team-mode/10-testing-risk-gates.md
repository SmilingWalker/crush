# 10 测试矩阵与风险门禁

AgentTeam Mode 涉及 runtime、DB、权限、UI 和多客户端。每个里程碑必须有 hard gate，
不能只靠手动演示通过。

## 测试矩阵

| 层 | 测试重点 | 包/命令落点 |
| --- | --- | --- |
| DB/sqlc | migrations、partial index、CAS、atomic claim、event seq | `go test ./internal/db/...` |
| Service | create team/member/task、mailbox receipt、audit/event transaction | `go test ./internal/team/...` |
| Runtime | MemberRunner loop、cancel、shutdown、flush、queued run、heartbeat | `go test ./internal/team/... -run Runtime` |
| Scheduler | claim race、retry policy、startup recovery | `go test ./internal/team/... -run Scheduler` |
| Permission | actor identity、scope、deny、late response、orphaned request | `go test ./internal/permission/... ./internal/team/... -run Permission` |
| Safety | workspace scope、symlink、sensitive deny-list、MCP filtering | `go test ./internal/agent/... ./internal/team/... -run Safety` |
| Tool policy | read-only/destructive classification、bash/network guard | `go test ./internal/agent/... -run ToolPolicy` |
| Proto/API | local/client parity、unknown event、snapshot/replay | `go test ./internal/server/... ./internal/client/...` |
| TUI reducer | idempotent event apply、seq gap、snapshot repair | `go test ./internal/ui/... -run Team` |
| Integration | M1 delegate, M3 teammate loop, M3.5 proposal, M4 permission, M5 patch | package-specific E2E, not full suite by default |
| Regression | old `/agent`, single chat, permission modal, session list | existing agent/session/UI tests |

## Hard gates

| Gate | 阻止进入 |
| --- | --- |
| M0.5 runtime spike 不通过 | M3 TeamRunner |
| M1 read-only safety 不通过 | 任何 delegate 默认开启 |
| M2 snapshot/event replay 不通过 | TUI 依赖 team event |
| M3 shutdown/flush 不通过 | 长期 teammate 默认开启 |
| M3.5 change proposal 写文件或进入 apply path | M5 patch artifact |
| M4 permission/audit 不完整 | teammate 写工具 |
| M5 patch conflict 不可靠 | direct write |
| M6 process/worktree 不稳定 | A2A gateway 默认启用 |

## 验收命令模板

每个 PR 在实际落地时应替换为仓库存在的精确 package，但报告格式保持一致：

```powershell
go test ./internal/db/...
go test ./internal/team/...
go test ./internal/agent/...
go test ./internal/permission/...
go test ./internal/server/... ./internal/client/...
go test ./internal/ui/...
```

如果某个 package 尚未存在，PR 必须说明“未创建该 package，本阶段不适用”，不能静默跳过。

每个里程碑最小命令：

| 里程碑 | 必跑 |
| --- | --- |
| M0/M0.5 | `go test ./internal/agent/... ./internal/team/...` |
| M1 | agent/team safety + UI reducer + delegate E2E |
| M2 | db/team/server/client snapshot + event replay |
| M2.5 | idempotency duplicate/conflict + client retry path |
| M3 | team runtime/scheduler/mailbox + shutdown/recovery + prompt envelope priority + UI reducer |
| M3.5 | change proposal artifact + review UI + no-write guard |
| M4 | permission/team audit + UI permission modal |
| M5 | artifact/content store/apply conflict + UI patch review |

## 关键测试用例

### M0.5 runtime ownership

```text
create coder runner
create member runner through AgentFactory
assert member runner is not Coordinator.currentAgent
mutate coder tools/models
assert member tools/models unchanged
start coder turn and member turn concurrently
assert AgentIsBusy only reflects coder
send second wakeup while member running
assert wakeup remains queued by MemberRunner/mailbox
assert no second running team_run is created
force SessionAgent.Run queued (nil,nil) path
assert adapter returns TurnQueued
assert no completed run/task state is written
cancel member turn through member runner
assert coder active request is untouched
stop member
assert message service flush was invoked
```

### M3 cancel semantics

```text
start member run
request CancelMemberTurn with actor/reason
assert member status becomes canceling_turn
assert member runner Cancel(session_id) is called, not Coordinator.Cancel(session_id)
assert cancel_requested event and audit are written
simulate Run returning context canceled
assert team_run becomes canceled
assert member becomes idle or blocked according to task policy
repeat same cancel request
assert no duplicate terminal event
simulate cancel timeout
assert team_run becomes interrupted
assert late Run completed result cannot overwrite interrupted/canceled
assert mailbox/task queue is not cleared by cancel current turn
```

### M1 delegate E2E

```text
enable Options.Experimental.AgentTeamPreview
start two delegates
delegate A searches parser
delegate B searches tests
both return result
leader gets aggregated result
partial state appears before all delegates finish
aggregating state disables duplicate insert
open child transcript is read-only and returns focus
cancel all stops active delegate
attempt read .env is denied
MCP instructions absent
```

### M2 domain E2E

```text
create team
spawn member
create task
list snapshot
verify event seq
verify audit row
verify client workspace can fetch same snapshot
verify deferred tables are absent in M2 migration
```

### M3 runtime E2E

```text
leader creates team
leader spawns member
leader sends message
member receives unread mailbox
member marks delivered
member runs one turn
member reports status
member marks read
compact UI updates
expanded item internal scroll works on 24-row terminal
action bar remains visible while member/activity list scrolls
```

### M3.5 change proposal E2E

```text
leader assigns implementation proposal task
member reads target files with read-only tools
member creates change_proposal artifact
proposal review modal shows target files, intent, anchor/line hint, pseudo diff, risk, verification suggestions
request revision sends structured feedback to member
accept for patch updates proposal status only
no workspace file is written
no patch apply service is called
invalid path/status/intent is rejected, but missing line/anchor or non-unified pseudo diff still renders as review-only
```

### M4 permission E2E

```text
member asks write permission
UI shows team/member/task/run/tool/path
user denies
member enters blocked
member reports alternative
second pending request stays queued
expired request disables allow actions
late allow response ignored
audit contains request and denial
non-team permission request still uses existing dialog/service behavior
hook deny blocks before bridge grant lookup
hook allow is audited with team/member/task/run context
allow task creates team-scoped grant only, not session-wide persistent permission
```

### M5 patch E2E

```text
member creates patch artifact
leader opens patch review
content_ref is opaque and backing filesystem path never appears in API/UI/member payload
content hash is verified before review/apply
file and hunk navigation works
large diff opens in summary mode
binary artifact is review-only
base hash matches -> apply succeeds
base hash mismatch -> conflict artifact
pre-write recheck mismatch -> no-write conflict
concurrent apply to same path is serialized
multi-file apply failure attempts rollback
rollback failure creates partial_apply/rollback_failed conflict artifact
no file overwritten on mismatch
audit records apply/reject/conflict
orphan blob cleanup failure writes audit/debug, not silent success
```

## Race / concurrency tests

必须覆盖：

- two members claim same task。
- duplicate mailbox delivery。
- event out of order。
- permission response after cancel。
- app shutdown while member running。
- leader run active while teammate run active。
- cost budget exceeded mid-run。
- concurrent patch apply touching same file。
- late file modification between base validation and write。

## Safety tests

必须覆盖：

- workspace 外路径。
- symlink 指向 workspace 外。
- `.env` / `.pem` / credentials。
- MCP tool not visible in M1。
- bash unavailable in M1-M3。
- hook allow writes audit。

## UI tests

建议使用现有 TUI reducer/unit 测试先覆盖：

- collapsed team item。
- expanded member rows。
- expanded team item internal viewport / overflow。
- M1 delegate lifecycle states。
- read-only child transcript modal。
- change proposal review modal。
- blocked state。
- permission modal fields。
- permission queue and timeout state。
- patch review state。
- patch diff file/hunk navigation and large diff summary。
- snapshot reload on seq gap。

## 退出报告格式

每个里程碑完成时必须有：

```text
Milestone: Mx
Feature flags:
User-visible demo:
Tests added:
Regression tests:
Known limitations:
Next blocked gates:
```
