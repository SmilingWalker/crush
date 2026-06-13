# Crush SubAgent 差距分析与递进建设方案

> **最终裁决文档 v2.0**
>
> 分析日期：2026-06-04 / 更新：2026-06-04
> 分析方法：3 位架构师 → 2 位技术负责人对抗评审 → Team Lead 裁决 → 与 AgentTeam 方案对齐
> 对标基准：Claude Code (`E:\workspace\remote-github\claude-code-home-version`)
> 关联方案：`docs/agent-team-mode/`（已有 M0-M6 技术方案）

---

## 一、方案关系

本方案与 AgentTeam 方案是**递进关系**，不是两条平行线：

```
M1: SubAgent 单兵地基（本文档定义）
 │   ActorContext / AgentFactory / TurnRunner / Agent配置
 │
 ├─→ AgentTeam M2: 并行搜索队（原 AgentTeam M1）
 │    用 M1 的工厂、执行接口、配置，只加并行调度
 │
 └─→ AgentTeam M4: 长期队员（原 AgentTeam M3）
      用 M1 的工厂、执行接口、配置，只加 mailbox + scheduler
```

M1 是地基，后续每个 AgentTeam 里程碑只做"增量"。详见 `01-progressive-architecture.md`。

---

## 二、当前状态评估

### 综合评分

```
维度             Crush    Claude Code   差距
─────────────────────────────────────────────
Agent定义/配置     2/10       9/10       -70%
工具集/安全        3/10       9/10       -60%
执行引擎/生命周期   2/10       9/10       -70%
UI渲染/UX          1/10       9/10       -80%
─────────────────────────────────────────────
综合               8/40      36/40       -70%
```

### Crush 当前 SubAgent 架构

```
主Agent (Coder)                子Agent (Task/Fetch)
┌──────────────────┐          ┌──────────────────────┐
│ 24个工具全部权限   │ ──同步──▶│ 5个只读工具            │
│ 全部MCP           │  阻塞    │ 无MCP                 │
│ 全部Hooks         │          │ 无异步/无隔离/无Kill   │
└──────────────────┘          │ 无进度/无配置/无身份    │
                              └──────────────────────┘
```

关键代码位置：
- Agent 定义：`internal/config/config.go:496-517`（2个硬编码，`json:"-"` 不可序列化）
- 工具过滤：`internal/agent/coordinator.go:548-553`（单一白名单）
- 子Agent执行：`internal/agent/coordinator.go:1096-1144`（`runSubAgent` 同步阻塞）
- UI渲染：`internal/ui/chat/agent.go`（仅 spinner + 树形结构）

### 已澄清的误区

| 误区 | 实际情况 |
|------|----------|
| "无递归防护" | `agent.go:461-486` 有 `hasRepeatedToolCalls()` + SHA-256 循环检测（窗口=10，最大重复=5），但不等于递归深度防护 |
| "子Agent不能写文件是缺陷" | `resolveReadOnlyTools()` 是有意的安全设计，但只读限制了实用性 |
| "工具过滤是单层" | 实际有 AllowedTools + MCP allow-list 两层 |
| "权限系统简陋" | 有 5 种机制（全局跳过、白名单、session自动批准、Hook预批准、持久化），但缺 per-agent 模式 |

---

## 三、M1 完整定义：SubAgent 单兵地基

M1 是后续所有 AgentTeam 里程碑的地基层。它产出的是**可复用的基础设施**，而不是一次性功能。

### 3.1 M1 总览

```
M1 四个模块                    谁在用
──────────────────────────────────────────────
ActorContext 身份系统            M2/M3/M4/M5 全部
AgentFactory 工厂                M2 DelegateRunner、M4 MemberRunner
TurnRunner 执行接口              M2/M3/M4/M5 全部
ToolPolicy 工具策略              M2/M3/M4 复用
Agent 配置系统（可定义+可配置）    M2/M3/M4 复用
全局黑名单                      全部
异步执行 + 取消 + 进度回调       全部
结构化返回结果                   全部
```

### 3.2 安全底座

#### 全局禁止列表

新建 `internal/agent/tools/disallowed.go`：

```go
// SubAgentDisallowedTools 是所有子Agent永远不能使用的工具
var SubAgentDisallowedTools = []string{
    "agent",              // 防递归（Agent 调用 Agent）
    "ask_user_questions", // 子Agent不能向用户提问
    "job_output",         // 不能读取其他任务输出
    "job_kill",           // 不能 Kill 其他任务
    "todos",              // 不需要任务管理
    "crush_info",         // 不需要 Crush 自身信息
    "crush_logs",         // 不需要访问日志
}
```

在 `buildTools()` 中，`isSubAgent=true` 时强制过滤。

#### 递归深度防护

在 `agent_tool.go` 中通过 context 注入递归计数器，硬编码上限=3：

