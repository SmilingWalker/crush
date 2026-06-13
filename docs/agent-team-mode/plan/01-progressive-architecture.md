# SubAgent → AgentTeam 递进架构

> 分析日期：2026-06-04
> 问题：SubAgent 改进方案能否作为 AgentTeam 的第一阶段？
> 结论：可以。M1 SubAgent 单兵地基 = 统一的 AgentTeam M1。后续 M2-M7 每层只做增量。

---

## 一、递进模型

```
┌──────────────────────────────────────────────────────────┐
│                                                          │
│  M1: SubAgent 单兵地基（3-4 周）                          │
│  ┌────────────────────────────────────────────────────┐  │
│  │ ActorContext │ AgentFactory │ TurnRunner │ Agent配置│  │
│  │ "谁在干活"    │ "创建工人"    │ "执行任务"  │ "定义工人"│  │
│  │ ToolPolicy   │ 全局黑名单    │ 异步+Kill   │ 进度回调 │  │
│  │ "能用啥工具"  │ "永远不能碰"  │ "不卡界面"  │ "看得见" │  │
│  └────────────────────────────────────────────────────┘  │
│                         │                                │
│         ┌───────────────┼───────────────┐                │
│         ▼               ▼               ▼                │
│       M2              M4              M6                  │
│   并行搜索队        长期队员         安全补丁              │
│   只加并行调度      只加mailbox     只加patch             │
│                    + scheduler     + apply               │
│                                                          │
│   每一层只做自己那一点增量，                                │
│   核心的"让一个工人执行一轮任务"永远调用 M1 的 TurnRunner    │
└──────────────────────────────────────────────────────────┘
```

---

## 二、为什么原来的 AgentTeam 方案需要调整

### 原来的 M0.5→M1 之间有一个空白

```
AgentTeam 原有路线：
  M0.5  hidden spike  ──→  代码扔掉  ──→  M1 从零写 DelegateRunner
       
问题：spike 验证了"SessionAgent 可以隔离运行"，
     但没有产出正式接口。M1 边写 DelegateRunner 边设计
     TurnRunner / AgentFactory / ToolPolicy。
     接口设计不好，M3 又要重写。
```

### 插入 M1 后

```
调整后的路线：
  M0.5  hidden spike  ──→  M1 产品化  ──→  M2 DelegateRunner 直接用
  
M1 产出：
  ActorContext     ──→ M2/M3/M4/M5 全部用
  AgentFactory     ──→ M2 Delegate、M4 Member 都用它创建 runner
  TurnRunner       ──→ 所有层执行一轮 turn 的唯一接口
  ToolPolicy       ──→ M2/M3/M4 复用的工具策略
  Agent 配置       ──→ 用户定义的 Agent 类型，全部层可用
  全局黑名单       ──→ 全部 Agent 类型共享

M2 只加：并行调度 + cancel group + Delegate UI
M4 只加：mailbox + scheduler + 状态机 + 长期生命周期
```

---

## 三、三层递进——用代码说明

### M1：单兵执行

```go
// M1 的接口
runner := factory.BuildRunner(ctx, spec)
result := runner.Run(ctx, TeamAgentCall{
    Actor:      actorCtx,
    ToolPolicy: readOnlyPolicy,
})

// 一次一个人，同步或异步
```

### M2：并行搜索队

```go
// M2 只加了并行调度
type DelegateRunner struct {
    factory agent.AgentFactory  // ← M1 的
}

func (d *DelegateRunner) RunGroup(tasks []Task) []Result {
    for _, task := range tasks {
        go func() {
            runner := d.factory.BuildRunner(ctx, task.Spec)  // ← M1 的
            result := runner.Run(ctx, task.ToAgentCall())     // ← M1 的
            results <- result
        }()
    }
    return aggregate(results)
}
// 增量：goroutine + channel + waitgroup + cancel group
```

### M4：长期队员

```go
// M4 只加了生命周期和邮箱
type MemberRunner struct {
    factory  agent.AgentFactory   // ← M1 的
    mailbox  chan Message         // ← M4 新加的
    state    MemberState          // ← M4 新加的
}

func (m *MemberRunner) Loop() {
    for {
        msg := <-m.mailbox
        m.state = Running
        
        runner := m.factory.BuildRunner(ctx, m.spec)  // ← M1 的
        result := runner.Run(ctx, msg.toAgentCall())    // ← M1 的
        
        m.handleResult(result)
        m.state = Idle
    }
}
// 增量：mailbox + scheduler + 状态机 + heartbeat + recovery
```

---

## 四、Agent 配置系统的递进

### M1 定义 Agent 结构体

