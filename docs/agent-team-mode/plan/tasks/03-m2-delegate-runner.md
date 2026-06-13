# M2: 并行搜索队 — 开发任务拆分

> 里程碑：M2 | 任务数：6 | 总工时：6 人天 | 建议周期：1.5-2 周
> 目标：DelegateRunner + DelegateRunGroup + 只读ToolPolicy + Delegate UI
> 依赖：M1 的 AgentFactory + TurnRunner + 异步模式 + 全局黑名单

---

## M2-01: DelegateRunner + DelegateRunGroup 核心

**工时**: 2 天 | **依赖**: M1-04, M1-05

### 涉及文件

- `internal/team/delegate_types.go`（新建）
- `internal/team/delegate_runner.go`（新建）

### 具体代码改动

#### delegate_types.go

```go
// internal/team/delegate_types.go
package team

import (
    "sync"
    "charm.land/fantasy"
)

// DelegateTask 描述一个 delegate 要执行的任务
type DelegateTask struct {
    ID      string `json:"id"`      // 唯一标识
    Prompt  string `json:"prompt"`  // 任务描述
    AgentID string `json:"agent_id"` // 使用哪种Agent（"general-purpose" | "explore" | "plan" | 用户自定义）
}

// DelegateResult 单个 delegate 的执行结果
type DelegateResult struct {
    TaskID         string                `json:"task_id"`
    Status         agent.TurnStatus       `json:"status"` // completed | canceled | failed
    Result         *fantasy.AgentResult   `json:"-"`
    ChildSessionID string                 `json:"child_session_id"`
    Error          string                 `json:"error,omitempty"`
    DurationMs     int64                  `json:"duration_ms"`
}

// DelegateRunGroup 管理一组并行delegate的生命周期
// M2 纯内存结构，不写DB
type DelegateRunGroup struct {
    ID      string           `json:"id"`
    Tasks   []DelegateTask   `json:"tasks"`
    Results []DelegateResult `json:"results"`
    Status  string           `json:"status"` // "running" | "partial" | "done" | "canceled"

    mu     sync.Mutex
    wg     sync.WaitGroup
    ctx    context.Context
    cancel context.CancelFunc
}

// RunningCount 返回仍在运行的delegate数量
func (g *DelegateRunGroup) RunningCount() int {
    count := 0
    g.mu.Lock()
    defer g.mu.Unlock()
    for _, r := range g.Results {
        if r.Status == "" { // 尚未完成
            count++
        }
    }
    return count
}

// DoneCount 返回已完成的delegate数量
func (g *DelegateRunGroup) DoneCount() int {
    count := 0
    g.mu.Lock()
    defer g.mu.Unlock()
    for _, r := range g.Results {
        if r.Status == agent.TurnCompleted {
            count++
        }
    }
    return count
}

// FailedCount 返回失败的delegate数量
func (g *DelegateRunGroup) FailedCount() int {
    count := 0
    g.mu.Lock()
    defer g.mu.Unlock()
    for _, r := range g.Results {
        if r.Status == agent.TurnCanceled || r.Status == agent.TurnFailed {
            count++
        }
    }
    return count
}

// TotalCost 返回所有delegate的token总消耗（从Result中提取）
func (g *DelegateRunGroup) TotalTokens() int64 {
    var total int64
    g.mu.Lock()
    defer g.mu.Unlock()
    for _, r := range g.Results {
        if r.Result != nil {
            total += r.Result.TotalUsage.InputTokens + r.Result.TotalUsage.OutputTokens
        }
    }
    return total
}
```

#### delegate_runner.go