```go
type recursionDepthKey struct{}

func withRecursionDepth(ctx context.Context, depth int) context.Context {
    return context.WithValue(ctx, recursionDepthKey{}, depth)
}

func getRecursionDepth(ctx context.Context) int {
    v, _ := ctx.Value(recursionDepthKey{}).(int)
    return v
}

// agentTool handler 中：
if getRecursionDepth(ctx) >= 3 {
    return fantasy.NewTextErrorResponse("max recursion depth exceeded"), nil
}
ctx = withRecursionDepth(ctx, getRecursionDepth(ctx)+1)
```

#### ActorContext 身份系统

新建 `internal/actor/actor.go`：

```go
type ActorContext struct {
    SessionID       string
    ParentSessionID string
    MessageID       string
    ToolCallID      string
    WorkspaceID     string
    
    // M2+ 字段（M1 定义但暂不使用）
    TeamID     string
    MemberID   string
    MemberName string
    TaskID     string
    RunID      string
}
```

### 3.3 执行引擎

#### 异步执行

`runSubAgent` 支持同步/异步两种模式：

```go
type SubAgentMode int
const (
    SubAgentSync  SubAgentMode = iota  // 当前行为：阻塞等结果
    SubAgentAsync                       // 新增：goroutine 后台执行
)

type SubAgentParams struct {
    Prompt string
    Mode   SubAgentMode
    Agent  SessionAgent
    // ...
}

type SubAgentHandle struct {
    RunID      string
    StatusChan <-chan SubAgentStatus
    Cancel     context.CancelFunc
}

type SubAgentStatus struct {
    State    string  // "running" | "done" | "error" | "canceled"
    Progress string  // 最近一条进度消息
}
```

#### Agent Kill

`SubAgentHandle.Cancel()` → `context.CancelFunc` → 子Agent 的 `activeRequests` 被取消。复用现有 `Cancel(sessionID)` 基础设施。

#### 进度回调

通过 pubsub 发布 `AgentProgress` 事件：

```go
type AgentProgressEvent struct {
    RunID          string
    ToolUseCount   int
    LastToolName   string
    TokenCount     int
    ActivityDesc   string
    ElapsedMs      int64
}
```

#### 结构化返回结果

```go
type AgentToolResult struct {
    AgentID           string  `json:"agent_id"`
    AgentType         string  `json:"agent_type"`
    Content           string  `json:"content"`
    TotalTokens       int64   `json:"total_tokens"`
    TotalToolUseCount int     `json:"total_tool_use_count"`
    TotalDurationMs   int64   `json:"total_duration_ms"`
    Usage             Usage   `json:"usage"`
}
```

### 3.4 Agent 配置系统

#### Agent 结构体

```go
// internal/config/config.go

type Agent struct {
    // --- 已有字段 ---
    ID            string                `json:"id"`
    Name          string                `json:"name"`
    Description   string                `json:"description"`
    Disabled      bool                  `json:"disabled,omitempty"`
    Model         SelectedModelType     `json:"model"`
    AllowedTools  []string              `json:"allowed_tools"`
    AllowedMCP    map[string][]string   `json:"allowed_mcp,omitempty"`
    ContextPaths  []string              `json:"context_paths,omitempty"`

    // --- M1 新增字段 ---
    DisallowedTools []string            `json:"disallowed_tools,omitempty"`
    SystemPrompt    string              `json:"system_prompt,omitempty"`
    PermissionMode  string              `json:"permission_mode,omitempty"` // "default" | "acceptEdits" | "plan" | "bypassPermissions"

    // --- M2-M4 预留字段（M1 定义 schema，暂不使用）---
    MaxTurns        int                 `json:"max_turns,omitempty"`
    Skills          []string            `json:"skills,omitempty"`
    McpServers      []string            `json:"mcp_servers,omitempty"`
}
```

#### 配置可序列化

```go
// 改前
Agents map[string]Agent `json:"-"`

// 改后
Agents map[string]Agent `json:"agents"`
```

#### SetupAgents 合并模式

