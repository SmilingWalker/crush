# 06 D 线：Product Integration Plane

D 线负责让 team 功能可见、可控、可回滚，包括 feature flag、TUI、observability、E2E 和文档。

## 1. Feature flag

当前 config 没有 experimental 分组。建议新增：

```go
type ExperimentalOptions struct {
    AgentTeamPreview bool `json:"agent_team_preview,omitempty"`
    AgentTeam        bool `json:"agent_team,omitempty"`
}
```

配置示例：

```json
{
  "experimental": {
    "agent_team_preview": true
  }
}
```

要求：

- 默认 false。
- flag off 时不改变 prompt/tools/UI/API。
- M1 只使用 `agent_team_preview`。
- M2+ 再使用 `agent_team`。

## 2. M1 Delegates UI

不做完整 Team panel，只做当前 session 下的 compact panel。

展示：

```text
Delegates
  researcher-a  running   00:21
  reviewer-b    done      00:34
```

操作：

- start delegates。
- cancel all。
- open child transcript。
- append aggregated result。

状态：

```text
pending
running
done
failed
canceled
interrupted
```

## 3. M2 Team panel

M2 展示 durable team：

```text
Team
  Members
    leader       idle
    researcher   running task-123

  Tasks
    task-123     running
    task-124     open

  Runs
    run-abc      streaming
```

不做：

- peer chat。
- artifact apply。
- dependency graph。

## 4. M3 Mailbox UI

新增：

- direct messages。
- unread/ack。
- teammate asks leader。
- retry/cancel/pause/resume。
- interrupted run indicator。

要求：

- teammate 问题必须明显展示。
- leader 回答后有明确 “continue task”。
- peer message 默认折叠，避免噪音。

## 5. M4 Artifact review UI

新增：

- patch artifact list。
- touched files summary。
- diff preview。
- apply/reject。
- conflict display。
- verification logs。

## 6. Observability

日志字段：

```text
team_id
member_id
task_id
run_id
session_id
tool_call_id
```

指标：

- active teams。
- active members。
- active runs。
- run duration。
- token/cost。
- cancel count。
- retry count。
- permission wait duration。
- tool execution count by actor。
- conflict count。

## 7. E2E 验收

M1：

- flag off 无 UI。
- start two delegates。
- aggregate result。
- cancel all。

M2：

- create team。
- spawn member。
- assign task。
- run completed。
- SSE reconnect replay。

M3：

- teammate ask leader。
- leader answer。
- task continues。
- interrupted run recovery。

M4：

- patch artifact review。
- apply clean patch。
- conflict patch 不覆盖。

