# M4: 长期队员运行 — 开发任务拆分

> 里程碑：M4 | 任务数：13 | 总工时：25 人天 | 建议周期：5-6 周
> 目标：MemberRunner + TeamRunner + Mailbox + Scheduler + PromptEnvelope + UI
> 依赖：M1 (TurnRunner/AgentFactory) + M3 (DB tables/TeamService)

---

## M4-01: MemberRunner 状态机

**工时**: 3 天 | **依赖**: M1 (TurnRunner/AgentFactory), M3 (team_members/team_runs)

### 涉及文件

- `internal/team/member_runner.go`（新建）

### 状态枚举

```go
type MemberState string
const (
    MemberCreated           MemberState = "created"
    MemberStarting          MemberState = "starting"
    MemberIdle              MemberState = "idle"
    MemberQueued            MemberState = "queued"
    MemberRunning           MemberState = "running"
    MemberWaitingPermission MemberState = "waiting_permission"
    MemberBlocked           MemberState = "blocked"
    MemberCancelingTurn     MemberState = "canceling_turn"
    MemberShuttingDown      MemberState = "shutting_down"
    MemberStopped           MemberState = "stopped"
    MemberFailed            MemberState = "failed"
)

// 合法转换表 (from -> []to)
var validTransitions = map[MemberState][]MemberState{
    MemberCreated:           {MemberStarting, MemberStopped},
    MemberStarting:          {MemberIdle, MemberFailed},
    MemberIdle:              {MemberQueued, MemberRunning, MemberBlocked, MemberShuttingDown, MemberStopped},
    MemberQueued:            {MemberRunning, MemberIdle, MemberShuttingDown},
    MemberRunning:           {MemberIdle, MemberWaitingPermission, MemberBlocked, MemberCancelingTurn, MemberFailed, MemberShuttingDown},
    MemberWaitingPermission: {MemberIdle, MemberBlocked, MemberCancelingTurn, MemberShuttingDown},
    MemberBlocked:           {MemberIdle, MemberShuttingDown, MemberStopped},
    MemberCancelingTurn:     {MemberIdle, MemberBlocked, MemberShuttingDown, MemberFailed},
    MemberShuttingDown:      {MemberStopped, MemberFailed},
    MemberStopped:           {},
    MemberFailed:            {MemberStopped},
}
```

### 核心结构体

```go
type WakeSource int
const (
    WakeSourceMailbox    WakeSource = iota // 收到新消息
    WakeSourceTask                         // 分配了新任务
    WakeSourceExplicit                     // leader 显式唤醒
    WakeSourceRecovery                     // 启动恢复
    WakeSourceDependency                   // 依赖任务完成
)

type MemberRunner struct {
    ID        string
    TeamID    string
    Spec      AgentSpec
    State     MemberState
    Version   int64          // CAS version

    factory   agent.AgentFactory
    runner    agent.TurnRunner
    sessionID string
    wakeCh    chan WakeSource

    currentRun   *TeamRun
    mu           sync.Mutex
    ctx          context.Context
    cancel       context.CancelFunc
    flushFn      func(context.Context) error

    svc         TeamService       // 写状态到DB
    maxConcurrentRuns int         // 默认=1
}

func NewMemberRunner(
    id, teamID string,
    spec AgentSpec,
    factory agent.AgentFactory,
    svc TeamService,
    flushFn func(context.Context) error,
) *MemberRunner {
    return &MemberRunner{
        ID:                id,
        TeamID:            teamID,
        Spec:              spec,
        State:             MemberCreated,
        factory:           factory,
        svc:               svc,
        flushFn:           flushFn,
        wakeCh:            make(chan WakeSource, 10),
        maxConcurrentRuns: 1,
    }
}
```

### 生命周期方法

