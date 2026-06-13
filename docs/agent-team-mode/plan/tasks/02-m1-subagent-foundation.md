# M1: SubAgent 单兵地基 — 开发任务拆分

> 里程碑：M1 | 任务数：8 | 总工时：9 人天 | 建议周期：2-3 周
> 目标：ActorContext + AgentFactory + TurnRunner + Agent配置 + 异步执行 + 安全底座
> 后续 M2-M7 全部踩在这一层上

---

## M1-01: Agent 配置结构体扩展 + 可序列化 + 4 个内置 Agent

**工时**: 1.5 天 | **依赖**: 无

### 涉及文件

- `internal/config/config.go`（修改）

### 具体代码改动

#### 改动 1：去掉 `json:"-"` 标签（行 590）

```go
// 改前
type Config struct {
    // ...
    Agents map[string]Agent `json:"-"`
}

// 改后
type Config struct {
    // ...
    Agents map[string]Agent `json:"agents"`
}
```

#### 改动 2：扩展 Agent 结构体（行 496-517）

```go
type Agent struct {
    // --- 已有字段，保持不变 ---
    ID          string              `json:"id,omitempty"`
    Name        string              `json:"name,omitempty"`
    Description string              `json:"description,omitempty"`
    Disabled    bool                `json:"disabled,omitempty"`
    Model       SelectedModelType   `json:"model" jsonschema:"required,description=The model type to use for this agent,enum=large,enum=small,default=large"`
    AllowedTools []string           `json:"allowed_tools,omitempty"`
    AllowedMCP  map[string][]string `json:"allowed_mcp,omitempty"`
    ContextPaths []string           `json:"context_paths,omitempty"`

    // --- M1 新增字段 ---
    // 每个Agent自己的工具黑名单
    DisallowedTools []string `json:"disallowed_tools,omitempty"`

    // 每个Agent自己的系统提示词模板
    SystemPrompt string `json:"system_prompt,omitempty"`

    // 权限模式：default | acceptEdits | plan | bypassPermissions
    PermissionMode string `json:"permission_mode,omitempty" jsonschema:"description=Permission mode for this agent,enum=default,enum=acceptEdits,enum=plan,enum=bypassPermissions"`

    // --- M2-M4 预留字段（M1 只定义 schema，暂不使用）---
    MaxTurns   int      `json:"max_turns,omitempty" jsonschema:"description=Maximum number of agentic turns before stopping"`
    Skills     []string `json:"skills,omitempty" jsonschema:"description=Skill names to preload for this agent"`
    McpServers []string `json:"mcp_servers,omitempty" jsonschema:"description=MCP server names available to this agent"`
}
```

#### 改动 3：新增 Agent ID 常量（行 59-62 附近）

```go
const (
    AgentCoder          string = "coder"
    AgentTask           string = "task"            // 保留兼容
    AgentGeneralPurpose string = "general-purpose" // M1 新增
    AgentExplore        string = "explore"         // M1 新增
    AgentPlan           string = "plan"            // M1 新增
)
```

#### 改动 4：重写 `SetupAgents()`（行 711-736）

```go
func (c *Config) SetupAgents() {
    allowedTools := resolveAllowedTools(allToolNames(), c.Options.DisabledTools)

    // 1. 内置默认 Agent
    builtins := map[string]Agent{
        AgentCoder: {
            ID:           AgentCoder,
            Name:         "Coder",
            Description:  "主编码Agent，拥有全部工具权限，负责与用户交互并协调子Agent。",
            Model:        SelectedModelTypeLarge,
            ContextPaths: c.Options.ContextPaths,
            AllowedTools: allowedTools,
            // coder 不设 DisallowedTools——它是主Agent，拥有全部权限
        },
        AgentGeneralPurpose: {
            ID:           AgentGeneralPurpose,
            Name:         "General Purpose",
            Description:  "通用子Agent，用于搜索代码、分析文件和执行多步骤任务。",
            Model:        SelectedModelTypeLarge,
            ContextPaths: c.Options.ContextPaths,
            AllowedTools:  []string{"view", "grep", "glob", "ls", "bash", "write"},
            DisallowedTools: []string{
                "agent",              // 防止递归
                "ask_user_questions", // 子Agent不能向用户提问
                "job_output",         // 不能读其他任务输出
                "job_kill",           // 不能Kill其他任务
                "todos",              // 不需要任务管理
                "crush_info",         // 不需要Crush自身信息
                "crush_logs",         // 不需要日志
            },
            AllowedMCP: map[string][]string{}, // 空map = 禁止所有MCP
        },
        AgentExplore: {
            ID:           AgentExplore,
            Name:         "Explore",
            Description:  "快速搜索Agent，用于文件查找和代码探索，只读。",
            Model:        SelectedModelTypeSmall,
            ContextPaths: c.Options.ContextPaths,
            AllowedTools:  []string{"view", "grep", "glob", "ls", "sourcegraph"},
            DisallowedTools: []string{
                "agent", "ask_user_questions", "job_output", "job_kill",
                "todos", "crush_info", "crush_logs",
                "bash", "write", "edit", "multiedit", "download",
            },
            PermissionMode: "plan", // 只读规划模式
            AllowedMCP:     map[string][]string{},
        },
        AgentPlan: {
            ID:           AgentPlan,
            Name:         "Plan",
            Description:  "规划设计Agent，只读分析，用于架构分析和实现方案设计。",
            Model:        SelectedModelTypeSmall,
            ContextPaths: c.Options.ContextPaths,
            AllowedTools:  []string{"view", "grep", "glob", "ls"},
            DisallowedTools: []string{
                "agent", "ask_user_questions", "job_output", "job_kill",
                "todos", "crush_info", "crush_logs",
                "bash", "write", "edit", "multiedit", "download",
                "sourcegraph", "fetch", "agentic_fetch",
            },
            PermissionMode: "plan",
            AllowedMCP:     map[string][]string{},
        },
    }

    // 2. 合并用户配置
    // 用户 crush.json 中的 "agents" 字段在 Load() 时已经反序列化到 c.Agents
    for key, userAgent := range c.Agents {
        if existing, ok := builtins[key]; ok {
            // 覆盖内置Agent的非零值字段
            if userAgent.Name != "" {
                existing.Name = userAgent.Name
            }
            if userAgent.Description != "" {
                existing.Description = userAgent.Description
            }
            if userAgent.Model != "" {
                existing.Model = userAgent.Model
            }
            if userAgent.AllowedTools != nil {
                existing.AllowedTools = userAgent.AllowedTools
            }
            if userAgent.DisallowedTools != nil {
                existing.DisallowedTools = userAgent.DisallowedTools
            }
            if userAgent.SystemPrompt != "" {
                existing.SystemPrompt = userAgent.SystemPrompt
            }
            if userAgent.PermissionMode != "" {
                existing.PermissionMode = userAgent.PermissionMode
            }
            if userAgent.AllowedMCP != nil {
                existing.AllowedMCP = userAgent.AllowedMCP
            }
            if userAgent.ContextPaths != nil {
                existing.ContextPaths = userAgent.ContextPaths
            }
            if userAgent.Disabled {
                existing.Disabled = true
            }
            builtins[key] = existing
        } else {
            // 新增用户自定义Agent
            builtins[key] = userAgent
        }
    }

    c.Agents = builtins
}
```

