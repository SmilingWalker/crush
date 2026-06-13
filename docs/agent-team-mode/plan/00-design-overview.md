# Crush SubAgent → AgentTeam 整体设计方案

> 面向团队开发人员的技术设计文档
> 阅读时间：20 分钟

---

## 目录

1. [背景与目标](#一背景与目标)
2. [当前 agent tool 的架构债务](#二当前-agent-tool-的架构债务)
3. [递进架构：从单 Agent 到多 Agent 协作](#三递进架构从单-agent-到多-agent-协作)
4. [核心抽象：三个贯穿所有阶段的基础组件](#四核心抽象三个贯穿所有阶段的基础组件)
5. [数据流：一次完整的任务执行链路](#五数据流一次完整的任务执行链路)
6. [Agent 定义系统：配置化与可扩展性](#六agent-定义系统配置化与可扩展性)
7. [权限模型：多层安全边界](#七权限模型多层安全边界)
8. [里程碑路线图](#八里程碑路线图)

---

## 一、背景与目标

Crush 当前的 Agent 架构为单一 Coder Agent，通过 `agent` tool 创建临时子 Agent 执行一次性搜索任务。子 Agent 只有 5 个只读工具，同步阻塞执行，无状态管理，无协作能力。

本方案的目标是：**将 SubAgent 系统从"一次性工具调用"逐步演进为"多 Agent 协作平台"**。

```
现状：                          目标：

User ←→ Coder                   User ←→ Coder (Leader)
                                      │
                                 ┌────┼────┐
                                 ↓    ↓    ↓
                            Reviewer Searcher  Tester
                                 │    │    │
                                 └────┼────┘
                                      ↓
                              结构化结果聚合
```

各阶段交付的核心能力：

- **M1**：SubAgent 异步执行、可取消、可配置、有进度反馈。用户可在 `crush.json` 中定义自定义 Agent 类型。
- **M2**：并行 Delegate 调度。Leader 同时派出 N 个 SubAgent 执行只读搜索，结果自动聚合。
- **M3**：团队数据持久化。Team/Member/Task/Run/Event/Audit 六类领域模型落 SQLite，关程序不丢状态。
- **M4**：长期 Member 运行时。Member 拥有独立生命周期（idle→running→idle），通过 Mailbox 通信，由 Scheduler 调度。
- **M5**：权限审批系统。Member 的写操作通过 PermissionBridge 进入审批流，支持 scoped grant（call/task/session）。
- **M6**：Patch Artifact 写文件。Member 生成 Patch，Leader review unified diff 后 Apply/Reject，含冲突检测和 rollback。
- **M7**：高级运行时隔离。Worktree/Process/A2A 三种 RuntimeBackend。

---

## 二、当前 agent tool 的架构债务

在讨论新架构之前，必须先回答一个问题：**现有的 `agent` tool 为什么不能直接在上面改？** 答案是：它不是"功能少"，而是"结构长错了"。在错误结构上叠加功能只会加速崩塌。

### 2.1 当前实现回顾

当前 SubAgent 的核心代码在 `internal/agent/coordinator.go` 的 `runSubAgent()` 方法中，约 60 行：

```go
// coordinator.go:1096-1144
func (c *coordinator) runSubAgent(ctx context.Context, params subAgentParams) (fantasy.ToolResponse, error) {
    // 1. 创建子 Session
    session, _ := c.sessions.CreateTaskSession(ctx, agentToolSessionID, params.SessionID, "...")

    // 2. 调用 SessionAgent.Run —— 同步阻塞！
    result, err := params.Agent.Run(ctx, SessionAgentCall{
        SessionID: session.ID, Prompt: params.Prompt, ...
    })

    // 3. 成本回传
    c.updateParentSessionCost(ctx, session.ID, params.SessionID)

    // 4. 返回纯文本结果
    return fantasy.NewTextResponse(result.Response.Content.Text()), nil
}
```

`subAgentParams` 结构体：

```go
type subAgentParams struct {
    Agent          SessionAgent
    SessionID      string
    AgentMessageID string
    ToolCallID     string
    Prompt         string
    SessionTitle   string
    SessionSetup   func(sessionID string)  // agentic_fetch 用来做 auto-approve
}
```

### 2.2 七个结构性问题

#### 问题一：Agent 定义完全硬编码，无法扩展

Agent 只有 2 种——`coder` 和 `task`，定义在 `SetupAgents()` 中，`Config.Agents` 字段标记了 `json:"-"`，配置文件无法定义新 Agent。

```go
// config.go:711-736
func (c *Config) SetupAgents() {
    agents := map[string]Agent{
        AgentCoder: {AllowedTools: allowedTools},
        AgentTask:  {AllowedTools: resolveReadOnlyTools(allowedTools)},
        // 就两个，写死的
    }
    c.Agents = agents  // json:"-" 导致永远不会从配置文件加载
}
```

**如果直接改**：需要在 `SetupAgents()` 里不断增加 `if/else`。每加一种 Agent 类型都要改核心代码。Agent 之间没有任何共性抽象。

#### 问题二：coordinator 是 god object，SubAgent 无权创建独立 runner

`runSubAgent` 是 `coordinator` 的方法，直接访问 `coordinator` 的所有私有字段（`sessions`、`permissions`、`cfg`、`currentAgent`）。`subAgentParams` 要求调用方传入一个预先构建好的 `SessionAgent` 实例——但 `SessionAgent` 的 `tools`、`models`、`messageQueue` 都是实例字段。当前 Task agent 和 Coder agent 共享同一个模型配置路径（都从 `config.SelectedModelTypeLarge` 取），所谓"独立"只是工具列表不同。

AgentTeam 方案的 M0.5 spike 的任务就是验证这一点：**能否创建真正独立的 `SessionAgent` 实例，确保 Coder 调 `SetTools` 不影响 Task agent？** 如果不能，整个 `runSubAgent` 模式需要重构。

#### 问题三：同步阻塞，无并发原语

```go
// 同步执行：coordinator 的 handler goroutine 被阻塞
result, err := params.Agent.Run(ctx, SessionAgentCall{...})
```

没有 goroutine、没有 channel、没有 `context.WithCancel`。返回的 `ToolResponse` 是纯文本——没有 run ID、没有状态查询接口、没有取消句柄。

**如果直接改**：需要给 `coordinator` 加 `activeSubAgents map`、加 `Cancel` 遍历逻辑、改 `CancelAll`。coordinator 的职责继续膨胀。

#### 问题四：没有执行者身份（ActorContext）

在 `runSubAgent` 中创建的子 Session 有 `ParentSessionID`，但没有把"谁启动了子 Agent"这个信息注入 tools 调用链。当子 Agent 调用 `view` 工具时，permission 系统能拿到 `SessionID` 和 `ToolCallID`，但拿不到"这个子 Agent 是从 Leader 的哪个 tool call 派出来的"这一层归因。

AgentTeam 方案基于这个洞察，设计了独立的 `internal/actor` 包，放在 `context.Context` 中传递，避免底层工具反向依赖上层领域。

#### 问题五：没有工具策略抽象

当前的工具过滤通过硬编码的 `AllowedTools` 字符串数组实现，配合 `resolveReadOnlyTools()` 写死了"只读=5个工具"。没有 `DisallowedTools`（per-agent 黑名单），没有 `ToolPolicy`（允许哪些 action），没有全局 `SubAgentDisallowedTools`（所有子 Agent 都不能用的工具列表）。

```go
// config.go:693-697
func resolveReadOnlyTools(tools []string) []string {
    readOnlyTools := []string{"glob", "grep", "ls", "sourcegraph", "view"}  // 硬编码
    return filterSlice(tools, readOnlyTools, true)
}
```

**如果直接改**：给 Agent 结构体加字段 → 但因为 `json:"-"` 无法从配置文件加载 → 只能改 `SetupAgents()` → 继续硬编码。

#### 问题六：Agent 类型和 Session 类型绑定过死

`agent_tool.go` 中写死了使用 `config.AgentTask` 配置：

```go
agentCfg, ok := c.cfg.Config().Agents[config.AgentTask]
```

`agentic_fetch_tool.go` 更粗暴——直接手工构建了一个全新的 `SessionAgent` 实例，工具列表硬编码在方法里：

```go
fetchTools := []fantasy.AgentTool{
    webFetchTool, webSearchTool,
    tools.NewGlobTool(...), tools.NewGrepTool(...),
    tools.NewSourcegraphTool(...), tools.NewViewTool(...),
}
```

**如果直接改**：两种完全不同的构建路径（`agentTool` 用配置文件、`agenticFetchTool` 硬编码）。每加一种新 Agent 工具，不知道该走哪条路。

#### 问题七：没有状态管理和持久化

子 Agent 完成后，创建的子 Session 标记了 `ParentSessionID`，但在 `ListSessions` 的 SQL 查询中被过滤掉了（`WHERE parent_session_id IS NULL`）。子 Agent 的执行历史只能通过主 Session 的 messages 链追溯，没有独立的 Run 记录——没有执行次数、没有 cost 归因到具体的 task、没有 heartbeat、没有崩溃恢复能力。

AgentTeam M3 引入的 `team_runs` 表就是为了解决这个问题：每次 Agent turn 执行都有一条独立的 Run 记录，包含 tokens、cost、usage_status（final/partial/unknown）、heartbeat、error。

### 2.3 为什么不能"先修修补补"？

| 修补方案 | 为什么不可行 |
|---------|------------|
| 给 Agent 结构体加字段 | `json:"-"` 导致配置无法持久化，必须先删标签；删标签后 `SetupAgents()` 的硬编码逻辑需要改为合并模式；合并模式需要定义"哪些字段支持覆盖"的规则 |
| 给 `runSubAgent` 加 goroutine | coordinator 没有管理多个异步 goroutine 的基础设施（无 active map、无 cancel 遍历、无 channel 状态回传）；加这些会让coordinator 膨胀为更大的 god object |
| 给子 Agent 加 bash 权限 | 没有全局黑名单机制，加 bash 等于给所有子 Agent 加——包括将来可能递归创建的子 Agent。必须先有安全底座再放工具 |
| 加 Per-Agent 权限模式 | 当前没有 `ActorContext`，permission 系统不知道是哪个 Agent 在请求——显示不了 Agent 类型/任务/归属信息 |

每个"小修补"都依赖另一个还没做的"小修补"，形成依赖链。而这条依赖链的前几项恰好就是 M1 地基。

### 2.4 哪些代码可以直接复用

不是全部重写。以下现有组件保持不变，直接作为新架构的底层：

| 现有组件 | 在新架构中的位置 | 变化 |
|---------|----------------|------|
| `SessionAgent.Run()` | 被 `TurnRunner` 接口封装后调用 | 调用方式从 `coordinator` 直接调变为通过 `AgentFactory` 创建的独立实例调 |
| `SessionAgent.messageQueue` | 保留用于同一 Session 的并发排队 | **不**作为 team mailbox 或 run lifecycle 来源（AgentTeam 决策 7b） |
| `permission.Service` | 被 `PermissionBridge` 包装 | 非 team session 走原路径；team session 走 bridge 扩展 |
| `hooked_tool.go` | 保留为执行前最终 gate | 增加 team audit 写入（M5-09） |
| `messages` 表 | 保留为 LLM turn 的对话记录 | M3 `team_runs` 表通过 `session_id` 关联 |
| `sessions` 表 | 保留 child session 机制 | M3 `team_session_links` 提供 team 维度的 session 检索 |
| `pubsub` 事件系统 | 新增 `PayloadTypeAgentProgress`、`PayloadTypeTeamEvent` | 只做通知，不做事实源 |

### 2.5 递进方案的设计动机

正因为上述问题形成了一个"先有鸡还是先有蛋"的循环（要加 bash 需要黑名单 → 黑名单需要 Agent 配置 → Agent 配置需要删 `json:"-"` → 删标签需要改 `SetupAgents` → 改 `SetupAgents` 需要 AgentFactory → AgentFactory 需要 TurnRunner 接口 → TurnRunner 需要 ActorContext），所以 M1 的设计是把整条依赖链上的**所有基础设施一次性建好**，然后 M2-M7 每层只做增量。

---

## 三、递进架构：从单 Agent 到多 Agent 协作

我们采用分层递进策略，而非一次性构建完整团队系统。每一层建立在上一层的基础设施之上，只做增量。

### M1：单 Agent 执行基座

建立 SubAgent 执行所需的基础设施。Agent 异步执行、可取消、进度可观测。提供 Agent 配置系统，用户可在配置文件中定义自定义 Agent 类型。

关键产出：
- `ActorContext`：统一的执行者身份标识
- `AgentFactory`：创建隔离的 `SessionAgent` 实例
- `TurnRunner`：Agent turn 执行的标准接口
- Agent 配置序列化 + 内置 4 种 Agent 类型
- 全局安全黑名单 + 递归深度防护

### M2：并行 Delegate 调度

在 M1 的 `TurnRunner` 接口之上，增加并行调度层。`DelegateRunner` 管理一组并行执行的 SubAgent，统一 cancel、统一聚合结果。

M2 的核心增量：并行调度 + cancel group + Delegate UI。`DelegateRunner.RunGroup()` 仅约 80 行，其余全部复用 M1。

### M3：团队数据持久化

将"团队"概念从内存提升为持久化领域模型。7 张 SQLite 表 + sqlc queries + TeamService + REST API + SSE 事件流。M3 与 M2 可并行开发（独立数据层）。

### M4：长期 Member 运行时

在 M1 的 `TurnRunner` 和 M3 的 DB 之上，增加 Member 生命周期管理。`MemberRunner` 是一个长期运行的 goroutine loop，通过 Mailbox 接收消息、通过 Scheduler 获取任务、通过 `TurnRunner.Run()` 执行。

Member 状态机：`created → starting → idle ⇄ running → idle（↔ blocked/waiting_permission/canceling_turn）→ shutting_down → stopped`

M4 的核心增量：状态机 + mailbox + scheduler + PromptEnvelope + shutdown/recovery。`MemberRunner` 内部的核心执行仍是调用 M1 的 `TurnRunner.Run()`。

### M5：权限审批

在现有 `permission.Service` 之上增加 team-aware wrapper（`PermissionBridge`），不创建第二套权限系统。Member 的写工具调用通过 bridge 进入审批流，UI 展示完整的 team/member/task/run/tool/path 归因信息。

### M6：安全文件写入

Member 不直接写 workspace。它生成 `PatchArtifact`（含 base_hash、touched_files、opaque content_ref），Leader 通过 unified diff viewer 审查后 Apply/Reject。Apply Service 执行 6 步安全写入流程：base hash check → pre-write recheck → per-path lock → temp file + atomic rename → rollback on failure。

### M7：高级运行时隔离

通过 `RuntimeBackend` 接口适配三种执行环境：InProcess（M4 默认）、Worktree（git worktree 隔离）、Process（独立进程 JSON-RPC IPC）。A2A Gateway 预留外部 Agent 协议映射。

### 递进关系总览

```
M1: TurnRunner + ActorContext + AgentFactory + Agent配置
 │
 ├─→ M2: DelegateRunner（+并行调度）
 │    每个 delegate 通过 TurnRunner.Run() 执行
 │
 ├─→ M3: 团队 DB（+持久化）
 │    独立数据层，可与 M2 并行
 │
 ├─→ M4: MemberRunner（+生命周期+mailbox）
 │    每个 turn 通过 TurnRunner.Run() 执行
 │
 ├─→ M5: PermissionBridge（+权限审批）
 │    包装现有 permission.Service
 │
 ├─→ M6: Patch + Apply（+安全写文件）
 │    依赖 M5 的权限桥
 │
 └─→ M7: RuntimeBackend（+隔离执行）
      远期规划
```

---

## 四、核心抽象：三个贯穿所有阶段的基础组件

### 3.1 ActorContext：统一的执行者身份

`ActorContext` 是贯穿 tools、hooks、permission、audit 的统一身份标识。它存储在 Go 的 `context.Context` 中，所有函数通过 `actor.FromContext(ctx)` 获取当前执行者身份。

```go
// internal/actor/actor.go
type ActorContext struct {
    // M1 核心字段
    SessionID       string
    ParentSessionID string
    MessageID       string
    ToolCallID      string
    WorkspaceID     string

    // M2+ 逐步填充
    TeamID     string
    MemberID   string
    MemberName string
    TaskID     string
    RunID      string
}
```

使用场景：
- **Tool 执行**：`hooked_tool.go` 从 context 提取 ActorContext，判断是否为 team session，决定是否写 team audit
- **Permission 审批**：UI 从 ActorContext 读取 member/task/run 信息，展示完整的执行者归因
- **Audit 记录**：所有 audit entry 包含 ActorContext 完整字段，支持按 team/member/task/run 维度查询

设计约束：`internal/actor` 不依赖 `internal/team`，避免底层工具反向依赖上层领域。

### 3.2 TurnRunner：Agent turn 执行接口

`TurnRunner` 是 Agent 执行的标准接口。不管是一次性 SubAgent（M1）、并行 Delegate（M2）、还是长期 Member（M4），执行一轮 LLM turn 的方式完全相同。

```go
// internal/agent/team_call.go
type TurnRunner interface {
    Run(ctx context.Context, call TeamAgentCall) (TurnRunResult, error)
    Cancel(sessionID string)
    IsSessionBusy(sessionID string) bool
}

type TeamAgentCall struct {
    SessionID       string
    ParentSessionID string
    PromptEnvelope  string
    Actor           actor.ActorContext
    ToolPolicy      ToolPolicyProfile
}

type TurnRunResult struct {
    Status TurnStatus  // completed | queued | canceled | failed
    Result *fantasy.AgentResult
}
```

各阶段使用方式：

```go
// M1: 单次同步/异步执行
runner := factory.BuildRunner(ctx, spec)
result, _ := runner.Run(ctx, call)

// M2: 并行调度
for _, task := range tasks {
    go func() {
        runner := factory.BuildRunner(ctx, spec)
        results <- runner.Run(ctx, call)
    }()
}

// M4: 长期 loop
for {
    msg := <-mailbox
    runner.Run(ctx, buildCall(msg))
    updateMemberState(Idle)
}
```

### 3.3 AgentFactory：独立的 SessionAgent 实例管理

```go
type AgentFactory interface {
    BuildRunner(ctx context.Context, spec AgentSpec) (TurnRunner, error)
}
```

关键约束（已冻结为架构决策）：**每个 `BuildRunner` 调用必须创建独立 `SessionAgent` 实例，不复用 `Coordinator.currentAgent`。** `SessionAgent` 内部的 `tools`、`models`、`messageQueue`、`activeRequests` 均为实例字段——共享实例会导致工具/模型策略、消息队列和 cancel 状态互相污染。

---

## 五、数据流：一次完整的任务执行链路

以 M4 为例，分析 Leader 派发任务到 Member 完成汇报的完整数据流：

### 4.1 执行流程

```
① Leader 发出指令
   → team_create_task tool 调用
   → TeamService.CreateTask
   → DB transaction: INSERT team_tasks + INSERT team_events + INSERT team_audit_events
   → SSE 广播 PayloadTypeTeamEvent

② Scheduler 分配任务
   → ClaimNextTask (CTE atomic SQL)
   → 更新 task assignee → 写入 event
   → 唤醒对应 MemberRunner (wakeCh ← WakeSourceTask)

③ MemberRunner 状态转换
   → idle → queued → running
   → 每次转换写 team_members CAS + team_events + audit

④ PromptEnvelope 组装
   → 固定 section 顺序（9 层）：
     1. System/Tool Policy (不可截断)
     2. Member Identity
     3. Task Full Text (不可截断)
     4. Direct Unread Messages [UNTRUSTED PEER INPUT]
     5. Dependency Result Summaries
     6. Leader Latest Instruction
     7. Broadcast/Role Messages [UNTRUSTED PEER INPUT]
     8. Session Summary
     9. Reporting Rules (不可截断)
   → 低优先级 section 按需截断, 插入 [truncated] 标记

⑤ TurnRunner.Run() 执行
   → SessionAgent.Run() → LLM stream + tool loop
   → 每次 tool call 通过 messages 表记录
   → 写权限操作触发 PermissionBridge

⑥ Member 完成
   → 调用 team_report_status 更新 task
   → 调用 team_send_message 通知 Leader
   → 状态: running → idle

⑦ Run 记录写入
   → INSERT/UPDATE team_runs (status, tokens, cost, usage_status)
   → INSERT team_events (event_type="run.completed")
   → 更新 team_members cost_so_far
   → SSE 广播

⑧ Leader 收到通知
   → 新消息作为 user-role message 注入 Leader session
   → UI 显示 team event 更新
```

### 4.2 数据存储

所有团队状态以 SQLite 为事实源，SSE/pubsub 仅用于通知。状态恢复通过 snapshot + event replay，不依赖事件流。

```
┌─────────────────────────────────────────────┐
│ SQLite                                       │
│                                              │
│ M3 Core (7 tables):                          │
│   teams             团队基本信息               │
│   team_members      队员信息 + 状态 + cost     │
│   team_tasks        任务信息 + version (CAS)  │
│   team_runs         执行记录 + usage_status    │
│   team_events       事件流 (per-team seq)     │
│   team_event_counters 事件序号生成器           │
│   team_audit_events 审计日志                  │
│                                              │
│ M4 Additions:                                │
│   team_session_links      Member↔Session 关联 │
│   team_mailbox_messages   队员邮箱            │
│   team_message_receipts   消息回执            │
│   team_task_dependencies  任务依赖关系         │
│   team_artifacts          Patch/Proposal     │
│                                              │
│ M5 Additions:                                │
│   team_permission_requests  权限申请          │
│   team_permission_grants    授权记录          │
│                                              │
│ M6 Additions:                                │
│   team_apply_conflicts      补丁冲突          │
└─────────────────────────────────────────────┘
```

---

## 六、Agent 定义系统：配置化与可扩展性

### 5.1 Agent 结构体

```go
// internal/config/config.go
type Agent struct {
    ID              string                `json:"id"`
    Name            string                `json:"name"`
    Description     string                `json:"description"`
    Model           SelectedModelType     `json:"model"`         // large | small
    AllowedTools    []string              `json:"allowed_tools"`
    DisallowedTools []string              `json:"disallowed_tools,omitempty"`
    SystemPrompt    string                `json:"system_prompt,omitempty"`
    PermissionMode  string                `json:"permission_mode,omitempty"` // default | acceptEdits | plan | bypassPermissions
    AllowedMCP      map[string][]string   `json:"allowed_mcp,omitempty"`
    ContextPaths    []string              `json:"context_paths,omitempty"`
    Disabled        bool                  `json:"disabled,omitempty"`

    // M2+ 预留字段
    MaxTurns        int                   `json:"max_turns,omitempty"`
    Skills          []string              `json:"skills,omitempty"`
    McpServers      []string              `json:"mcp_servers,omitempty"`
}
```

### 5.2 内置 Agent 类型

| ID | 名称 | 工具集 | 模型 | 用途 |
|----|------|--------|------|------|
| `coder` | Coder | 全部（24 个工具） | large | 主 Agent |
| `general-purpose` | General Purpose | view/grep/glob/ls/bash/write | large | 通用子 Agent |
| `explore` | Explore | view/grep/glob/ls/sourcegraph | small | 快速只读搜索 |
| `plan` | Plan | view/grep/glob/ls | small | 只读规划设计 |

### 5.3 用户自定义配置

在 `crush.json` 中通过 `agents` 字段覆盖内置配置或新增 Agent 类型：

```json
{
  "agents": {
    "code-reviewer": {
      "name": "Code Reviewer",
      "description": "Review code for security vulnerabilities and bad practices",
      "model": "small",
      "allowed_tools": ["view", "grep", "glob", "bash"],
      "disallowed_tools": ["write", "edit", "agent", "ask_user_questions"],
      "system_prompt": "You are a code security reviewer. When reviewing code, focus on: SQL injection, XSS, authorization bypass, sensitive data exposure. Output a concise report with risk levels.",
      "permission_mode": "acceptEdits"
    }
  }
}
```

`SetupAgents()` 执行合并逻辑：内置 Agent 先写入 map → 用户配置中同 key 的非零值字段覆盖 → 新增 key 直接追加。用户未配置时系统行为不变。

### 5.4 安全边界

三层过滤机制，在 `buildTools()` 中按顺序应用：

1. **全局黑名单**（`SubAgentDisallowedTools`）：所有 isSubAgent=true 的 Agent 强制禁用 `agent`、`ask_user_questions`、`job_output`、`job_kill`、`todos`、`crush_info`、`crush_logs`
2. **Per-Agent 黑名单**（`Agent.DisallowedTools`）：用户在配置中额外禁止的工具
3. **递归深度防护**：Agent 嵌套调用通过 context 传递深度计数器，硬上限=3

主 Agent（coder, isSubAgent=false）不受以上限制。

---

## 七、权限模型：多层安全边界

### 6.1 权限检查链路

工具执行时的权限检查链路（从外到内）：

```
Agent.DisallowedTools 过滤
  → Agent.AllowedTools 过滤
    → 全局 SubAgentDisallowedTools 过滤
      → hooked_tool.go PreToolUse Hook
        → PermissionBridge (team session)
          → 现有 permission.Service (非 team session)
            → UI 审批 / Hook 预批准 / 持久化授权 / 全局白名单
```

### 6.2 PermissionBridge（M5）

`PermissionBridge` 实现 `permission.Service` 接口，作为现有权限系统的 team-aware wrapper。不创建独立的权限子系统。

```go
func (b *PermissionBridge) Request(ctx context.Context, opts permission.CreatePermissionRequest) (bool, error) {
    ac, hasTeam := actor.FromContext(ctx)
    if !hasTeam || ac.TeamID == "" {
        return b.inner.Request(ctx, opts)  // 非 team session：原有流程
    }

    // 1. 检查现有 scoped grant
    if grant := b.grantStore.FindActiveGrant(ctx, ac, opts); grant != nil {
        b.audit(ctx, "grant_auto")
        return true, nil
    }

    // 2. 创建 team_permission_requests row
    // 3. 写 audit
    // 4. 发布 waiting_permission 事件
    // 5. 等待 UI 决策
    return b.waitDecision(ctx, req.ID)
}
```

约束（不可违反）：
- 非 team session 行为完全不变
- `hooked_tool.go` 仍是执行前最终 hook gate
- Hook deny 阻止执行 + 写 audit
- Hook allow 写 team audit
- `allow for task` 创建 team-scoped grant，**不调用** `GrantPersistent` 创建 session-wide grant

### 6.3 Scoped Grant

三种授权范围，scope 越宽越持久：

| Scope | 含义 | 影响范围 | 持久化 |
|-------|------|---------|--------|
| `call` | 单次工具调用 | 当前 tool call | 不创建 grant |
| `task` | 当前任务 | 同一 task 内同 tool+action+resource | 创建 `team_permission_grants` row |
| `session` | 当前 session | 同一 member session 内 | 创建 grant（不在默认 UI 展示） |

### 6.4 权限状态机

```
pending ──→ allowed   (用户批准, scope=call → 无 grant; scope=task → 创建 grant)
       ──→ denied    (用户拒绝, member → blocked)
       ──→ expired   (TTL=5min, member → blocked)
       ──→ canceled  (run cancel 级联)
       ──→ orphaned  (app restart 恢复, 原 wait channel 不可恢复)
```

Late response 处理：run 已结束时收到的权限决策只写 audit，不创建 grant，不 resolve tool call。

---

## 八、里程碑路线图

```
M1  ████████░░  单兵地基        8任务   9d   3-4周   基础设施
M2  ██████░░░░  并行搜索队      6任务   6d   +2周   并行调度
M3  ██████████  团队数据库      9任务  12d   +3周   持久化
M4  ██████████  长期队员       13任务  25d   +6周   状态机+mailbox
    ██████
M5  ████████░░  权限审批       10任务  16d   +4周   PermissionBridge
M6  ████████░░  安全补丁       10任务 14.5d  +4周   Patch+Apply
M7  ████░░░░░░  高级隔离        6任务  6.5d  远期    Worktree/Process/A2A
────────────────────────────────────────────────────────
                总计            62任务  89d  ~6个月
```

| 里程碑 | 核心交付 | 依赖 | 关键风险 |
|--------|---------|------|---------|
| M1 | ActorContext, AgentFactory, TurnRunner, Agent配置, 异步执行, 全局黑名单 | 无 | — |
| M2 | DelegateRunner, DelegateRunGroup, 结果聚合, Delegate UI | M1 | — |
| M3 | 7 DB tables, TeamService, TeamWorkspace, API routes, Debug UI | M1 | — |
| M4 | MemberRunner, TeamRunner, Mailbox, Scheduler, PromptEnvelope, 状态机 | M1, M3 | SessionAgent 实例隔离, CancelMemberTurn 并发竞争 |
| M5 | PermissionBridge, Scoped Grant, Team Permission UI, Audit | M4 | 与现有 permission.Service 集成复杂度 |
| M6 | PatchArtifact, ContentStore, Apply Service, Patch Review UI | M5 | Apply rollback 可能失败导致 workspace 部分修改 |
| M7 | RuntimeBackend, Worktree, Process, A2A Gateway | M6 | 远期接口可能随 M4-M6 调整 |

### 关键风险

| 风险 | 位置 | 等级 | 缓解措施 |
|------|------|------|---------|
| SessionAgent 实例共享导致 tools/models 污染 | M0.5 spike | 🔴 | M0.5 必须验证隔离可行性，不通过不进 M1 |
| CancelMemberTurn 中 cancel/late result/heartbeat 并发竞争 terminal state | M4-02 | 🔴 | 单一 writer (MemberRunner) + version CAS guard |
| Apply Service rollback 中间失败 | M6-04 | 🔴 | Best-effort rollback + conflict artifact + audit |
| PermissionBridge 与现有 permission.Service 接口不匹配 | M5-01 | 🟡 | Bridge 包裹不替代内部状态机 |
| SQLite atomic claim 在并发下非严格 atomic | M4-03 | 🟡 | CTE WITH 子句 + 单用户场景并发低 |

---

## 附录：关键文件速查

```
internal/
├── actor/actor.go                    M1: ActorContext 定义
├── agent/
│   ├── team_call.go                  M1: TurnRunner + AgentFactory 接口
│   ├── agent_tool.go                 M1: SubAgent 入口 + 递归深度防护
│   ├── agent_result.go               M1: AgentToolResult 结构化返回
│   ├── progress.go                   M1: pubsub 进度事件
│   ├── coordinator.go                M1: 异步执行 + Agent Kill
│   └── tools/disallowed.go           M1: 全局黑名单
├── team/
│   ├── delegate_runner.go            M2: DelegateRunner + RunGroup
│   ├── delegate_types.go             M2: DelegateTask/Result/Group
│   ├── models.go                     M3: 领域模型 + Status 枚举
│   ├── store_*.go                    M3: 6 个 Store 实现
│   ├── service.go                    M3: TeamService facade
│   ├── member_runner.go              M4: MemberRunner 状态机 + loop
│   ├── runner.go                     M4: TeamRunner 生命周期
│   ├── scheduler.go                  M4: Claim/Wake/Heartbeat
│   ├── mailbox.go                    M4: Mailbox + Receipt
│   ├── prompt_builder.go             M4: PromptEnvelope 组装
│   ├── shutdown.go                   M4: 9-step shutdown sequence
│   ├── recovery.go                   M4: Stale run recovery
│   ├── cost.go                       M4: Partial cost accounting
│   ├── permission_bridge.go          M5: Team-aware permission wrapper
│   ├── permission_state.go           M5: Scoped Grant FSM
│   ├── permission_queue.go           M5: Queue + Timeout
│   ├── audit.go                      M5: 审计覆盖
│   ├── artifact_patch.go             M6: PatchArtifact schema
│   ├── content_store.go              M6: ContentStore 接口
│   ├── blob_store.go                 M6: Filesystem blob 实现
│   ├── apply_patch.go                M6: 6-step apply + rollback
│   └── runtime_adapter.go            M7: RuntimeBackend 接口
├── config/config.go                  M1: Agent 结构体 + SetupAgents
├── db/
│   ├── migrations/                   M3-M6: 数据库迁移文件
│   └── sql/team_queries.sql          M3: sqlc queries
└── ui/
    ├── chat/delegate.go              M2: Delegate 紧凑/展开 UI
    ├── chat/team.go                  M4: Compact Team Item UI
    └── dialog/patch_review.go        M6: Patch review/apply UI
```
