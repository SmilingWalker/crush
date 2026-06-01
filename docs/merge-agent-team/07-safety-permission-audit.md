# 07 Safety Permission Audit

## 整合来源

- 当前 repo：ActorContext 前置、read-only policy、敏感文件 deny-list、MCP 过滤、
  Tool 四钩子、三层 permission scope。
- Notion：TeamIdentity、PermissionBridge、permission source、audit 字段。

## 取舍

安全路线采用当前 repo 的保守策略；permission bridge 吸收 Notion 的 identity 和 audit 设计。

## M1 必须完成

- workspace-scoped read-only。
- sensitive file deny-list。
- MCP instructions 默认不注入 delegate。
- ActorContext skeleton。
- 禁止 bash/edit/write/job tools/MCP write。

允许工具：

```text
view
grep
glob
ls
```

默认关闭或禁止：

```text
sourcegraph
fetch
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

## ActorContext / TeamIdentity

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

Mapping:

```text
ActorContext
  -> TeamIdentity
  -> PermissionSource
  -> Hook payload/env
  -> ToolExecutionEvent
```

## Permission Scope

合并取舍：

- 不采用 Notion 的 `one_shot/session/agent/team` 全量开放。
- 采用当前 repo 的初期三级：

```text
call
task
session
```

预留但不实现：

```text
agent
team
workspace
```

要求：

- 默认 UI 不给 teammate 提供 session scope 快捷授权。
- `allow task` 不可扩散到另一个 task。
- grant 可过期。
- grant 带 constraints：tool/action/path-prefix/MCP server/tool/network host。

## PermissionBridge Flow

```text
tool requests permission
  -> bridge reads ActorContext / TeamIdentity
  -> no team identity: delegate to base permission service
  -> has team identity:
       check scoped grant cache
       append permission_request mailbox/audit
       publish TeamEvent waiting_permission
       wait permission_response
       apply scope if allowed
       return decision
```

Leader model 不默认审批权限。未来 opt-in 必须：

- 显式开启。
- 限 tool/action/path/scope。
- 有过期时间。
- 写 audit。
- TUI 强提示。

## Tool 四钩子

```go
CheckPermissions(ctx, input)
ValidateInput(ctx, input)
IsReadOnly(input)
IsDestructive(input)
```

用途：

- M1 临时用工具白名单。
- M3a 切换到显式 tool capability。
- teammate 只能使用 `IsReadOnly=true` 的工具，直到进入写能力阶段。

## Audit 要求

M4 起，以下事件必须 audit：

- tool started/succeeded/failed/denied。
- permission requested/allowed/denied。
- hook allow。
- mailbox control message。
- task claim/update。
- patch apply/reject。

默认不记录 raw input/output/env/header/file content。记录 input hash、summary、resource ref。

## Bash 安全

teammate 获得 bash 权限前必须补齐：

- 危险命令检测。
- 命令替换检测。
- heredoc/process substitution 检测。
- PowerShell/Zsh 特殊语法检测。
- network 命令目标白名单。
- 环境变量泄露防护。

M1-M3 不给 teammate bash 权限。