#### 改动 5：新增模式常量（可选）

```go
const (
    PermissionModeDefault         = "default"
    PermissionModeAcceptEdits     = "acceptEdits"
    PermissionModePlan            = "plan"
    PermissionModeBypassPermissions = "bypassPermissions"
)
```

### 配置文件示例

用户在 `crush.json` 中：

```json
{
  "agents": {
    "code-reviewer": {
      "name": "代码审查员",
      "description": "审查代码安全性，查找SQL注入、XSS、权限绕行等问题。",
      "model": "small",
      "allowed_tools": ["view", "grep", "glob", "bash"],
      "disallowed_tools": ["write", "edit", "agent"],
      "system_prompt": "你是代码安全审查专家。审查代码时关注：SQL注入、XSS、权限绕行、敏感信息泄露。输出简洁报告。",
      "permission_mode": "acceptEdits"
    },
    "general-purpose": {
      "allowed_tools": ["view", "grep", "glob", "ls", "bash"],
      "disallowed_tools": ["write", "edit", "agent", "ask_user_questions"]
    }
  }
}
```

- `"code-reviewer"` 是新增的自定义 Agent
- `"general-purpose"` 覆盖了内置 general-purpose 的工具集
- 其他字段（model、system_prompt）保持内置默认值

### 验收标准

1. `crush.json` 中 `"agents": {...}` 字段可正确序列化和反序列化
2. 用户配置 `"general-purpose": {"allowed_tools": ["view"]}` 覆盖内置 AllowedTools，其他字段保持默认
3. 用户新增 `"code-reviewer"` 出现在 `SetupAgents()` 后的 `c.Agents` 中
4. `SetupAgents()` 执行后始终包含 coder / general-purpose / explore / plan 四个
5. Agent.Disabled=true 时该 Agent 不出现在 buildAgent 候选列表中
6. 现有 config golden file 测试通过或同步更新
7. 旧配置文件（无 agents 字段）不崩溃，`c.Agents` 仅含 4 个内置

### 测试用例

```go
func TestSetupAgents_BuiltinsAlwaysPresent(t *testing.T) {
    cfg := &Config{}
    cfg.SetupAgents()
    // 四个内置Agent始终存在
    assert.Contains(t, cfg.Agents, AgentCoder)
    assert.Contains(t, cfg.Agents, AgentGeneralPurpose)
    assert.Contains(t, cfg.Agents, AgentExplore)
    assert.Contains(t, cfg.Agents, AgentPlan)
}

func TestSetupAgents_UserOverrideBuiltin(t *testing.T) {
    cfg := &Config{
        Agents: map[string]Agent{
            AgentGeneralPurpose: {
                AllowedTools: []string{"view", "grep"},
            },
        },
    }
    cfg.SetupAgents()
    gp := cfg.Agents[AgentGeneralPurpose]
    // 用户指定的工具集
    assert.Equal(t, []string{"view", "grep"}, gp.AllowedTools)
    // 其他字段保持内置默认
    assert.Equal(t, SelectedModelTypeLarge, gp.Model)
    assert.Equal(t, "General Purpose", gp.Name)
}

func TestSetupAgents_UserAddsCustomAgent(t *testing.T) {
    cfg := &Config{
        Agents: map[string]Agent{
            "code-reviewer": {
                Name: "Code Reviewer",
                Model: SelectedModelTypeSmall,
                AllowedTools: []string{"view", "grep", "glob"},
            },
        },
    }
    cfg.SetupAgents()
    assert.Contains(t, cfg.Agents, "code-reviewer")
    assert.Equal(t, "Code Reviewer", cfg.Agents["code-reviewer"].Name)
}
```

---

## M1-02: 全局安全底座 — 黑名单 + 递归深度防护 + buildTools 过滤

**工时**: 1 天 | **依赖**: M1-01

### 涉及文件

- `internal/agent/tools/disallowed.go`（新建）
- `internal/agent/coordinator.go`（修改 `buildTools`，行 480-590）
- `internal/agent/agent_tool.go`（修改 `agentTool`，行 26-68）

### 具体代码改动

#### 改动 1：全局黑名单（新建文件）

