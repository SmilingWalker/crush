# 05 C 线：Safety & Isolation Plane

C 线负责权限、审计、MCP/skills 可见性、读写隔离。它必须早于完整 team runtime，否则多 agent 会放大现有安全盲区。

## 1. M1 安全基础

M1 必须在 delegate 运行前就位的最小安全措施。

### 1.1 workspace-scoped read-only

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
- symlink TOCTOU 防护：`filepath.EvalSymlinks` + 即时校验 + open 后 fstat 验证。

### 1.2 敏感文件 deny-list

read-only 不是安全边界。`grep/view` 可以读取 `.env`、`credentials.json` 等敏感文件。

默认 deny 模式：

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
.ssh/config
```

匹配规则：

- 基于 filepath.Ext 和 filepath.Match。
- 路径 normalize 后匹配（resolve symlink + clean）。
- 匹配时拒绝读取，返回 "access denied: sensitive file"。
- deny-list 存储在 config，用户可追加或移除条目。

实现位置：

- `internal/agent/tools` 中 view/grep 的路径校验环节。
- 在 workspace scope 检查之后、文件读取之前执行。

### 1.3 MCP instructions 过滤

M1 delegate 默认不注入 MCP 上下文：

- 不注入 user 级 MCP server instructions。
- 不注入 global 级 MCP server instructions。
- 不暴露 MCP tools 给 delegate。
- 只有 project 级 MCP 且标记 `team_visible: true` 才注入。

实现位置：

- `buildTools` / `buildAgent` 中 MCP instructions 收集逻辑。
- 通过 `ToolBuildOptions.MCPPolicy` 控制。

## 2. ActorContext

M1 引入 skeleton，M2 全链路注入。

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

### 2.1 M1 ActorContext skeleton

M1 最小实现：

- `ActorContext` 结构体定义。
- `DelegateRunner` 创建 delegate 时填充 `AgentName`、`ParentSessionID`、`SessionID`。
- 日志输出包含 actor 信息（`zap.String("actor", ctx.AgentName)`）。
- 旧调用（非 team mode）`ActorContext` 为空值，不影响现有行为。

### 2.2 M2 全链路注入

M2 补全所有字段：

- `TeamID`、`MemberID`、`TaskID`、`RunID` 从 team DB 填充。
- permission request 包含 actor 信息。
- hook payload 包含 actor 信息。

## 3. Permission 扩展

当前 permission key 太粗。M3b 改为 scoped grant。

### 3.1 简化 scope（三级）

初期只实现三级 scope，不做 `agent`/`team`/`workspace`：

```text
call     -- 仅本次 tool call
task     -- 当前 task 的所有 run
session  -- 当前 session 内所有 tool call
```

实现要求：

- 默认 UI 不给 teammate 提供 session scope 快捷授权。
- `allow task` 不可扩散到另一个 task。
- grant 可过期。
- grant 带 constraints：tool/action/path-prefix/MCP server/tool/network host。

### 3.2 预留扩展

`agent`/`team`/`workspace` 三级 scope 预留在枚举中，但 M3b 不实现。

原因：

- 7 级 scope 增加实现复杂度和用户认知负担。
- 初期 teammate 数量有限，call/task/session 足够覆盖。
- 等用户反馈后再决定是否需要更粗粒度的授权。

## 4. Tool 四钩子

M3a 引入，为每个 tool 增加显式安全标记。

```go
type Tool interface {
    // ... 现有方法
    CheckPermissions(ctx context.Context, input any) (*PermissionResult, error)
    ValidateInput(ctx context.Context, input any) error
    IsReadOnly(input any) bool
    IsDestructive(input any) bool
}
```

用途：

- `IsReadOnly`：delegate 只能使用 `IsReadOnly=true` 的工具。
- `IsDestructive`：危险操作额外警告。
- `CheckPermissions`：统一的权限检查入口。
- `ValidateInput`：输入校验（路径、参数范围）。

M1 临时方案：使用白名单（view/grep/glob/ls），M3a 正式切换到四钩子。

## 5. ToolExecutionEvent

Permission event 不覆盖所有工具，因此 M3b 增加 tool audit。

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

### 5.1 hook allow 也写 audit

当前 `hook allow` 跳过 permission prompt，但不记录审计事件。M3b 要求：

- hook `allow` 也生成 `ToolExecutionEvent`（phase=`permission_resolved`, reason=`hook`）。
- `updated_input` 记录 input hash/diff summary。

## 6. Hooks

当前 hooks 只包 top-level agent。team mode 不应默认改变该行为。

实现要求：

- 增加 hook config：`include_team_members`、`include_subagents`、`actor_filter`。
- hook payload 增加 ActorContext。
- hook `allow` 也写 audit event。
- `updated_input` 记录 input hash/diff summary。

## 7. MCP/skills actor-aware

MCP：

- tool list 按 actor policy。
- server instructions 按 actor policy（M1 已实现 deny，M2 增加细粒度控制）。
- prompts/resources API 按 actor policy。
- teammate 默认不可见 user/global MCP。

Skills：

- teammate prompt 只注入允许 skill metadata。
- global user skills 默认不暴露给 teammate。
- `view crush://skills/...` 校验 actor 可见性。

## 8. Bash 安全分析（M4 前置条件）

teammate 获得 bash 权限前（M4+），必须补齐 bash 安全分析。

参考 Claude Code 的 `bashSecurity.ts`，需要覆盖：

- 危险命令黑名单：`rm -rf /`、`mkfs`、`dd`、`:(){:|:&};:` 等。
- 命令替换检测：`$(...)`、`` `...` ``。
- Heredoc 检测。
- Process substitution 检测：`<(...)`、`>(...)`。
- Zsh/PowerShell 扩展语法检测。
- Network 命令限制：`curl`、`wget` 目标白名单。
- 环境变量泄露防护：`export`、`env`、`printenv`。

M1-M3 不需要 bash 安全（delegate 没有 bash 权限），但 M4 patch artifact 可能涉及运行测试，M4 前必须补齐。

## 9. M4 patch artifact 安全写

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

## 10. 测试

必须测试：

- `grep` workspace 外路径被拒。
- symlink 指向 workspace 外被拒。
- symlink TOCTOU 场景（check 和 use 之间被替换）。
- delegate 不能读取 `.env` 等敏感文件。
- teammate 默认看不到 MCP instructions。
- teammate 默认看不到 global skill。
- permission request 显示 team/member/task/run。
- hook allow 也有 audit。
- patch base mismatch 不覆盖文件。
- deny-list 用户配置覆盖生效。