```go
// internal/team/delegate_runner.go
package team

import (
    "context"
    "fmt"
    "log/slog"
    "sync"
    "time"

    "charm.land/fantasy"
    "github.com/charmbracelet/crush/internal/actor"
    "github.com/charmbracelet/crush/internal/agent"
    "github.com/google/uuid"
)

// DelegateRunner 管理并行只读delegate的生命周期。
// 每个delegate使用独立SessionAgent实例（通过AgentFactory创建），
// M2只做并行调度，核心执行逻辑完全复用M1的TurnRunner。
type DelegateRunner struct {
    factory agent.AgentFactory
    mu      sync.Mutex
    groups  map[string]*DelegateRunGroup
}

// NewDelegateRunner 创建DelegateRunner
func NewDelegateRunner(factory agent.AgentFactory) *DelegateRunner {
    return &DelegateRunner{
        factory: factory,
        groups:  make(map[string]*DelegateRunGroup),
    }
}

// RunGroup 启动一组并行delegate，立即返回。
// 每个delegate在独立goroutine中执行，结果写入group.Results。
// 当所有delegate完成后，group.Status自动变为"done"。
//
// 约束：
//   - 最大并发数默认3
//   - 纯内存操作，不写DB
//   - delegate之间不互相通信
func (d *DelegateRunner) RunGroup(ctx context.Context, tasks []DelegateTask) *DelegateRunGroup {
    groupCtx, groupCancel := context.WithCancel(ctx)

    group := &DelegateRunGroup{
        ID:      uuid.New().String(),
        Tasks:   tasks,
        Results: make([]DelegateResult, len(tasks)),
        Status:  "running",
        ctx:     groupCtx,
        cancel:  groupCancel,
    }

    d.mu.Lock()
    d.groups[group.ID] = group
    d.mu.Unlock()

    group.wg.Add(len(tasks))

    for i, task := range tasks {
        go func(idx int, t DelegateTask) {
            defer group.wg.Done()
            defer func() {
                if r := recover(); r != nil {
                    slog.Error("delegate panic recovered", "task_id", t.ID, "panic", r)
                    group.mu.Lock()
                    group.Results[idx] = DelegateResult{
                        TaskID: t.ID,
                        Status: agent.TurnFailed,
                        Error:  fmt.Sprintf("panic: %v", r),
                    }
                    group.mu.Unlock()
                }
            }()

            startTime := time.Now()

            // 构建 delegate 的 AgentSpec
            spec := agent.AgentSpec{
                AgentType:      t.AgentID,
                PermissionMode: "default",
                ToolPolicy:     ReadOnlyDelegatePolicy(),
            }

            // 创建独立 runner（不复用coder runner）
            runner, err := d.factory.BuildRunner(groupCtx, spec)
            if err != nil {
                group.mu.Lock()
                group.Results[idx] = DelegateResult{
                    TaskID: t.ID,
                    Status: agent.TurnFailed,
                    Error:  fmt.Sprintf("build runner: %v", err),
                }
                group.mu.Unlock()
                return
            }

            // 执行
            result, err := runner.Run(groupCtx, agent.TeamAgentCall{
                PromptEnvelope: t.Prompt,
                ToolPolicy:     ReadOnlyDelegatePolicy(),
                Actor: actor.ActorContext{
                    // delegate actor context
                },
            })

            duration := time.Since(startTime)

            group.mu.Lock()
            if err != nil {
                group.Results[idx] = DelegateResult{
                    TaskID:     t.ID,
                    Status:     agent.TurnFailed,
                    Error:      err.Error(),
                    DurationMs: duration.Milliseconds(),
                }
            } else {
                group.Results[idx] = DelegateResult{
                    TaskID:         t.ID,
                    Status:         result.Status,
                    Result:         result.Result,
                    DurationMs:     duration.Milliseconds(),
                }
            }
            group.mu.Unlock()

            slog.Debug("delegate finished",
                "group_id", group.ID,
                "task_id", t.ID,
                "status", group.Results[idx].Status,
                "duration_ms", duration.Milliseconds(),
            )
        }(i, task)
    }

    // 后台等待goroutine：全部完成时更新group状态
    go func() {
        group.wg.Wait()
        group.mu.Lock()
        if group.Status == "running" {
            group.Status = "done"
        }
        group.mu.Unlock()

        d.mu.Lock()
        delete(d.groups, group.ID)
        d.mu.Unlock()

        slog.Debug("delegate group finished", "group_id", group.ID, "status", group.Status)
    }()

    return group
}

// ActiveGroupCount 返回当前活跃的delegate组数量
func (d *DelegateRunner) ActiveGroupCount() int {
    d.mu.Lock()
    defer d.mu.Unlock()
    return len(d.groups)
}
```

### 验收标准

1. `RunGroup(ctx, [task1, task2, task3])` 返回非 nil group
2. 3 个 goroutine 并行执行（时间戳验证，三个 delegate 开始时间差值 < 1 秒）
3. 任一 goroutine panic 不影响其他
4. `group.wg.Wait()` 后 `group.Results` 全部填充
5. 纯内存操作——不写 DB、不创建 team tables

### 测试用例

```go
func TestDelegateRunner_ParallelExecution(t *testing.T) {
    factory := &mockAgentFactory{delay: 100 * time.Millisecond}
    runner := NewDelegateRunner(factory)

    tasks := []DelegateTask{
        {ID: "1", Prompt: "task 1", AgentID: "explore"},
        {ID: "2", Prompt: "task 2", AgentID: "explore"},
        {ID: "3", Prompt: "task 3", AgentID: "explore"},
    }

    start := time.Now()
    group := runner.RunGroup(context.Background(), tasks)

    // 等待完成
    time.Sleep(500 * time.Millisecond)

    // 应该比串行快得多（3×100ms 串行=300ms，并行应 <150ms）
    assert.Less(t, time.Since(start), 200*time.Millisecond)
    assert.Equal(t, 3, len(group.Results))
    assert.Equal(t, "done", group.Status)

    // 所有结果状态为completed
    for _, r := range group.Results {
        assert.Equal(t, agent.TurnCompleted, r.Status)
    }
}

func TestDelegateRunner_PanicRecovery(t *testing.T) {
    factory := &mockAgentFactory{panicOnBuild: true}
    runner := NewDelegateRunner(factory)

    group := runner.RunGroup(context.Background(), []DelegateTask{
        {ID: "1", Prompt: "task 1", AgentID: "explore"},
    })

    time.Sleep(200 * time.Millisecond)

    assert.Equal(t, agent.TurnFailed, group.Results[0].Status)
    assert.Contains(t, group.Results[0].Error, "panic")
}
```

