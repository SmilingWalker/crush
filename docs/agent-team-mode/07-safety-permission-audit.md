# 07 安全、权限与审计

AgentTeam Mode 的安全线必须早于功能线。M1 开始就要建立 actor identity 和只读隔离；
M4 才允许 permission bridge 进入可写能力前置条件。

## ActorContext

新增统一 actor identity，canonical package 是 `internal/actor`。它是 agent、team、permission、
hooks、tools 共享的轻量上下文包，不依赖 `internal/team`，避免底层工具为了读 actor 身份反向依赖
team domain。

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

M1 delegate 只做窄 allow-list，不引入新的工具权限执行链。实现方式是复用现有
`agent.AllowedTools` / `coordinator.buildTools()` 的过滤模式，只构建明确允许的工具集合；
`hooked_tool.go` 继续作为 PreToolUse audit/deny 的最后一道门。

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

初始分类表：

| Tool | M1 delegate | 原因 |
| --- | --- | --- |
| `view` | allow | workspace 内只读文件读取，仍受 path guard 和 deny-list 约束 |
| `grep` | allow | workspace 内只读搜索，结果需 redaction/deny-list |
| `glob` | allow | workspace 内路径枚举，仍不能泄露敏感文件内容 |
| `ls` | allow | workspace 内目录枚举 |
| `sourcegraph` | deny by default | 可能触发外部代码搜索/网络路径，等 M1 后按配置开放 |
| `fetch` / `agentic_fetch` | deny by default | 网络读取面过大，等 network policy 完成后开放 |
| `bash` | deny | 命令执行，不属于 read-only delegate |
| `edit` / `multiedit` / `write` / `download` | deny | 可写或落盘 |
| `job_output` / `job_kill` / `crush_logs` | deny | 可能泄露其他运行上下文或控制进程 |
| MCP tools / resources | deny by default | 需 actor-aware MCP policy |
| `todos` / `agent` | deny | 会改变协作结构或创建嵌套 delegated run |

路径规则：

- path 必须先解析为 canonical absolute path，再和 canonical workspace root 比较。
- path guard 必须在文件系统访问层执行，即 view/grep/glob/ls 真正 read/stat/list 前再次校验，
  不能只在 tool input parsing 层校验。
- symlink、Windows junction/reparse point resolve 后仍必须在 workspace root 内。
- Windows 路径比较 case-insensitive，并拒绝 alternate data stream 形式。
- 输入路径先做 URL/percent decode、Unicode normalization（NFC）和控制字符/null byte 拒绝。
- hard link 指向的敏感文件无法可靠从路径判断时，按 basename/path deny-list 继续拒绝，并在 M1 测试中覆盖。
- 拒绝 workspace 外路径时返回 structured denial。

## 敏感文件 deny-list

即使 read-only delegate 也不能读：

```text
.env
.env.*
credentials.json
credentials.*
secrets.*
*.key
*.pem
*.p8
*.p12
*.pfx
*.jks
id_rsa
id_ed25519
*.ssh/config
.ssh/id_*
.kube/config
.npmrc
.netrc
pip.conf
aws/credentials
gcloud/application_default_credentials.json
```

实现位置：

- view/grep/glob/ls 类工具路径校验后、文件读取/stat/list 前。
- deny-list 匹配 canonical path 和 canonical basename；大小写不敏感平台按 lower-case 比较。
- M1 safety tests 必须覆盖 symlink、junction/reparse point、percent-encoded path、Unicode 变体、
  null byte/control char、大小写变体和敏感文件别名。
- config 支持用户追加模式和逐路径 allow exception；默认模式不能被整体关闭，exception 必须写
  audit/debug reason。

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

M4 的 `PermissionBridge` 是现有 permission/hook 执行链的 team-aware wrapper，不是第二套
permission subsystem。工具仍通过 `permission.Service.Request` 进入审批；`hooked_tool.go`
仍先运行 PreToolUse hook，并且 hook deny/allow 仍由现有逻辑决定是否进入普通 permission flow。
Team 模式只在 member runtime wiring 时注入一个实现 `permission.Service` 的 bridge，用来读取
`ActorContext`、补全 team 归因、写 team 持久事实源和把 UI 决策映射回当前等待中的 tool call。