```go
// Start 启动 MemberRunner：加载DB状态→创建TurnRunner→进入Loop
func (m *MemberRunner) Start(ctx context.Context) error {
    m.mu.Lock()
    defer m.mu.Unlock()

    if m.State != MemberCreated {
        return fmt.Errorf("member %s already started (state=%s)", m.ID, m.State)
    }

    m.transitionLocked(MemberStarting)

    // 1. 从DB加载member状态
    member, err := m.svc.GetMember(ctx, m.TeamID, m.ID)
    if err != nil {
        m.transitionLocked(MemberFailed)
        return fmt.Errorf("load member: %w", err)
    }

    // 2. 通过 AgentFactory 创建独立的 TurnRunner
    runner, err := m.factory.BuildRunner(ctx, m.Spec)
    if err != nil {
        m.transitionLocked(MemberFailed)
        return fmt.Errorf("build runner: %w", err)
    }
    m.runner = runner
    m.sessionID = member.SessionID

    // 3. 进入idle，启动主循环
    m.transitionLocked(MemberIdle)

    m.ctx, m.cancel = context.WithCancel(ctx)
    go m.loop()

    return nil
}

// loop 主循环：等待wake→执行turn→回到idle
func (m *MemberRunner) loop() {
    defer m.cancel()

    for {
        select {
        case <-m.ctx.Done():
            return
        case source := <-m.wakeCh:
            m.handleWake(source)
        }
    }
}

// handleWake 处理一次唤醒
func (m *MemberRunner) handleWake(source WakeSource) {
    m.mu.Lock()
    // single-flight gate
    if m.State == MemberRunning || m.State == MemberQueued || m.State == MemberCancelingTurn {
        // 保留 wakeup 到 mailbox/local queue（不 reject）
        m.mu.Unlock()
        return
    }
    m.transitionLocked(MemberQueued)
    m.mu.Unlock()

    // 转换为running
    m.mu.Lock()
    m.transitionLocked(MemberRunning)
    m.mu.Unlock()

    // 执行一轮turn
    startTime := time.Now()
    result, err := m.runner.Run(m.ctx, agent.TeamAgentCall{
        SessionID:      m.sessionID,
        PromptEnvelope: m.buildPrompt(source),
        Actor:          m.actorCtx(),
    })

    // 处理结果
    m.mu.Lock()
    defer m.mu.Unlock()

    if err != nil {
        if errors.Is(err, context.Canceled) {
            m.transitionLocked(MemberIdle)
        } else {
            m.transitionLocked(MemberFailed)
        }
        return
    }

    // 记录 run 结果
    run := TeamRun{
        TeamID:    m.TeamID,
        MemberID:  m.ID,
        SessionID: m.sessionID,
        Status:    RunCompleted,
    }
    if result.Result != nil {
        run.PromptTokens = result.Result.TotalUsage.InputTokens
        run.CompletionTokens = result.Result.TotalUsage.OutputTokens
    }
    m.svc.FinishRun(m.ctx, run)

    m.transitionLocked(MemberIdle)
}

// transitionLocked CAS更新状态（调用方必须持有m.mu）
func (m *MemberRunner) transitionLocked(to MemberState) {
    allowed, ok := validTransitions[m.State]
    if !ok {
        slog.Error("unknown current state", "state", m.State)
        return
    }
    for _, t := range allowed {
        if t == to {
            m.State = to
            // CAS update DB
            go m.svc.UpdateMemberState(context.Background(),
                UpdateMemberStateRequest{MemberID: m.ID, State: string(to), ExpectedVersion: m.Version})
            return
        }
    }
    slog.Error("invalid state transition",
        "member_id", m.ID, "from", m.State, "to", to)
}

func (m *MemberRunner) Wake(source WakeSource) {
    select {
    case m.wakeCh <- source:
    default:
        // channel满了，丢弃或记录
        slog.Warn("member wake channel full", "member_id", m.ID)
    }
}
```

### 验收标准

1. 状态机所有合法转换 table-driven 测试通过
2. Version CAS 冲突返回错误
3. `maxConcurrentRunsPerMember=1` 且 busy 时不会启动第二个 turn
4. `TurnQueued` 是 non-terminal 状态，不写 completed run
5. MemberRunner 不直接调用 `SessionAgent.Run(sessionID, prompt)`

### 高风险标注

**⚠️ 最高风险**: `SessionAgent.Run` 的 `(nil, nil)` queued case 转换逻辑依赖 M0.5 spike 结论。如果 spike 发现无法安全复用，状态机需重构。

---

## M4-02: TeamRunner 生命周期

**工时**: 3 天 | **依赖**: M4-01, M3 (TeamService)

### 涉及文件

- `internal/team/runner.go`（新建）

### 核心接口