---

## M2-02: CancelGroup 统一取消

**工时**: 0.5 天 | **依赖**: M2-01

### 涉及文件

- `internal/team/delegate_runner.go`（修改，扩展方法）

### 具体代码改动

```go
// CancelGroup 取消指定delegate组内的所有活跃delegate。
// 幂等：重复调用不panic。
func (d *DelegateRunner) CancelGroup(groupID string) error {
    d.mu.Lock()
    group, ok := d.groups[groupID]
    d.mu.Unlock()

    if !ok {
        return fmt.Errorf("delegate group %s not found", groupID)
    }

    group.mu.Lock()
    if group.Status == "canceled" {
        group.mu.Unlock()
        return nil // 幂等
    }
    group.Status = "canceled"
    group.mu.Unlock()

    group.cancel() // ctx.Done()触发所有delegate goroutine退出

    return nil
}

// CancelAllGroups 取消所有活跃的delegate组
func (d *DelegateRunner) CancelAllGroups() {
    d.mu.Lock()
    defer d.mu.Unlock()

    for id, group := range d.groups {
        group.mu.Lock()
        if group.Status != "canceled" {
            group.Status = "canceled"
        }
        group.mu.Unlock()
        group.cancel()
        slog.Debug("canceled delegate group", "group_id", id)
    }
}
```

### 验收标准

1. `CancelGroup(groupID)` 后所有 delegate goroutine 在 2 秒内退出
2. 取消后 `group.Results[i].Status` 为 `TurnCanceled`
3. 重复调用 `CancelGroup` 不 panic
4. 所有 goroutine 退出后 `d.groups` 不再含该 group

---

## M2-03: Delegate ToolPolicy 只读过滤

**工时**: 0.5 天 | **依赖**: M1-02, M2-01

### 涉及文件

- `internal/agent/team_call.go`（修改）

### 具体代码改动

```go
// team_call.go 新增

// ReadOnlyDelegatePolicy 返回M2 delegate的只读工具策略。
// 仅允许 view/grep/glob/ls/sourcegraph 五个只读工具。
func ReadOnlyDelegatePolicy() ToolPolicyProfile {
    return ToolPolicyProfile{
        AllowedTools:    []string{"view", "grep", "glob", "ls", "sourcegraph"},
        DisallowedTools: []string{
            "agent", "ask_user_questions", "job_output", "job_kill",
            "todos", "crush_info", "crush_logs",
            "bash", "write", "edit", "multiedit", "download",
            "fetch", "agentic_fetch",
        },
        PermissionMode: "default",
    }
}
```

### 验收标准

1. delegate runner 构建的 agent 仅含 5 个只读工具
2. write/edit/bash/agent 不在 delegate 工具列表中
3. leader coder 工具列表不受影响

---

## M2-04: Delegate UI

**工时**: 2 天 | **依赖**: M2-01

### 涉及文件

- `internal/ui/chat/delegate.go`（新建）
- `internal/ui/model/chat.go`（修改）

### 具体代码改动

```go
// internal/ui/chat/delegate.go
package chat

// DelegateGroupMessageItem 渲染一组并行delegate
type DelegateGroupMessageItem struct {
    *baseToolMessageItem
    Children    []*DelegateChildItem
    GroupStatus string // "running" | "done" | "canceled"
    Expanded    bool
}

type DelegateChildItem struct {
    AgentType      string  // "explore" | "general-purpose" | "plan"
    Status         string  // "running" | "done" | "error" | "canceled"
    ToolUseCount   int
    ActivityDesc   string
    Result         *agent.AgentToolResult
    ChildSessionID string
    DurationMs     int64
}

// RenderDelegateCompact 紧凑模式：一行状态行
// "Delegates  2 running / 1 done  ¥0.04"
func (d *DelegateGroupMessageItem) RenderDelegateCompact(sty *styles.Styles) string {
    running := 0
    done := 0
    for _, c := range d.Children {
        switch c.Status {
        case "running":
            running++
        case "done":
            done++
        }
    }
    return fmt.Sprintf("Delegates  %d running / %d done", running, done)
}

// RenderDelegateExpanded 展开模式：每个child一行
func (d *DelegateGroupMessageItem) RenderDelegateExpanded(sty *styles.Styles) string {
    var lines []string
    for _, c := range d.Children {
        statusIcon := statusIcon(c.Status)
        line := fmt.Sprintf("%s %-16s %-8s %s",
            statusIcon, c.AgentType, c.Status,
            c.ActivityDesc,
        )
        lines = append(lines, line)
    }
    return strings.Join(lines, "\n")
}

func statusIcon(status string) string {
    switch status {
    case "running":   return "●"
    case "done":      return "✓"
    case "error":     return "✗"
    case "canceled":  return "⊘"
    default:          return "○"
    }
}
```

