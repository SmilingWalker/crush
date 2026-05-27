# 07 M2 Durable Team Skeleton

M2 的目标是把 M1 的 preview 升级为 durable team skeleton：team/member/task/run/event 可以落库和重放，但协作仍保持简单。

## 1. 范围

包含：

- `AgentRegistry`。
- `RunAgent`。
- `SessionAgentCall.RunContext`。
- `teams/team_members/team_tasks/team_runs/team_events/team_session_links`。
- Team API。
- `PayloadTypeTeamEvent`。
- snapshot + event replay。
- Team panel 初版。

不包含：

- peer mailbox。
- task dependencies。
- pause/resume/retry。
- patch artifact。
- direct write。

## 2. 创建 team

事务：

```text
CreateTeamTx:
  insert teams
  insert leader team_member
  insert team_session_links(role=leader_root)
  insert team_events(team.created, member.created)
```

要求：

- leader member 映射当前 coder。
- 不创建新的 leader agent。
- commit 后 publish `team_event`。

## 3. 创建 teammate

流程：

```text
validate policy/model
create member root session
AgentRegistry.Register(AgentSpec)
SpawnMemberTx
publish member.created/member.ready
```

失败策略：

- registry 构建失败前，不写 active member。
- member 已入库但 runtime 失败，status=`failed`，允许 retry。

## 4. 分配 task

M2 允许简单 assign：

```text
POST /teams/{team_id}/tasks
  -> insert team_tasks(status=open)
  -> scheduler or immediate runner creates team_runs(status=queued/running)
  -> RunAgent
  -> FinishRunTx
```

要求：

- task session parent 指向 member root session。
- `RunAgent` 带 `TeamID/MemberID/TaskID/RunID`。
- result 写 task/run 状态。

## 5. SSE replay

新增：

```go
PayloadTypeTeamEvent = "team_event"
```

client：

- 识别 team event。
- unknown event 仍安全忽略。

reconnect：

```text
snapshot -> since_seq -> events replay -> subscribe
```

## 6. 验收

必须通过：

- 创建 team 后 DB 有 leader member。
- spawn teammate 后 registry 有 runtime。
- assign task 后 run completed。
- `ListSessions` 不显示 member/task child sessions。
- old `/agent` 行为不变。
- old busy 只看 coder。
- team event 可 replay。