```go
// internal/agent/tools/disallowed.go
package tools

// SubAgentDisallowedTools 是所有子Agent永远不能使用的工具列表。
// 这个列表在 buildTools() 的最后一层过滤中被强制应用（isSubAgent=true 时）。
// 主Agent (coder, isSubAgent=false) 不受此列表影响。
//
// 列表设计原则：
//   - agent: 防止子Agent递归创建更多子Agent
//   - ask_user_questions: 子Agent不能弹窗与用户交互
//   - job_output: 子Agent不能读取其他并行任务的输出
//   - job_kill: 子Agent不能终止其他任务
//   - todos: 子Agent不需要管理任务列表
//   - crush_info: 子Agent不需要Crush自身信息
//   - crush_logs: 子Agent不需要访问日志
var SubAgentDisallowedTools = []string{
    "agent",
    "ask_user_questions",
    "job_output",
    "job_kill",
    "todos",
    "crush_info",
    "crush_logs",
}

// IsSubAgentDisallowed 检查工具名是否在全局子Agent黑名单中
func IsSubAgentDisallowed(toolName string) bool {
    for _, disallowed := range SubAgentDisallowedTools {
        if toolName == disallowed {
            return true
        }
    }
    return false
}
```

#### 改动 2：递归深度防护（agent_tool.go）

```go
// internal/agent/agent_tool.go

// recursionDepthKey 是context中存储递归深度的key
type recursionDepthKey struct{}

// maxRecursionDepth 是子Agent的最大嵌套深度
const maxRecursionDepth = 3

// withRecursionDepth 将递归深度写入context
func withRecursionDepth(ctx context.Context, depth int) context.Context {
    return context.WithValue(ctx, recursionDepthKey{}, depth)
}

// getRecursionDepth 从context读取递归深度，不存在时返回0
func getRecursionDepth(ctx context.Context) int {
    v, ok := ctx.Value(recursionDepthKey{}).(int)
    if !ok {
        return 0
    }
    return v
}
```

在 `agentTool` 的 handler 函数（约行 43）入口处：

```go
func (c *coordinator) agentTool(ctx context.Context) (fantasy.AgentTool, error) {
    // ... 现有代码 ...

    return fantasy.NewParallelAgentTool(
        AgentToolName,
        agentToolDescription,
        func(ctx context.Context, params AgentParams, call fantasy.ToolCall) (fantasy.ToolResponse, error) {
            if params.Prompt == "" {
                return fantasy.NewTextErrorResponse("prompt is required"), nil
            }

            // --- M1-02 新增：递归深度检查 ---
            depth := getRecursionDepth(ctx)
            if depth >= maxRecursionDepth {
                return fantasy.NewTextErrorResponse(
                    fmt.Sprintf("max agent recursion depth exceeded (depth=%d, max=%d)", depth, maxRecursionDepth),
                ), nil
            }
            // 向下传递 depth+1
            ctx = withRecursionDepth(ctx, depth+1)
            // --- 递归深度检查结束 ---

            sessionID := tools.GetSessionFromContext(ctx)
            if sessionID == "" {
                return fantasy.ToolResponse{}, errors.New("session id missing from context")
            }

            agentMessageID := tools.GetMessageFromContext(ctx)
            if agentMessageID == "" {
                return fantasy.ToolResponse{}, errors.New("agent message id missing from context")
            }

            return c.runSubAgent(ctx, subAgentParams{
                Agent:          agent,
                SessionID:      sessionID,
                AgentMessageID: agentMessageID,
                ToolCallID:     call.ID,
                Prompt:         params.Prompt,
                SessionTitle:   "New Agent Session",
            })
        },
    ), nil
}
```

#### 改动 3：buildTools 双层过滤

在 `coordinator.go` 的 `buildTools()` 方法中（行 548-553），修改过滤逻辑：

```go
// 改前（行 548-553）
var filteredTools []fantasy.AgentTool
for _, tool := range allTools {
    if slices.Contains(agent.AllowedTools, tool.Info().Name) {
        filteredTools = append(filteredTools, tool)
    }
}

// 改后
var filteredTools []fantasy.AgentTool
for _, tool := range allTools {
    toolName := tool.Info().Name

    // 第一层：Agent 白名单过滤（已有逻辑）
    if !slices.Contains(agent.AllowedTools, toolName) {
        continue
    }

    // 第二层：子Agent安全过滤（M1-02 新增）
    if isSubAgent {
        // 2a. 全局子Agent禁止列表
        if tools.IsSubAgentDisallowed(toolName) {
            continue
        }
        // 2b. 每个Agent自己的禁止列表
        if slices.Contains(agent.DisallowedTools, toolName) {
            continue
        }
    }

    filteredTools = append(filteredTools, tool)
}
```

### 验收标准

1. `agent` tool 嵌套调用第 4 层时返回错误 `"max agent recursion depth exceeded"`
2. general-purpose 子Agent 的工具列表中不包含: `agent`, `ask_user_questions`, `job_output`, `job_kill`, `todos`, `crush_info`, `crush_logs`
3. 用户 `crush.json` 中 `"disallowed_tools": ["bash"]` 对 general-purpose 额外禁止 bash
4. coder（主Agent, `isSubAgent=false`）工具列表不受黑名单影响
5. `IsSubAgentDisallowed` 函数单测覆盖全部 7 个工具名

### 测试用例