### 验收标准

1. compact 模式显示 delegate 组状态行和 child 指示器
2. Enter 展开后看到每个 child 详情（名称/状态/工具数/耗时）
3. running spinner、done 完成标记、error/canceled 红色标记
4. 选中已完成 child → Enter → 打开 child transcript modal（只读，Esc 关闭）
5. flag off 时不渲染 delegate UI

---

## M2-05: 结果聚合

**工时**: 0.5 天 | **依赖**: M2-01, M1-06

### 涉及文件

- `internal/team/delegate_runner.go`（修改）

### 具体代码改动

```go
// AggregateResults 将一组delegate结果聚合成markdown摘要
func AggregateResults(group *DelegateRunGroup) string {
    var sb strings.Builder

    totalMs := int64(0)
    for _, r := range group.Results {
        totalMs += r.DurationMs
    }

    fmt.Fprintf(&sb, "## Delegate Results (%d delegates, %dms)\n\n", len(group.Results), totalMs)

    for i, r := range group.Results {
        // 找到对应的task
        task := group.Tasks[i]

        fmt.Fprintf(&sb, "### %d. %s (%dms, %d tools)\n", i+1, task.AgentID, r.DurationMs, 0)

        if r.Status == agent.TurnFailed {
            fmt.Fprintf(&sb, "**FAILED**: %s\n", r.Error)
        } else if r.Status == agent.TurnCanceled {
            fmt.Fprintf(&sb, "**CANCELED**\n")
        } else if r.Result != nil {
            content := r.Result.Response.Content.Text()
            if len(content) > 500 {
                content = content[:500] + "..."
            }
            fmt.Fprintf(&sb, "%s\n", content)
        }

        sb.WriteString("\n---\n\n")

        // 总长度限制：2000字符
        if sb.Len() > 2000 {
            sb.WriteString("... (truncated)")
            break
        }
    }

    return sb.String()
}
```

### 验收标准

1. 3 个 delegate 完成后生成结构化 markdown 摘要
2. 摘要中每个 delegate 有关键指标
3. 失败 delegate 标注原因
4. 总长 ≤2000 字符

---

## M2-06: M2 端到端演示脚本

**工时**: 0.5 天 | **依赖**: M2-01 到 M2-05

### 涉及文件

- `scripts/demo-m2.sh`（新建）
- `scripts/demo-m2-task.md`（新建）

### demo-m2-task.md 内容

```markdown
# M2 Delegate 演示脚本

## 场景：同时搜索三个不同方向

1. 启动 crush（确保 AgentTeamPreview=true）
2. 发送 prompt：
   "Search the entire codebase for:
    a) All SQL migration files and their schemas
    b) All HTTP API route definitions
    c) All Go interface definitions
    Use 3 delegates in parallel."

## 预期行为

### T1: Delegate 启动
- 看到 compact delegate item: "Delegates 3 running / 0 done"
- 每个delegate显示 running spinner

### T2: Delegate 执行中
- Enter 展开，看到3个delegate都显示running
- 每个delegate显示当前活动（"grep: CREATE TABLE"等）

### T3: 陆续完成
- 第一个完成 → "Delegates 2 running / 1 done"
- 第二个完成 → "Delegates 1 running / 2 done"
- 全部完成 → group.Status = "done"
- 聚合结果显示在消息中

### T4: 取消场景
- 在delegate running时按 Esc → 出现 cancel prompt
- 确认后所有delegate状态变为canceled

## 验证项
- [ ] 3 delegate并行启动
- [ ] 每个delegate只使用只读工具
- [ ] 展开面板显示正确状态
- [ ] 完成后聚合结果正确
- [ ] 取消功能正常
```

### 验收标准

脚本可手动执行，3 delegate 并行可见，取消正常，聚合正确。

---

## M2 依赖关系图

```
M1-04 (接口) ──┬──→ M2-01 (核心) ──┬──→ M2-02 (取消)
               │                    ├──→ M2-04 (UI)
               │                    └──→ M2-05 (聚合) ──→ M2-06 (演示)
M1-02 (安全) ──┴──→ M2-03 (策略) ──┘
```

