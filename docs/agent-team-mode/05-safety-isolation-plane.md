# 05 C 线：Safety & Isolation Plane

C 线负责权限、审计、MCP/skills 可见性、读写隔离。它必须早于完整 team runtime，否则多 agent 会放大现有安全盲区。

## 1. M1 workspace-scoped read-only

M1 delegate 默认只读。

允许：

```text
view
grep
glob
ls
```

默认关闭：

```text
sourcegraph
fetch
```

禁止：

```text
bash
edit
multiedit
write
download
job_output
job_kill
crush_logs
MCP tools
```

实现要求：

- path 必须 absolute/clean。
- resolve symlink。
- 检查最终路径仍在 workspace root。
- Windows path 比较要 case-insensitive。

## 2. ActorContext

M2 引入：

```go
type ActorContext struct {
    WorkspaceID     string
    TeamID          string
    AgentID         string
    AgentName       string
    AgentRole       string
    MemberID        string
    TaskID          string
    RunID           string
    ParentSessionID string
    SessionID       string
    MessageID       string
    ToolCallID      string
    CallerChain     []ActorRef
}
```

放置建议：

- 新包 `internal/actor`，避免 `agent/team/proto/permission` 循环依赖。

注入路径：

```text
TeamCoordinator/Scheduler
  -> AgentRegistry.RunAgent
  -> SessionAgentCall.RunContext
  -> tools.ActorContextKey
  -> permission/hook/audit/tool
```

## 3. Permission 扩展

当前 permission key 太粗。M3 改为 scoped grant。

scope：

```text
call
run
task
session
agent
team
workspace
```

实现要求：

- 默认 UI 不给 teammate 提供 workspace scope 快捷授权。
- `allow task` 不可扩散到另一个 task。
- grant 可过期。
- grant 带 constraints：tool/action/path-prefix/MCP server/tool/network host。

## 4. ToolExecutionEvent

Permission event 不覆盖所有工具，因此 M3 增加 tool audit。

阶段：

```text
queued
pre_hook
permission_requested
permission_resolved
started
succeeded
failed
denied
canceled
post_hook
```

默认不记录 raw input/output/env/header/file content。

最小字段：

```go
EventID
Actor
ToolCallID
ToolName
Phase
Operation
ResourceRef
InputHash
InputSummary
OutputSummary
DurationMs
ErrorClass
CreatedAt
```

## 5. Hooks

当前 hooks 只包 top-level agent。team mode 不应默认改变该行为。

实现要求：

- 增加 hook config：`include_team_members`、`include_subagents`、`actor_filter`。
- hook payload 增加 ActorContext。
- hook `allow` 也写 audit event。
- `updated_input` 记录 input hash/diff summary。

## 6. MCP/skills actor-aware

MCP：

- tool list 按 actor policy。
- server instructions 按 actor policy。
- prompts/resources API 按 actor policy。
- teammate 默认不可见 user/global MCP。

Skills：

- teammate prompt 只注入允许 skill metadata。
- global user skills 默认不暴露给 teammate。
- `view crush://skills/...` 校验 actor 可见性。

## 7. M4 patch artifact 安全写

teammate 写作业第一阶段不直接写工作区。

流程：

```text
generate patch artifact
record touched files + base hash
leader review
apply validates base hash
if mismatch -> conflict artifact
if clean -> write files + record new hash
```

必须记录：

- generated_by run_id。
- touched files。
- base hash/blob oid。
- patch text。
- verification logs。
- apply status。

## 8. 测试

必须测试：

- `grep` workspace 外路径被拒。
- symlink 指向 workspace 外被拒。
- teammate 默认看不到 MCP instructions。
- teammate 默认看不到 global skill。
- permission request 显示 team/member/task/run。
- hook allow 也有 audit。
- patch base mismatch 不覆盖文件。