```go
func (c *Config) SetupAgents() {
    // 1. 内置默认
    builtins := map[string]Agent{
        "coder": {
            ID: "coder", Name: "Coder",
            Description: "主编码Agent，拥有全部工具",
            Model: SelectedModelTypeLarge,
            AllowedTools: allToolNames(),
        },
        "general-purpose": {
            ID: "general-purpose", Name: "General Purpose",
            Description: "通用子Agent，用于搜索、分析和多步骤任务",
            Model: SelectedModelTypeLarge,
            AllowedTools: []string{"view","grep","glob","ls","bash","write"},
            DisallowedTools: SubAgentDisallowedTools,
        },
        "explore": {
            ID: "explore", Name: "Explore",
            Description: "快速搜索Agent，适合文件查找和代码探索",
            Model: SelectedModelTypeSmall,
            AllowedTools: []string{"view","grep","glob","ls","sourcegraph"},
            DisallowedTools: SubAgentDisallowedTools,
        },
        "plan": {
            ID: "plan", Name: "Plan",
            Description: "规划设计Agent，只读，用于架构分析和方案设计",
            Model: SelectedModelTypeSmall,
            AllowedTools: []string{"view","grep","glob","ls"},
            DisallowedTools: SubAgentDisallowedTools,
            PermissionMode: "plan",
        },
    }

    // 2. 用户配置覆盖/新增
    for key, agent := range c.Agents {
        if existing, ok := builtins[key]; ok {
            // 覆盖内置Agent的字段
            if agent.AllowedTools != nil { existing.AllowedTools = agent.AllowedTools }
            if agent.DisallowedTools != nil { existing.DisallowedTools = agent.DisallowedTools }
            if agent.SystemPrompt != "" { existing.SystemPrompt = agent.SystemPrompt }
            if agent.Model != "" { existing.Model = agent.Model }
            if agent.PermissionMode != "" { existing.PermissionMode = agent.PermissionMode }
            builtins[key] = existing
        } else {
            // 新增用户定义的Agent
            builtins[key] = agent
        }
    }

    c.Agents = builtins
}
```

#### 配置文件示例

```json
{
  "agents": {
    "code-reviewer": {
      "name": "代码审查员",
      "description": "审查代码安全性，查找漏洞和不良实践",
      "model": "small",
      "allowed_tools": ["view", "grep", "glob", "bash"],
      "disallowed_tools": ["write", "edit", "agent"],
      "system_prompt": "你是代码安全审查专家。审查代码时关注：SQL注入、XSS、权限绕行、敏感信息泄露。",
      "permission_mode": "acceptEdits"
    }
  }
}
```

### 3.5 接口定义（M2-M7 依赖）

```go
// internal/agent/team_call.go

// TurnRunner 执行一轮 Agent turn 的标准接口
// M1 SubAgent / M2 Delegate / M4 Member 三层共用
type TurnRunner interface {
    Run(ctx context.Context, call TeamAgentCall) (TurnRunResult, error)
    Cancel(sessionID string)
    IsSessionBusy(sessionID string) bool
}

// AgentFactory 创建独立 SessionAgent 实例的工厂
// 每个 runner 持有独立实例，不复用 Coordinator.currentAgent
type AgentFactory interface {
    BuildRunner(ctx context.Context, spec AgentSpec) (TurnRunner, error)
}

// TeamAgentCall 调用描述符，不同于 SessionAgentCall
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

### 3.6 内置 Agent 从 2 个扩展到 4 个

| Agent | 工具集 | 模型 | 用途 |
|-------|--------|------|------|
| `coder` | 全部（主Agent） | large | 默认主Agent，不变 |
| `general-purpose` | view/grep/glob/ls/bash/write | large | 通用子Agent（替代原 task） |
| `explore` | view/grep/glob/ls/sourcegraph | small | 快速搜索 |
| `plan` | view/grep/glob/ls | small | 只读规划设计 |

---

## 四、完整递进路线（M1-M7）

```
M1: SubAgent 单兵地基（本文档定义）
 │  3-4 周
 │  产出：ActorContext / AgentFactory / TurnRunner / Agent配置
 │
 ├─→ M2: 并行搜索队（原 AgentTeam M1）
 │    3-4 周
 │    增量：DelegateRunner / DelegateRunGroup / Delegate UI
 │    复用：M1 的工厂、执行接口、工具策略、Agent 配置
 │
 ├─→ M3: 团队数据领域（原 AgentTeam M2）
 │    3-4 周
 │    增量：7 张 DB 表 / TeamService / TeamSnapshot
 │    复用：M1 的 ActorContext（补充 TeamID/MemberID 字段）
 │
 ├─→ M4: 长期队员运行（原 AgentTeam M3）
 │    4-6 周
 │    增量：MemberRunner / Mailbox / Scheduler / PromptEnvelope
 │    复用：M1 的 TurnRunner / AgentFactory / Agent 配置
 │
 ├─→ M5: 权限审批（原 AgentTeam M4）
 │    3-4 周
 │    增量：PermissionBridge / Scoped Grant / Audit
 │    复用：M1 的 ActorContext / ToolPolicy
 │
 ├─→ M6: 安全补丁（原 AgentTeam M5）
 │    4-5 周
 │    增量：Patch Artifact / ContentStore / Apply Service
 │    复用：M1 的执行接口、M5 的权限桥
 │
 └─→ M7: 高级隔离（原 AgentTeam M6）
      远期
      增量：Worktree Runtime / Process Backend / A2A Gateway