```go
type StopMode int
const (
    StopGraceful StopMode = iota // 等待当前turn完成
    StopCancel                    // 取消当前turn后停止
    StopForce                     // 强制停止，不等flush
)

type TeamRunner interface {
    StartTeam(ctx context.Context, teamID string) error
    StopTeam(ctx context.Context, teamID string, mode StopMode) error
    SpawnMember(ctx context.Context, teamID, memberID string, spec AgentSpec) error
    StopMember(ctx context.Context, teamID, memberID string, mode StopMode) error
    CancelMemberTurn(ctx context.Context, req CancelMemberTurnRequest) error
    Status(ctx context.Context, teamID string) (TeamRuntimeStatus, error)
}

type CancelMemberTurnRequest struct {
    TeamID      string
    MemberID    string
    RequestedBy actor.ActorContext
    Reason      string
    Timeout     time.Duration
}

type TeamRuntimeStatus struct {
    TeamID     string
    Members    map[string]MemberRuntimeState
    ActiveRuns int
}

type MemberRuntimeState struct {
    State        MemberState
    CurrentTask  string
    CurrentTool  string
    ActivityDesc string
    CostMicros   int64
}
```

### teamRunner 实现

```go
type teamRunner struct {
    svc     TeamService
    members map[string]*MemberRunner
    factory agent.AgentFactory
    mu      sync.RWMutex
}

func (t *teamRunner) StartTeam(ctx context.Context, teamID string) error {
    // load members from DB
    members, _ := t.svc.ListMembers(ctx, teamID)

    t.mu.Lock()
    defer t.mu.Unlock()

    for _, member := range members {
        if member.Status == MemberStopped || member.Status == MemberFailed {
            continue
        }
        mr := NewMemberRunner(member.ID, teamID, /* spec */, t.factory, t.svc, nil)
        t.members[member.ID] = mr

        go mr.Start(ctx)
    }
    return nil
}

func (t *teamRunner) SpawnMember(ctx context.Context, teamID, memberID string, spec AgentSpec) error {
    // 1. Validate team exists + member can be spawned
    // 2. Create member in DB (通过 TeamService)
    // 3. Create MemberRunner
    // 4. Start()
    // 5. Store in registry
    mr := NewMemberRunner(memberID, teamID, spec, t.factory, t.svc, nil)
    t.mu.Lock()
    t.members[memberID] = mr
    t.mu.Unlock()
    return mr.Start(ctx)
}

func (t *teamRunner) CancelMemberTurn(ctx context.Context, req CancelMemberTurnRequest) error {
    t.mu.RLock()
    mr, ok := t.members[req.MemberID]
    t.mu.RUnlock()
    if !ok {
        return fmt.Errorf("member %s not found", req.MemberID)
    }

    // 1. CAS member -> canceling_turn
    // 2. Write event + audit
    // 3. Call mr.Cancel(sessionID)
    // 4. Wait with timeout
    // 5. Flush
    // 6. Mark run terminal state
    // 7. Publish event
    return mr.Stop(ctx, StopCancel)
}
```

### 验收标准

1. `SpawnMember` 后 goroutine 不泄漏
2. `StopMember` graceful 按顺序执行（cancel→wait→flush→terminal→event）
3. `StopTeam` 对所有 member 执行 shutdown 顺序
4. `CancelMemberTurn` 幂等
5. late Run result 不能覆盖已写入的 `canceled`/`interrupted` terminal state

### 高风险标注

**⚠️ 高风险**: cancel signal、late Run result、heartbeat timeout 三者并发竞争 terminal state。缓解：严格单 writer（MemberRunner 拥有自己的 run）+ version CAS guard + late result 检测 version 变化后只写 audit。

---

## M4-03: Scheduler (claim/wakeup/heartbeat/stale recovery)

**工时**: 2.5 天 | **依赖**: M4-01, M3 (team_tasks/team_runs)

### 涉及文件

- `internal/team/scheduler.go`（新建）
- `internal/team/recovery.go`（新建）

### 核心实现