```go
func TestSubAgentDisallowedTools_ContainsExpected(t *testing.T) {
    expected := []string{"agent", "ask_user_questions", "job_output", "job_kill", "todos", "crush_info", "crush_logs"}
    for _, name := range expected {
        assert.True(t, IsSubAgentDisallowed(name), "tool %q should be disallowed", name)
    }
    assert.False(t, IsSubAgentDisallowed("view"))
    assert.False(t, IsSubAgentDisallowed("bash"))
}

func TestRecursionDepth_RoundTrip(t *testing.T) {
    ctx := context.Background()
    assert.Equal(t, 0, getRecursionDepth(ctx))

    ctx = withRecursionDepth(ctx, 2)
    assert.Equal(t, 2, getRecursionDepth(ctx))

    ctx = withRecursionDepth(ctx, 0)
    assert.Equal(t, 0, getRecursionDepth(ctx))
}

func TestBuildTools_SubAgentFiltersDisallowed(t *testing.T) {
    // Mock agent config
    agent := config.Agent{
        AllowedTools:    []string{"view", "grep", "bash", "agent", "ask_user_questions"},
        DisallowedTools: []string{"bash"}, // 用户额外禁止bash
    }
    // isSubAgent=true时
    tools := buildToolsForTest(t, agent, true /* isSubAgent */)
    toolNames := extractToolNames(tools)

    assert.Contains(t, toolNames, "view")
    assert.Contains(t, toolNames, "grep")
    assert.NotContains(t, toolNames, "bash")              // 用户禁止
    assert.NotContains(t, toolNames, "agent")              // 全局禁止
    assert.NotContains(t, toolNames, "ask_user_questions") // 全局禁止
}
```

---

## M1-03: ActorContext 身份系统

**工时**: 0.5 天 | **依赖**: 无

### 涉及文件

- `internal/actor/actor.go`（新建）

### 具体代码改动

```go
// internal/actor/actor.go
// Package actor 提供统一的Actor身份标识，供 tools、hooks、permission、team runtime 共享。
// 不依赖 internal/team，避免底层工具反向依赖 team domain。

package actor

import "context"

// ActorContext 表示当前操作的执行者身份。
// M1 使用 SessionID/ParentSessionID/MessageID/ToolCallID 四个核心字段。
// M2+ 逐步填充 TeamID/MemberID/TaskID/RunID 等团队相关字段。
type ActorContext struct {
    // --- M1 字段 ---
    SessionID       string `json:"session_id"`
    ParentSessionID string `json:"parent_session_id,omitempty"`
    MessageID       string `json:"message_id,omitempty"`
    ToolCallID      string `json:"tool_call_id,omitempty"`
    WorkspaceID     string `json:"workspace_id,omitempty"`

    // --- M2+ 预留字段（M1 定义，M2+ 填充）---
    TeamID     string `json:"team_id,omitempty"`
    MemberID   string `json:"member_id,omitempty"`
    MemberName string `json:"member_name,omitempty"`
    MemberRole string `json:"member_role,omitempty"`
    TaskID     string `json:"task_id,omitempty"`
    RunID      string `json:"run_id,omitempty"`
}

type actorContextKey struct{}

// WithContext 将 ActorContext 注入 context
func (a ActorContext) WithContext(ctx context.Context) context.Context {
    return context.WithValue(ctx, actorContextKey{}, a)
}

// FromContext 从 context 提取 ActorContext，不存在时返回 false
func FromContext(ctx context.Context) (ActorContext, bool) {
    v, ok := ctx.Value(actorContextKey{}).(ActorContext)
    return v, ok
}

// ShortID 返回截断的 SessionID（前8字符），用于日志
func (a ActorContext) ShortID() string {
    if len(a.SessionID) > 8 {
        return a.SessionID[:8]
    }
    return a.SessionID
}

// IsSubAgent 判断是否为子Agent（存在 ParentSessionID）
func (a ActorContext) IsSubAgent() bool {
    return a.ParentSessionID != ""
}

// IsTeamMember 判断是否为团队成员（M2+ 使用）
func (a ActorContext) IsTeamMember() bool {
    return a.TeamID != "" && a.MemberID != ""
}
```

### 验收标准

1. `ActorContext` 通过 `WithContext` / `FromContext` 往返存取一致
2. `FromContext` 在未注入时返回 `(ActorContext{}, false)`
3. JSON 序列化时 omitempty 字段为空值不出现在 JSON 中
4. `ShortID()` 正确截断
5. `IsSubAgent()` / `IsTeamMember()` 逻辑正确

### 测试用例

```go
func TestActorContext_RoundTrip(t *testing.T) {
    ctx := context.Background()
    _, ok := actor.FromContext(ctx)
    assert.False(t, ok)

    ac := actor.ActorContext{
        SessionID:       "sess_abc123",
        ParentSessionID: "sess_parent",
        MessageID:       "msg_456",
        ToolCallID:      "call_789",
    }
    ctx = ac.WithContext(ctx)

    got, ok := actor.FromContext(ctx)
    assert.True(t, ok)
    assert.Equal(t, "sess_abc123", got.SessionID)
    assert.Equal(t, "sess_parent", got.ParentSessionID)
}

func TestActorContext_ShortID(t *testing.T) {
    ac := actor.ActorContext{SessionID: "abcdefghijklmnop"}
    assert.Equal(t, "abcdefgh", ac.ShortID())

    ac2 := actor.ActorContext{SessionID: "short"}
    assert.Equal(t, "short", ac2.ShortID())
}

func TestActorContext_IsSubAgent(t *testing.T) {
    assert.True(t, actor.ActorContext{ParentSessionID: "parent"}.IsSubAgent())
    assert.False(t, actor.ActorContext{}.IsSubAgent())
}
```

---

## M1-04: TurnRunner + AgentFactory 接口定义

**工时**: 0.5 天 | **依赖**: M1-03

### 涉及文件

- `internal/agent/team_call.go`（新建）

### 具体代码改动