```

| 里程碑 | 用户能力 | 累计工期 | 核心增量 |
|--------|---------|----------|----------|
| M1 | 自定义Agent、异步执行、可取消、可配置 | 3-4 周 | 地基 |
| M2 | 并行派出3个搜索员 | 6-8 周 | +并行调度 |
| M3 | 创建团队，可查看状态 | 9-12 周 | +数据库 |
| M4 | 长期队员，可收消息跑任务 | 13-18 周 | +邮箱+调度 |
| M5 | 队员请求权限，你审批 | 16-22 周 | +权限桥 |
| M6 | 队员生成补丁，你审查应用 | 20-27 周 | +补丁系统 |
| M7 | 隔离工作区 | 远期 | +worktree |

---

## 五、明确放弃的功能

以下 Claude Code 功能针对 60M+ 多团队用户场景，Crush 作为单人终端工具**不需要**：

| 功能 | 放弃理由 |
|------|----------|
| 6源配置合并 | 2源（内置 + crush.json）足够 |
| ASYNC_AGENT_ALLOWED_TOOLS 独立白名单 | 同步/异步用同一套规则 |
| Fork/Remote 执行模式 | 企业多租户场景 |
| SendMessage/Team/Swarm | 单用户无多Agent协作需求 |
| EnterPlanMode/ExitPlanMode | Crush 无 Plan Mode 概念 |
| AgentSummary 独立持久化服务 | Session 级日志已覆盖 |
| BackgroundTask 6种类型 | 3种（Task/Fetch/Shell）足够 |
| Agent 定义管理 UI | JSON 配置是终端用户自然选择 |

---

## 六、代码落点总览

```
crush/
├── internal/
│   ├── actor/                        ← M1 新建
│   │   └── actor.go                  ActorContext 身份系统
│   │
│   ├── agent/
│   │   ├── team_call.go              ← M1 新建 TurnRunner + AgentFactory
│   │   ├── agent_tool.go             ← M1 重构 异步 + 结构化结果
│   │   ├── coordinator.go            ← M1 重构 runSubAgent
│   │   └── tools/
│   │       ├── disallowed.go         ← M1 新建 全局黑名单
│   │       └── policy.go             ← M1 新建 ToolPolicy
│   │
│   ├── team/                         ← M2-M6 新建（原 AgentTeam 方案）
│   │   ├── delegate_runner.go        M2
│   │   ├── runner.go                 M4
│   │   ├── member_runner.go          M4
│   │   ├── mailbox.go                M4
│   │   ├── service.go                M3
│   │   ├── permission_bridge.go      M5
│   │   ├── artifact_patch.go         M6
│   │   └── ...
│   │
│   ├── config/
│   │   └── config.go                 ← M1 修改 Agent 结构体 + 序列化
│   │
│   ├── db/
│   │   └── migrations/               ← M3 新建 7+ 张 team 表
│   │
│   └── ui/
│       └── chat/
│           ├── agent.go              ← M1 修改 进度 + 统计渲染
│           └── delegate.go           ← M2 新建 Delegate UI
```

---

## 七、风险评估

| 风险 | 概率 | 影响 | 缓解措施 |
|------|------|------|----------|
| Async goroutine 泄漏 | 中 | 内存泄漏 | `context.WithTimeout` + `defer cancel` |
| 配置向后兼容破坏 | 中 | 现有用户加载失败 | golden file 测试全覆盖 |
| `SessionAgent` 共享污染 | 中 | 工具/模型互相干扰 | `AgentFactory` 强制创建独立实例 |
| 写工具权限绕行 | 低 | 安全漏洞 | 双层过滤 + `PermissionMode` 检查 |
| Bubble Tea 性能 | 低 | 多 Agent 时帧率下降 | virtual list + 折叠默认展示 |

---

## 八、相关文档

| 文档 | 内容 |
|------|------|
| `01-progressive-architecture.md` | 递进架构详解（SubAgent → AgentTeam） |
| `docs/agent-team-mode/00-technical-design.md` | AgentTeam 全局技术设计 |
| `docs/agent-team-mode/01-architecture-principles.md` | 架构原则与模块边界 |
| `docs/agent-team-mode/05-runtime-control-plane.md` | Runtime 控制面 |
| `docs/agent-team-mode/07-safety-permission-audit.md` | 安全、权限与审计 |
| `docs/agent-team-mode/09-milestone-plan-m0-m6.md` | 里程碑计划 |
| `docs/agent-team-mode/11-implementation-plan.md` | 实施拆分与 PR 顺序 |
| `docs/agent-team-mode/12-open-architecture-issues.md` | 已冻结架构决策 |

---

> **文档版本**: v2.0
> **变更**: 与 AgentTeam 方案对齐，明确递进关系，补充 M1 Agent 配置系统
> **产出团队**: architect-a, architect-b, architect-c, tech-lead-a, tech-lead-b, team-lead