```go
type SchedulerConfig struct {
    HeartbeatInterval  time.Duration // 默认 30s
    HeartbeatTimeout   time.Duration // 默认 90s
    MaxConcurrentRuns  int           // 默认 3
}

type Scheduler struct {
    db  *sql.DB
    svc TeamService
    cfg SchedulerConfig
}

// ClaimNextTask 原子获取下一个待分配任务
func (s *Scheduler) ClaimNextTask(ctx context.Context, teamID, memberID string) (*TeamTask, error) {
    task, err := s.svc.ClaimNextTask(ctx, ClaimNextTaskRequest{TeamID: teamID, MemberID: memberID})
    if err != nil {
        return nil, err
    }
    return &task, nil
}

// StartHeartbeat 启动心跳goroutine
func (s *Scheduler) StartHeartbeat(ctx context.Context, runID string) (func(), error) {
    ctx, cancel := context.WithCancel(ctx)
    go func() {
        ticker := time.NewTicker(s.cfg.HeartbeatInterval)
        defer ticker.Stop()
        for {
            select {
            case <-ctx.Done():
                return
            case <-ticker.C:
                s.svc.HeartbeatRun(ctx, runID)
            }
        }
    }()
    return cancel, nil // 返回stop函数
}

// RecoverStaleRuns 恢复过期的run
func (s *Scheduler) RecoverStaleRuns(ctx context.Context) (*RecoveryReport, error) {
    cutoff := time.Now().Add(-s.cfg.HeartbeatTimeout)
    stale, err := s.svc.FindStaleRuns(ctx, cutoff)
    // mark interrupted, write audit
    // recovery report统计
    return &RecoveryReport{
        InterruptedRuns: len(stale),
        Details:         stale,
    }, nil
}
```

### 验收标准

1. `ClaimNextTask` 并发安全（SQL atomic）
2. Heartbeat goroutine 正常运行 30s 间隔
3. Heartbeat 过期 run 在 startup 被标记 `interrupted`
4. Active member 无 active run 时恢复为 `idle`

### 中风险

SQLite `UPDATE ... LIMIT 1 RETURNING *` 在并发下非严格 atomic。缓解：使用 CTE WITH 子句 + 单用户场景缓解。

---

## M4-04: Mailbox + Message Receipt

**工时**: 2.5 天 | **依赖**: M3 (M3b tables)

### 涉及文件

- `internal/team/mailbox.go`（新建）
- `internal/team/store_mailbox.go`（新建）

### 数据模型

```go
type RecipientType string
const (
    RecipientDirect    RecipientType = "direct"
    RecipientBroadcast RecipientType = "broadcast"
    RecipientRole      RecipientType = "role"
)

type MessageKind string
const (
    KindMessage        MessageKind = "message"
    KindTaskAssignment MessageKind = "task_assignment"
    KindTaskStatus     MessageKind = "task_status"
    KindShutdownRequest MessageKind = "shutdown_request"
    KindShutdownAck    MessageKind = "shutdown_ack"
)

type MailboxMessage struct {
    ID             string
    TeamID         string
    FromMemberID   string
    FromRole       string
    RecipientType  RecipientType
    ToMemberID     string        // direct
    ToRole         string        // role
    Kind           MessageKind
    CorrelationID  string
    Summary        string
    Payload        json.RawMessage
    CreatedAt      time.Time
}

type MailboxStore interface {
    InsertMessage(ctx, tx, MailboxMessage) error
    GetUnreadMessages(ctx, memberID string) ([]MailboxMessage, error)
    MarkDelivered(ctx, msgID, memberID string) error
    MarkRead(ctx, msgID, memberID string) error
}

// SendMessage 发送消息并唤醒收件人
func SendMessage(ctx context.Context, svc TeamService, msg MailboxMessage, members map[string]*MemberRunner) error {
    // 1. 解析收件人（direct/broadcast/role）
    // 2. Insert message + receipt per recipient (single tx)
    // 3. Wake each recipient MemberRunner
    for _, recipientID := range resolveRecipients(msg, members) {
        if mr, ok := members[recipientID]; ok {
            mr.Wake(WakeSourceMailbox)
        }
    }
    return nil
}
```

### 验收标准

1. Direct: 1 message + 1 receipt + 收件人唤醒
2. Broadcast: 1 message + N receipts + 全部唤醒
3. Role: 正确匹配 role 收件人
4. Delivered/read 状态可追踪
5. Shutdown_request 不直接投给模型（特殊处理）

---

## M4-05: PromptEnvelope Builder

**工时**: 1.5 天 | **依赖**: M4-04, M4-01

### 涉及文件

- `internal/team/prompt_builder.go`（新建）

### Section 优先级（固定顺序，不可调整）