```go
// internal/agent/team_call.go
package agent

import (
    "context"
    "charm.land/fantasy"
    "github.com/charmbracelet/crush/internal/actor"
)

// TurnStatus 表示一轮 agent turn 的终态
type TurnStatus string

const (
    TurnCompleted TurnStatus = "completed" // 正常完成
    TurnQueued    TurnStatus = "queued"    // 已入队，尚未执行
    TurnCanceled  TurnStatus = "canceled"  // 被取消
    TurnFailed    TurnStatus = "failed"    // 执行失败

    // TurnRunning 不是终态，仅用于状态查询
    TurnRunning TurnStatus = "running"
)

// IsTerminal 判断是否为终态
func (s TurnStatus) IsTerminal() bool {
    return s == TurnCompleted || s == TurnCanceled || s == TurnFailed
}

// TeamAgentCall 是 team/delegate/member runtime 调用 agent 的描述符。
// 它与 SessionAgentCall 是独立类型——SessionAgentCall 被 SessionAgent.Run 直接消费，
// TeamAgentCall 用于 TurnRunner 接口，两个不能混淆。
type TeamAgentCall struct {
    SessionID       string
    ParentSessionID string
    PromptEnvelope  string            // 组装后的完整提示词
    Actor           actor.ActorContext // 执行者身份
    ToolPolicy      ToolPolicyProfile  // 工具策略
}

// ToolPolicyProfile 定义工具使用策略
type ToolPolicyProfile struct {
    AllowedTools    []string // 白名单（nil = 全部允许）
    DisallowedTools []string // 黑名单（额外禁止）
    PermissionMode  string   // default | acceptEdits | plan | bypassPermissions
}

// TurnRunResult 是一轮 agent turn 的结果
// Status 为 queued 时 Result 为 nil（表示已入队但尚未执行）
type TurnRunResult struct {
    Status TurnStatus
    Result *fantasy.AgentResult
}

// TurnRunner 是 agent turn 的执行接口。
// M1 用于单个子Agent，M2 DelegateRunner 用于并行搜索，
// M4 MemberRunner 用于长期队员。三层共用同一接口。
type TurnRunner interface {
    // Run 执行一轮 agent turn
    Run(ctx context.Context, call TeamAgentCall) (TurnRunResult, error)

    // Cancel 取消指定 session 的当前 turn
    Cancel(sessionID string)

    // IsSessionBusy 检查 session 是否有活跃的 turn
    IsSessionBusy(sessionID string) bool
}

// AgentSpec 描述创建一个 TurnRunner 所需的参数
type AgentSpec struct {
    AgentType      string            // agent 类型标识 (如 "general-purpose")
    SystemPrompt   string            // 系统提示词
    ModelType      string            // 模型选择 ("large" | "small" | "inherit")
    PermissionMode string            // 权限模式
    ToolPolicy     ToolPolicyProfile // 工具策略
}

// AgentFactory 创建独立 SessionAgent 实例的工厂。
// 每个 runner 持有独立实例——不复用 Coordinator.currentAgent，
// 确保 coder 的 SetTools/SetModels 不影响 delegate/member runner。
type AgentFactory interface {
    BuildRunner(ctx context.Context, spec AgentSpec) (TurnRunner, error)
}
```

### 验收标准

1. 编译通过，`TeamAgentCall` 与 `SessionAgentCall` 不产生命名冲突
2. `TurnRunResult` 明确区分 completed/queued/canceled/failed 四种终态
3. `TurnStatus.IsTerminal()` 对四种状态返回正确
4. 接口定义不修改 `agent.go` 或 `coordinator.go` 的已有类型

### 测试用例

```go
func TestTurnStatus_IsTerminal(t *testing.T) {
    assert.True(t, TurnCompleted.IsTerminal())
    assert.True(t, TurnCanceled.IsTerminal())
    assert.True(t, TurnFailed.IsTerminal())
    assert.False(t, TurnQueued.IsTerminal())
    assert.False(t, TurnRunning.IsTerminal())
}

func TestTeamAgentCall_NotConfusableWithSessionAgentCall(t *testing.T) {
    // 编译时类型检查：TeamAgentCall 和 SessionAgentCall 是不同的类型
    var tc TeamAgentCall
    // 这行如果编译通过，说明两个类型确实独立
    _ = tc.SessionID
    _ = tc.ToolPolicy
}
```

---

## M1-05: 异步执行 + Agent Kill

**工时**: 2 天 | **依赖**: M1-02, M1-04

### 涉及文件

- `internal/agent/coordinator.go`（修改 `runSubAgent` 行 1096-1144 + coordinator 结构体 + Cancel/CancelAll）
- `internal/agent/agent_tool.go`（修改 handler）

### 具体代码改动

#### 改动 1：新增类型定义

```go
// 放在 coordinator.go，coordinator 结构体之前

// SubAgentMode 表示子Agent的执行模式
type SubAgentMode int

const (
    SubAgentSync  SubAgentMode = iota // 同步阻塞（当前行为，兼容模式）
    SubAgentAsync                      // 异步后台执行
)

// SubAgentHandle 是异步子Agent的句柄，调用方通过它查询状态和取消执行
type SubAgentHandle struct {
    RunID      string
    StatusChan <-chan SubAgentStatus
    Cancel     context.CancelFunc
}

// SubAgentStatus 表示异步子Agent的当前状态
type SubAgentStatus struct {
    State    string // "running" | "done" | "error" | "canceled"
    Progress string // 最近一条进度描述
    Result   *AgentToolResult
    Error    error
}
```

#### 改动 2：扩展 subAgentParams

```go
// 在 coordinator.go 行 1081-1091
type subAgentParams struct {
    Agent          SessionAgent
    SessionID      string
    AgentMessageID string
    ToolCallID     string
    Prompt         string
    SessionTitle   string
    SessionSetup   func(sessionID string)

    // M1-05 新增
    Mode     SubAgentMode // Sync 或 Async
    AgentID  string       // agent 类型标识，用于 AgentToolResult
    ActorCtx actor.ActorContext
}
```

#### 改动 3：扩展 coordinator 结构体

