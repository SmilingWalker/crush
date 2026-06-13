# M7: 高级隔离 — 开发任务拆分

> 里程碑：M7 | 任务数：6 | 总工时：6.5 人天 | 状态：远期规划（概要级）
> 依赖：M6 (Patch) + M4 (MemberRunner)

---

## M7-01: Runtime Adapter Interface

**工时**: 1 天

```go
type RuntimeKind string
const (
    RuntimeInProcess RuntimeKind = "in_process" // M4 默认行为
    RuntimeWorktree  RuntimeKind = "worktree"   // git worktree 隔离
    RuntimeProcess   RuntimeKind = "process"    // 独立进程
    RuntimeA2A       RuntimeKind = "a2a"        // 外部agent网关
)

type RuntimeBackend interface {
    ID() string
    Kind() RuntimeKind
    Start(ctx context.Context, spec RuntimeSpec) (RuntimeHandle, error)
    Stop(ctx context.Context, handle RuntimeHandle, mode StopMode) error
    Status(ctx context.Context, handle RuntimeHandle) (RuntimeStatus, error)
    ForwardToolCall(ctx context.Context, handle RuntimeHandle, call ToolCall) (ToolResponse, error)
}

type RuntimeSpec struct {
    MemberID      string
    TeamID        string
    WorkspaceID   string
    BaseRef       string            // worktree: git ref
    Env           map[string]string
    Policy        RuntimePolicy
}

type RuntimePolicy struct {
    AllowNetwork  bool
    AllowWrite    bool
    MaxMemoryMB   int
    MaxDiskMB     int
    TimeoutSeconds int
}
```

InProcessBackend 保持 M4 行为不变。

---

## M7-02 ~ M7-06

| 任务 | 工时 | 核心产出 |
|------|------|----------|
| M7-02 Worktree Backend | 1.5d | git worktree add/remove + 冲突检测 + disk quota |
| M7-03 Process Backend | 1.5d | 独立 crush child process + JSON-RPC IPC + SIGTERM→SIGKILL |
| M7-04 Crash Recovery | 1.0d | CrashDetector per 30s + crash→interrupted+draft+cleanup |
| M7-05 A2A Gateway | 1.0d | AgentCard + Crush<->A2A 映射 + auth延后 |
| M7-06 Advanced UI | 0.5d | backend 列显示 + Experimental.AgentTeamAdvanced flag |

---