```go
type PromptPriority int
const (
    PrioritySystem   PromptPriority = 1  // 不可截断
    PriorityCritical PromptPriority = 1  // 不可截断
    PriorityHigh     PromptPriority = 2
    PriorityMedium   PromptPriority = 3
    PriorityLow      PromptPriority = 4
    PriorityLowest   PromptPriority = 5
)

func (pb *PromptBuilder) Build() (string, error) {
    sections := []struct {
        name     string
        content  string
        priority PromptPriority
    }{
        // 1. System/developer/tool policy (不可截断)
        {"system_policy", pb.systemPolicy(), PrioritySystem},
        // 2. Member identity (不可截断)
        {"member_identity", pb.memberIdentity(), PriorityCritical},
        // 3. Current task full text (不可截断)
        {"current_task", pb.taskText(), PriorityCritical},
        // 4. Direct unread messages (UNTRUSTED)
        {"direct_messages", pb.directMessages(), PriorityHigh},
        // 5. Dependency result summaries
        {"dependency_results", pb.dependencyResults(), PriorityMedium},
        // 6. Leader latest instruction
        {"leader_instruction", pb.leaderInstruction(), PriorityMedium},
        // 7. Broadcast/role messages (UNTRUSTED)
        {"broadcast_messages", pb.broadcastMessages(), PriorityLow},
        // 8. Own session summary
        {"session_summary", pb.sessionSummary(), PriorityLowest},
        // 9. Reporting rules
        {"reporting_rules", pb.reportingRules(), PriorityCritical},
    }

    // 拼接并处理overflow
    result := pb.concat(sections)
    if exceedsContextWindow(result) {
        result = pb.truncate(result)
    }
    return result, nil
}
```

### 验收标准

1. Section order 可单测
2. Task full text 不截断
3. Direct before broadcast
4. Overflow 截断低优先级而非 task/system
5. Task 不可截断时 `Build()` 返回 `ErrContextOverflow`
6. Mailbox 内容标记 UNTRUSTED PEER INPUT

---

## M4-06: Member Tools

**工时**: 1.5 天 | **依赖**: M4-04

### 涉及文件

- `internal/team/tools.go`（新建）

```go
// team_report_status tool
// Member 更新自己的 task 状态 + 唤醒 leader
// CAS update: version 冲突时重试一次，仍冲突则 blocked

// team_send_message tool
// Member 发送消息给 leader/其他 member/广播
// 限制：member 只能发 message/task_status 类型
// 限制：member 不能 shutdown 其他 member
```

### 验收标准

1. Member `team_report_status` 更新 task 状态
2. Member `team_send_message` direct/broadcast/role 可达
3. Member 不能更新别的 member 的 task
4. Actor policy 检查正确

---

## M4-07 ~ M4-13: Shutdown、Recovery、UI、Session Links、Task Deps、Cost、E2E

由于篇幅限制，以下任务概要列出：

| 任务 | 工时 | 核心产出 |
|------|------|----------|
| M4-07 Shutdown 顺序 | 2.0d | 9步shutdown序列 (stopWakeups→event→cancel→wait→flush→terminal→stopped→event→publish) |
| M4-08 Stale Recovery | 1.5d | StartupRecovery、InterruptedReason、usage_status partial/unknown |
| M4-09 Compact Team UI | 2.0d | TeamCompactItem + ExpandedTeamView + 24行约束 |
| M4-10 Session Links | 1.0d | team_session_links 表 + RecoverMemberSession |
| M4-11 Task Deps | 1.5d | blocks/blockedBy + 循环依赖检测DFS + OnTaskCompleted级联wake |
| M4-12 Cost Accounting | 1.0d | UsageRecord、final/partial/unknown、UI "---" 对 unknown |
| M4-13 M4 E2E | 2.0d | 6个E2E场景 (spawn/message/report/cancel/shutdown/recovery) |

---

## M4 依赖关系图

```
M4-01 (MemberRunner) ──┬──→ M4-02 (TeamRunner) ──→ M4-09 (UI)
                        ├──→ M4-03 (Scheduler) ──→ M4-08 (Recovery)
                        ├──→ M4-04 (Mailbox) ──→ M4-05 (Envelope)
                        │       └──→ M4-06 (Tools)
                        ├──→ M4-07 (Shutdown)
                        ├──→ M4-10 (SessionLinks)
                        ├──→ M4-11 (TaskDeps)
                        ├──→ M4-12 (Cost)
                        └──→ M4-13 (E2E) ← 依赖所有
```