```go
// coordinator 结构体（行 90-110）新增字段
type coordinator struct {
    // ... 已有字段 ...

    // M1-05 新增：追踪活跃的异步子Agent
    activeSubAgents   map[string]context.CancelFunc
    activeSubAgentsMu sync.RWMutex
}
```

在 `NewCoordinator` 中初始化：
```go
c := &coordinator{
    // ... 已有字段 ...
    activeSubAgents: make(map[string]context.CancelFunc),
}
```

#### 改动 4：拆分 runSubAgent

```go
// 同步模式（原逻辑基本不变，只改返回值类型）
func (c *coordinator) runSubAgentSync(ctx context.Context, params subAgentParams) (AgentToolResult, error) {
    agentToolSessionID := c.sessions.CreateAgentToolSessionID(params.AgentMessageID, params.ToolCallID)
    session, err := c.sessions.CreateTaskSession(ctx, agentToolSessionID, params.SessionID, params.SessionTitle)
    if err != nil {
        return AgentToolResult{}, fmt.Errorf("create session: %w", err)
    }

    if params.SessionSetup != nil {
        params.SessionSetup(session.ID)
    }

    model := params.Agent.Model()
    maxTokens := model.CatwalkCfg.DefaultMaxTokens
    if model.ModelCfg.MaxTokens != 0 {
        maxTokens = model.ModelCfg.MaxTokens
    }

    providerCfg, ok := c.cfg.Config().Providers.Get(model.ModelCfg.Provider)
    if !ok {
        return AgentToolResult{}, errModelProviderNotConfigured
    }

    startTime := time.Now()
    result, err := params.Agent.Run(ctx, SessionAgentCall{
        SessionID:        session.ID,
        Prompt:           params.Prompt,
        MaxOutputTokens:  maxTokens,
        ProviderOptions:  getProviderOptions(model, providerCfg),
        Temperature:      model.ModelCfg.Temperature,
        TopP:             model.ModelCfg.TopP,
        TopK:             model.ModelCfg.TopK,
        FrequencyPenalty: model.ModelCfg.FrequencyPenalty,
        PresencePenalty:  model.ModelCfg.PresencePenalty,
        NonInteractive:   true,
    })
    if err != nil {
        return AgentToolResult{}, err
    }

    // 成本累加
    if err := c.updateParentSessionCost(ctx, session.ID, params.SessionID); err != nil {
        return AgentToolResult{}, err
    }

    return c.buildAgentToolResult(result, params.AgentID, startTime, session.ID), nil
}

// 异步模式：启动 goroutine，立即返回句柄
func (c *coordinator) runSubAgentAsync(ctx context.Context, params subAgentParams) (SubAgentHandle, error) {
    runID := uuid.New().String()
    ctx, cancel := context.WithCancel(ctx)

    // 注册
    c.activeSubAgentsMu.Lock()
    c.activeSubAgents[runID] = cancel
    c.activeSubAgentsMu.Unlock()

    statusChan := make(chan SubAgentStatus, 10)

    go func() {
        defer close(statusChan)
        defer func() {
            c.activeSubAgentsMu.Lock()
            delete(c.activeSubAgents, runID)
            c.activeSubAgentsMu.Unlock()
        }()

        // Send initial status
        statusChan <- SubAgentStatus{State: "running", Progress: "starting..."}

        result, err := c.runSubAgentSync(ctx, params)
        if err != nil {
            if errors.Is(err, context.Canceled) {
                statusChan <- SubAgentStatus{State: "canceled", Error: err}
            } else {
                statusChan <- SubAgentStatus{State: "error", Error: err}
            }
            return
        }

        statusChan <- SubAgentStatus{State: "done", Result: &result}
    }()

    return SubAgentHandle{
        RunID:      runID,
        StatusChan: statusChan,
        Cancel:     cancel,
    }, nil
}

// 入口：根据 mode 分发
func (c *coordinator) runSubAgent(ctx context.Context, params subAgentParams) (interface{}, error) {
    if params.Mode == SubAgentAsync {
        return c.runSubAgentAsync(ctx, params)
    }
    return c.runSubAgentSync(ctx, params)
}
```

#### 改动 5：扩展 Cancel/CancelAll

```go
func (c *coordinator) Cancel(sessionID string) {
    // ... 已有逻辑（取消主Agent）...

    // M1-05：遍历活跃子Agent，取消匹配的
    c.activeSubAgentsMu.RLock()
    defer c.activeSubAgentsMu.RUnlock()
    for runID, cancel := range c.activeSubAgents {
        // 通过 runID 前缀匹配或其他方式找到对应子Agent
        if strings.Contains(runID, sessionID) {
            cancel()
        }
    }
}

func (c *coordinator) CancelAll() {
    // ... 已有逻辑 ...

    // M1-05：取消所有活跃子Agent
    c.activeSubAgentsMu.RLock()
    defer c.activeSubAgentsMu.RUnlock()
    for runID, cancel := range c.activeSubAgents {
        slog.Debug("canceling sub-agent", "run_id", runID)
        cancel()
    }
}
```

### 验收标准

1. 同步模式（SubAgentSync）行为与当前完全一致
2. 异步模式（SubAgentAsync）**立即**返回 SubAgentHandle，不阻塞调用方
3. `handle.Cancel()` 调用后子Agent goroutine 在 5 秒内退出
4. `CancelAll()` 终止所有活跃子Agent
5. 执行 100 次异步子Agent后 `runtime.NumGoroutine()` 无泄漏增长
6. 异步子Agent正常完成时 channel 正确关闭，状态为 "done"
7. 异步子Agent被取消时状态为 "canceled"

### 测试用例

