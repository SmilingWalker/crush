# 07 安全、权限与审计

AgentTeam Mode 的安全线必须早于功能线。M1 开始就要建立 actor identity 和只读隔离；
M4 才允许 permission bridge 进入可写能力前置条件。

## ActorContext

新增统一 actor identity：

```go
type ActorContext struct {
    WorkspaceID     string
    TeamID          string
    MemberID        string
    MemberName      string
    MemberRole      string
    TaskID          string
    RunID           string
    ParentSessionID string
    SessionID       string
    MessageID       string
    ToolCallID      string
    CallerChain     []ActorRef
}
```

用途：

- tool policy。
- permission request。
- hook payload。
- audit。
- log fields。
- TeamEvent。
- TUI permission dialog。

## M1 read-only policy

M1 delegate 只允许：

```text
view
grep
glob
ls
```

默认禁止：

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

可选但默认关闭：

```text
sourcegraph
fetch
```

路径规则：

- path 必须 resolve 到 workspace root 内。
- symlink resolve 后仍在 workspace root 内。
- Windows 路径比较 case-insensitive。
- 拒绝 workspace 外路径时返回 structured denial。

## 敏感文件 deny-list

即使 read-only delegate 也不能读：

```text
.env
.env.*
credentials.json
credentials.*
*.key
*.pem
*.p12
*.pfx
id_rsa
id_ed25519
*.ssh/config
```

实现位置：

- view/grep/read 类工具路径校验后、文件读取前。
- config 支持用户追加和移除，但默认必须开启。

## MCP 与 skills 过滤

M1 默认：

- 不注入 user/global MCP server instructions。
- 不暴露 MCP tools 给 delegate。
- project MCP 只有显式 `team_visible: true` 才可见。
- skills 同样按 actor policy 过滤。

M4 后 MCP write tool 需要 permission bridge 和 audit。

## Permission scope

第一版只实现：

```text
call      当前 tool call
task      当前 task
session   当前 member session
```

预留但不实现：

```text
agent
team
workspace
```

scope 必须带 constraints：

```text
tool_name
action
path_prefix
mcp_server
mcp_tool
network_host
expires_at
```

默认 UI 不给 teammate 提供 session scope 快捷授权。

## PermissionBridge

M4 实现：

```text
tool requests permission
  -> read ActorContext
  -> no team identity: delegate to existing permission service
  -> has team identity:
       check scoped grant
       create permission request row/audit/event
       publish waiting_permission
       show UI with member/task/run/tool/path
       wait response
       apply scope if allowed
       return decision
```

状态：

```text
pending
allowed
denied
expired
canceled
orphaned
```

规则：

- run canceled 后 pending request 转为 `canceled`。
- app 重启后无法恢复 channel 的 request 转为 `orphaned` 或重新投递 UI。
- response 晚到且 run/tool_call 已结束，不再生效，只写 audit。
- 默认超时后转为 `expired`，member blocked 并 report alternative。

## Team permission fact source

Team permission 使用 team 专用持久表，不扩展现有 `internal/permission` 的内存 pending
模型。现有 permission service 继续服务非 team session；`PermissionBridge` 在存在
`ActorContext.TeamID` 时写 team 表。

### `team_permission_requests`

```text
id
workspace_id
team_id
member_id
task_id
run_id
session_id
tool_call_id
tool_name
action
resource_type          file | mcp | bash | network | other
resource_ref
reason_summary
status                 pending | allowed | denied | expired | canceled | orphaned
requested_scope        call | task | session
decision_scope         nullable
decision
decided_by             user | system | hook
created_at
expires_at
decided_at
orphaned_at
```

### `team_permission_grants`

```text
id
workspace_id
team_id
member_id
task_id                nullable
session_id             nullable
tool_name
action
resource_type
resource_ref
scope                  call | task | session
source_request_id
expires_at
created_at
revoked_at
```

查询要求：

- `FindActiveGrant(actor, tool, resource)` 必须按 scope 从窄到宽匹配。
- app startup 查 `pending` request；无法重新连接 wait channel 的转 `orphaned` 并写 audit。
- late response 对已结束 `run_id/tool_call_id` 不创建 grant，只更新 request decision 并写 audit。
- `pending_permissions` snapshot 来自 `team_permission_requests where status='pending'`。

索引：

```text
team_permission_requests(team_id, status, created_at)
team_permission_requests(run_id, tool_call_id)
team_permission_grants(team_id, member_id, scope, expires_at)
team_permission_grants(source_request_id)
```

## Hook audit

hook allow 是 permission prompt 的旁路，也必须审计。

M4 起以下事件必须 audit：

- tool started/succeeded/failed/denied。
- permission requested/allowed/denied/expired。
- hook allow/deny。
- mailbox control message。
- task claim/update。
- patch apply/reject。

## Tool policy adapter

每个 tool 需要通过 adapter/metadata 暴露权限能力，不能要求一次性改完所有工具实现。
PR-1/PR-2 先引入 wrapper：

```go
type ToolPolicyMetadata struct {
    Name          string
    ReadOnly      bool
    Destructive   bool
    ResourceKinds []ResourceKind
}

type ToolPolicyAdapter interface {
    Metadata(toolName string) ToolPolicyMetadata
    ValidateInput(ctx context.Context, toolName string, input any) error
    CheckPermissions(ctx context.Context, actor ActorContext, toolName string, input any) Decision
}
```

用途：

- M1 只允许 `IsReadOnly=true` 工具。
- M3 teammate read-only runtime 用显式 capability。
- M4 permission bridge 使用 `IsDestructive` 决定 UI 强提示。

迁移策略：

- 已有工具先通过 registry metadata 标注 read-only/destructive。
- 无 metadata 的工具默认不可给 delegate/member 使用。
- 现有 `hooked_tool.go` 的 PreToolUse 继续保留，作为执行前 audit/deny 的最后一道门。
- 后续再逐步把工具内部 permission 逻辑抽到 adapter。

## Bash 安全

M1-M3 不给 teammate bash 权限。M5 如要允许 patch verification bash，必须先覆盖：

- destructive command detection。
- command substitution detection。
- heredoc/process substitution detection。
- PowerShell 特殊语法检测。
- network command host allow-list。
- env/secrets 泄露检测。
- command output redaction。

## 成本与 runaway 控制

M2 起启用：

- team/member/run max cost。
- team/member/run max tokens。
- max active members。
- max pending permissions。
- max mailbox depth。
- heartbeat timeout。

超预算时：

1. stop accepting new tasks。
2. cancel or finish current run by policy。
3. publish budget event。
4. TUI 显示 blocked/budget_exceeded。