```go
type Agent struct {
    ID              string              `json:"id"`
    Name            string              `json:"name"`
    Description     string              `json:"description"`
    Model           SelectedModelType   `json:"model"`
    AllowedTools    []string            `json:"allowed_tools"`
    DisallowedTools []string            `json:"disallowed_tools,omitempty"`   // M1 新增
    SystemPrompt    string              `json:"system_prompt,omitempty"`      // M1 新增
    PermissionMode  string              `json:"permission_mode,omitempty"`    // M1 新增
    AllowedMCP      map[string][]string `json:"allowed_mcp,omitempty"`
    ContextPaths    []string            `json:"context_paths,omitempty"`
    Disabled        bool                `json:"disabled,omitempty"`

    // M2-M4 预留字段（只定义 schema，M1 暂不使用）
    MaxTurns   int      `json:"max_turns,omitempty"`
    Skills     []string `json:"skills,omitempty"`
    McpServers []string `json:"mcp_servers,omitempty"`
}
```

### M1 内置 4 个 Agent

| Agent | 工具集 | 模型 | 用途 |
|-------|--------|------|------|
| `coder` | 全部 | large | 主Agent |
| `general-purpose` | view/grep/glob/ls/bash/write | large | 通用子Agent |
| `explore` | view/grep/glob/ls/sourcegraph | small | 快速搜索 |
| `plan` | view/grep/glob/ls | small | 只读规划 |

### M2 用户可以用 M1 的配置定义自己的 Agent

```json
{
  "agents": {
    "code-reviewer": {
      "model": "small",
      "allowed_tools": ["view", "grep", "glob", "bash"],
      "disallowed_tools": ["write", "edit", "agent"],
      "system_prompt": "你是代码安全审查专家..."
    }
  }
}
```

M2 DelegateRunner 可以通过 `spec.AgentType = "code-reviewer"` 使用用户定义的 Agent。

---

## 五、关键设计决策

### 决策 1：TurnRunner 是唯一的执行接口

所有层都通过 `TurnRunner.Run()` 执行 agent turn。不绕开，不直接调 `SessionAgent.Run`。

### 决策 2：AgentFactory 保证实例隔离

每个 runner 持有独立 `SessionAgent` 实例。不复用 `Coordinator.currentAgent`。coder 的 `SetTools`/`SetModels` 不影响 delegate/member。

### 决策 3：ActorContext 逐层丰富

```
M1: SessionID + MessageID + ToolCallID + ParentSessionID
M2: + WorkspaceID
M3: + TeamID + MemberID + MemberName
M4: + TaskID + RunID
```

字段定义在 M1 的 `internal/actor` 包中，后续层只填充不使用的新字段。

### 决策 4：工具权限逐层放宽

```
M1: read-only allow-list + 全局黑名单
M2: 同 M1，只读搜索
M3: 同 M1，只读队员
M4: + PermissionBridge（可写，需审批）
M5: + Patch artifact（写文件，需 leader review）
```

---

## 六、完整里程碑序列

```
M1   SubAgent 单兵地基          3-4 周
     ├─ ActorContext + AgentFactory + TurnRunner
     ├─ Agent 配置（可序列化 + 4 内置 + 用户自定义）
     ├─ 全局黑名单 + 递归防护
     ├─ 异步执行 + Kill + 进度回调
     └─ 结构化返回结果

M2   并行搜索队                 3-4 周（累计 6-8 周）
     ├─ DelegateRunner + DelegateRunGroup
     ├─ CancelGroup
     └─ Delegate UI（compact item）

M3   团队数据领域               3-4 周（累计 9-12 周）
     ├─ 7 张 DB 表
     ├─ TeamService + TeamSnapshot
     └─ Debug UI

M4   长期队员运行               4-6 周（累计 13-18 周）
     ├─ MemberRunner 状态机
     ├─ Mailbox + Scheduler
     ├─ PromptEnvelope
     └─ Compact team item UI

M5   权限审批                   3-4 周（累计 16-22 周）
     ├─ PermissionBridge
     ├─ Scoped Grant (call/task/session)
     └─ Audit + Permission UI

M6   安全补丁                   4-5 周（累计 20-27 周）
     ├─ Patch Artifact + ContentStore
     ├─ Apply Service（base hash + atomic write）
     └─ Patch Review/Apply UI

M7   高级隔离                   远期
     ├─ Worktree Runtime
     ├─ Process Backend
     └─ A2A Gateway
```

---

## 七、与 AgentTeam 方案的对应关系

| 本文档 | 原 AgentTeam 方案 | 变化 |
|--------|-------------------|------|
| M1 | **新增** | SubAgent 单兵地基，包含 Agent 配置系统 |
| M2 | M1（只读并行） | 完全复用 M1 基础设施，只加并行调度 |
| M3 | M2（DB 领域） | 不变 |
| M4 | M3（长期队员） | 不变 |
| M5 | M4（权限审批） | 不变 |
| M6 | M5（安全补丁） | 不变 |
| M7 | M6（高级隔离） | 不变 |

---

> **关键变化**：M1 插入了 SubAgent 单兵地基层，补充了原方案缺失的 Agent 配置系统、
> 统一执行接口和全局安全底座。后续 M2-M7 完全复用 M1 产出，每层只做增量。