```go
func TestSubAgentAsync_ImmediateReturn(t *testing.T) {
    start := time.Now()
    handle, err := coordinator.runSubAgent(ctx, subAgentParams{
        Mode: SubAgentAsync,
        // ... other params
    })
    elapsed := time.Since(start)

    assert.NoError(t, err)
    assert.Less(t, elapsed, 100*time.Millisecond, "async should return immediately")
    assert.NotEmpty(t, handle.RunID)
    assert.NotNil(t, handle.StatusChan)
}

func TestSubAgentAsync_Cancel(t *testing.T) {
    handle, _ := coordinator.runSubAgent(ctx, subAgentParams{Mode: SubAgentAsync})
    handle.Cancel()

    // 等待goroutine退出
    select {
    case status := <-handle.StatusChan:
        if status.State == "canceled" {
            return // OK
        }
        // 可能还有其他状态
    case <-time.After(5 * time.Second):
        t.Fatal("cancel did not stop goroutine within 5s")
    }
}

func TestSubAgentAsync_NoGoroutineLeak(t *testing.T) {
    before := runtime.NumGoroutine()

    for i := 0; i < 100; i++ {
        handle, _ := coordinator.runSubAgent(ctx, subAgentParams{Mode: SubAgentAsync})
        handle.Cancel()
    }

    // 等待所有goroutine退出
    time.Sleep(500 * time.Millisecond)

    after := runtime.NumGoroutine()
    assert.LessOrEqual(t, after, before+5, "goroutine leak detected")
}
```

---

## M1-06: 结构化返回结果 AgentToolResult

**工时**: 1 天 | **依赖**: M1-05

### 涉及文件

- `internal/agent/agent_result.go`（新建）
- `internal/agent/coordinator.go`（修改 runSubAgent 返回值）
- `internal/agent/agent_tool.go`（修改 handler 返回值序列化）

### 具体代码改动

#### 改动 1：结果结构体定义

```go
// internal/agent/agent_result.go
package agent

import "time"

// AgentToolResult 是子Agent完成后的结构化结果。
// 序列化为 JSON 嵌入 ToolResponse.Content 返回给主Agent。
type AgentToolResult struct {
    AgentID           string `json:"agent_id"`
    AgentType         string `json:"agent_type"`
    Content           string `json:"content"`
    TotalTokens       int64  `json:"total_tokens"`
    TotalToolUseCount int    `json:"total_tool_use_count"`
    TotalDurationMs   int64  `json:"total_duration_ms"`
    Usage             Usage  `json:"usage"`
}

// Usage 包含 token 使用详情
type Usage struct {
    InputTokens      int64 `json:"input_tokens"`
    OutputTokens     int64 `json:"output_tokens"`
    CacheReadTokens  int64 `json:"cache_read_tokens,omitempty"`
}

// buildAgentToolResult 从 fantasy.AgentResult 构建 AgentToolResult
func buildAgentToolResult(
    result *fantasy.AgentResult,
    agentType string,
    startTime time.Time,
    sessionID string,
) AgentToolResult {
    ar := AgentToolResult{
        AgentID:           sessionID,
        AgentType:         agentType,
        Content:           result.Response.Content.Text(),
        TotalTokens:       result.TotalUsage.InputTokens + result.TotalUsage.OutputTokens,
        TotalToolUseCount: countToolCalls(result),
        TotalDurationMs:   time.Since(startTime).Milliseconds(),
        Usage: Usage{
            InputTokens:     result.TotalUsage.InputTokens,
            OutputTokens:    result.TotalUsage.OutputTokens,
            CacheReadTokens: result.TotalUsage.CacheReadTokens,
        },
    }
    return ar
}

// countToolCalls 统计 agent result 中的工具调用次数
func countToolCalls(result *fantasy.AgentResult) int {
    count := 0
    for _, step := range result.Steps {
        for _, content := range step.Content {
            if _, ok := content.(fantasy.ToolCallContent); ok {
                count++
            }
        }
    }
    return count
}
```

#### 改动 2：handler 中序列化

```go
// agent_tool.go handler 中
result, err := c.runSubAgent(ctx, params)
if err != nil {
    return fantasy.NewTextErrorResponse(...), nil
}

// 序列化为 JSON
resultJSON, _ := json.Marshal(result)

return fantasy.NewTextResponse(string(resultJSON)), nil
```

### 验收标准

1. 子Agent完成后 ToolResponse.Content 是合法 JSON
2. JSON 反序列化为 AgentToolResult 后 `agent_type` 为实际类型
3. `total_tool_use_count` 统计正确
4. `total_duration_ms` 为实际耗时
5. `usage.input_tokens` / `usage.output_tokens` 与 model response 一致

### 测试用例

```go
func TestAgentToolResult_JSONRoundTrip(t *testing.T) {
    original := AgentToolResult{
        AgentID:           "sess_123",
        AgentType:         "general-purpose",
        Content:           "Found 5 files",
        TotalTokens:       1500,
        TotalToolUseCount: 8,
        TotalDurationMs:   45200,
        Usage: Usage{InputTokens: 800, OutputTokens: 700},
    }

    data, _ := json.Marshal(original)
    var decoded AgentToolResult
    json.Unmarshal(data, &decoded)

    assert.Equal(t, original.AgentType, decoded.AgentType)
    assert.Equal(t, original.TotalToolUseCount, decoded.TotalToolUseCount)
    assert.Equal(t, original.TotalDurationMs, decoded.TotalDurationMs)
}
```

---

## M1-07: 进度回调 — pubsub AgentProgressEvent

**工时**: 1 天 | **依赖**: M1-05

### 涉及文件

- `internal/pubsub/events.go`（修改）
- `internal/agent/progress.go`（新建）
- `internal/agent/coordinator.go`（修改）

### 具体代码改动

```go
// internal/agent/progress.go
package agent

// AgentProgressEvent 表示子Agent的执行进度
type AgentProgressEvent struct {
    RunID        string `json:"run_id"`
    AgentType    string `json:"agent_type"`
    ToolUseCount int    `json:"tool_use_count"`
    LastToolName string `json:"last_tool_name,omitempty"`
    TokenCount   int    `json:"token_count"`
    ActivityDesc string `json:"activity_desc,omitempty"`
    ElapsedMs    int64  `json:"elapsed_ms"`
}
```