M4 实现流程：

```text
tool requests permission
  -> read ActorContext
  -> no team identity: delegate to existing permission service
  -> has team identity:
       check scoped grant
       create permission request row/audit/event
       publish waiting_permission
       show existing permission UI with team/member/task/run/tool/path extension
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

- `PermissionBridge` 不替代现有 `permission.Service`；它必须包裹或适配现有 service，让非 team
  session 行为完全不变。
- `hooked_tool.go` 仍是执行前最终 hook gate；team bridge 不能绕过 hook deny，也不能在 hook
  allow 时跳过 audit。
- bridge 只把 team 语义补进 request/decision/audit：`team_id`、`member_id`、`task_id`、
  `run_id`、`session_id`、`tool_call_id`、`tool_name`、resource 和 reason。
- 现有 `internal/ui/dialog/permissions.go` 是第一版 UI 基座；M4 只扩展展示字段和 action mapping，
  不做第二套完全独立弹窗。
- run canceled 后 pending request 转为 `canceled`。
- app 重启后无法恢复 channel 的 request 转为 `orphaned` 或重新投递 UI。
- response 晚到且 run/tool_call 已结束，不再生效，只写 audit。
- 默认超时后转为 `expired`，member blocked 并 report alternative。
- `allow once` 只 resolve 当前 tool call。
- `allow for task` 创建 team-scoped grant，并 resolve 当前 tool call；后续请求必须仍经过 bridge
  的 grant lookup，不能写入现有 permission service 的 session-wide persistent grant。
- `allow for session` 不在默认 UI 展示；如 M4 第一版保留高级入口，也必须写 team-scoped grant，
  不能调用现有 `GrantPersistent` 形成缺少 task/team 约束的全 session 记忆。

## Team permission fact source

Team permission 使用 team 专用持久表记录事实源，但这不是独立执行链。现有 permission service
继续提供工具等待、Grant/Deny resolve、hook approval 和非 team session 行为；team 表负责
team/member/task/run 归因、scoped grant、late response、orphaned recovery、snapshot 和 audit。
`PermissionBridge` 在存在 `ActorContext.TeamID` 时写 team 表，并把最终决策映射回现有
permission waiter。

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
- grant lookup 只能由 `PermissionBridge` 在 team request path 内使用；工具、UI、server/client
  不直接查询 grant 表来绕过 permission service。
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

## Tool policy 演进

M1 不新增新的工具策略 adapter，也不要求每个 tool 立刻暴露 metadata registry。M1 的实现必须足够简单：

```text
ActorContext
  -> build allowed tool name set: view, grep, glob, ls
  -> coordinator/buildTools filters by name
  -> path guard wraps filesystem-facing read/list/search
  -> hooked_tool.go PreToolUse remains final audit/deny gate
```

规则：

- 无论 delegate prompt 如何要求，M1 runner 只能拿到 allow-list 内工具。
- allow-list 之外的工具不能进入模型 tool schema，而不是等 tool call 发生后再拒绝。
- `sourcegraph` / `fetch` 这类“看似只读但有网络面”的工具默认不进入 allow-list。
- M3 teammate read-only runtime 继续复用同一 allow-list 和 path guard。
- M4 permission bridge 可以在此基础上引入更细的 tool metadata，但它是 M4+ 迁移项，不是 PR-1 阻塞项。

后续若引入 adapter，必须满足：

- 不替代 `hooked_tool.go`；hook 仍是执行前最终 audit/deny。
- 与现有 `AllowedTools` 配置兼容，不能出现两套互相绕过的工具过滤逻辑。
- adapter metadata 缺失的工具默认 deny 给 delegate/member。

## Bash 安全

M1-M3.5 不给 teammate bash 权限。M5 如要允许 patch verification bash，必须先覆盖：

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