```go
// pubsub/events.go 新增
const PayloadTypeAgentProgress PayloadType = "agent_progress"
```

```go
// coordinator.go：异步goroutine中定期发布进度
go func() {
    ticker := time.NewTicker(500 * time.Millisecond)
    defer ticker.Stop()

    for {
        select {
        case <-ctx.Done():
            return
        case <-ticker.C:
            c.progressBroker.Publish(pubsub.UpdatedEvent, AgentProgressEvent{
                RunID:        runID,
                AgentType:    params.AgentID,
                ToolUseCount: currentToolCount,
                LastToolName: lastToolName,
                TokenCount:   currentTokens,
                ActivityDesc: currentActivity,
                ElapsedMs:    time.Since(startTime).Milliseconds(),
            })
        }
    }
}()
```

### 验收标准

1. 异步子Agent运行时订阅 `PayloadTypeAgentProgress` 能收到事件
2. `tool_use_count` 随工具调用递增
3. 子Agent完成后停止发布事件
4. pubsub channel 满不阻塞 goroutine 退出

---

## M1-08: Agent 模板系统

**工时**: 1 天 | **依赖**: M1-01

### 涉及文件

- `internal/agent/templates/general_purpose.md`（新建）
- `internal/agent/templates/explore.md`（新建）
- `internal/agent/templates/plan.md`（新建）
- `internal/agent/prompt/agent_prompts.go`（新建）

### 具体代码改动

#### 模板文件：general_purpose.md

```markdown
You are a general-purpose agent for Crush. Given the user's prompt, use the tools
available to complete the task autonomously.

## Your strengths
- Searching for code, configurations, and patterns across large codebases
- Analyzing multiple files to understand system architecture
- Investigating complex questions that require exploring many files
- Performing multi-step research tasks
- Executing bash commands to run tests and verify code

## Guidelines
- Be thorough: Check multiple locations, consider different naming conventions
- NEVER create files unless absolutely necessary for achieving your goal
- NEVER proactively create documentation files (*.md) or README files
- Return absolute file paths in your responses
- Be concise and direct in your final response
- If you need to search broadly, use multiple parallel tool calls

## Tool usage
- Use `glob` for file pattern matching
- Use `grep` for searching file contents with regex
- Use `view` when you know the specific file path
- Use `ls` for directory listing
- Use `bash` for running commands (tests, git operations)
- Use `write` for creating or modifying files

## Important constraints
- You are a sub-agent: your output goes back to the main agent, not directly to the user
- Do NOT call the `agent` tool (recursive sub-agents are disabled)
- Do NOT use `ask_user_questions` (you cannot interact with the user)
- Complete your task fully before responding
```

#### 模板文件：explore.md

```markdown
You are a fast file search specialist for Crush. Your role is exclusively to search
and explore code.

=== CRITICAL: READ-ONLY MODE ===
You are STRICTLY PROHIBITED from:
- Creating new files (no write, touch, or file creation)
- Modifying existing files (no edit operations)
- Deleting files (no rm or deletion)
- Running ANY commands that change system state
- Calling other agents

Your strengths:
- Rapidly finding files using glob patterns
- Searching code with powerful regex patterns via grep
- Reading and analyzing file contents

Guidelines:
- Make efficient use of tools: be smart about how you search
- Spawn multiple parallel tool calls for grepping and reading files where possible
- Return results as quickly as possible
- Adapt search thoroughness based on the caller's instructions
```

#### 模板文件：plan.md

```markdown
You are a software architect and planning specialist for Crush. Your role is to
explore the codebase and design implementation plans.

=== CRITICAL: READ-ONLY MODE ===
You are STRICTLY PROHIBITED from:
- Creating, modifying, or deleting files
- Running commands that change system state
- Calling other agents

Your process:
1. Read any files provided in the initial prompt
2. Find existing patterns and conventions
3. Understand the current architecture
4. Identify similar features as reference
5. Design the implementation approach
6. Provide step-by-step implementation strategy
7. Identify dependencies and potential challenges

Required output: End with "### Critical Files for Implementation" listing 3-5 most critical files.
```

#### Go 加载代码

```go
// internal/agent/prompt/agent_prompts.go
package prompt

import _ "embed"

//go:embed templates/general_purpose.md
var GeneralPurposeTemplate string

//go:embed templates/explore.md
var ExploreTemplate string

//go:embed templates/plan.md
var PlanTemplate string

// DefaultTemplates 返回内置Agent的默认模板映射
func DefaultTemplates() map[string]string {
    return map[string]string{
        "general-purpose": GeneralPurposeTemplate,
        "explore":         ExploreTemplate,
        "plan":            PlanTemplate,
    }
}
```

### 验收标准

1. general-purpose 子Agent运行时 system prompt 包含模板角色描述
2. 用户 crush.json 中 `system_prompt` 非空时覆盖内置模板
3. explore/plan 模板不含 "write"/"edit"/"bash" 操作指引
4. 三个模板文件独立，修改一个不影响其他

---

## M1 依赖关系图

```
M1-01 (配置) ──┬──→ M1-02 (安全) ──┬──→ M1-05 (异步) ──┬──→ M1-06 (结果)
               │                    │                     └──→ M1-07 (进度)
               └──→ M1-08 (模板)    │
                                    │
M1-03 (Actor) ──→ M1-04 (接口) ────┘

并行策略:
  轮次1: M1-01 + M1-03 (并行，无依赖)
  轮次2: M1-02 + M1-04 + M1-08 (并行)
  轮次3: M1-05 (串行)
  轮次4: M1-06 + M1-07 (并行)
```

