# 00 全局技术方案

## 一句话方案

AgentTeam Mode 在 `crush` 内新增一个独立 `internal/team` 领域层。leader agent 通过
`team_*` tools 管理 team、teammate、task、mailbox 和 artifact；teammate 仍然使用
现有 `SessionAgent` 执行单轮模型调用，但生命周期、任务、消息、权限和审计由 team
domain 统一管控。

## 当前代码约束

现有代码已经具备一些基础，但不能直接等价为 team mode：

| 现有能力 | 当前位置 | 可复用点 | 不足 |
| --- | --- | --- | --- |
| 默认 coder agent | `internal/agent/coordinator.go` | 构建模型、工具、prompt 的入口 | `currentAgent` 语义是 singleton |
| session 并发队列 | `internal/agent/agent.go` | 同一 `SessionAgent` 可承载多个 session turn | `messageQueue` 是内存 prompt queue，不是 mailbox |
| `agent` tool | `internal/agent/agent_tool.go` | 可创建 child session、跑一次 sub-agent | 同步 one-shot，不是长期 teammate |
| session parent | `internal/session/session.go` | child session 不进入普通 session list | 没有 team/member/task identity |
| permission service | `internal/permission/permission.go` | 已有用户审批和持久授权 | 缺 team/member/task/run 归因和 scoped grant |
| pubsub/SSE/client | `internal/pubsub`, `internal/server/events.go`, `internal/client/proto.go` | 可传输新事件 | 不是可靠事实源，缺 TeamEvent replay |
| TUI chat tree | `internal/ui/model`, `internal/ui/chat` | 可展示 parent/child 和 permission | 缺 team roster、member 状态、mailbox、patch review |

## 目标形态

```text
app/backend/server
  -> workspace.TeamWorkspace
  -> team.Service
       -> SQLite/sqlc tables
       -> TeamEvent outbox
       -> audit
  -> team.TeamRunner
       -> team.MemberRunner
            -> agent.TurnRunner(SessionAgent)
            -> team mailbox/task prompt builder
            -> permission bridge
  -> TUI Team panel / compact team item
```

## 端到端运行链路

AgentTeam Mode 的核心不是“多开几个 agent”，而是把 leader、teammate、任务、消息、
权限、审计和 UI 状态纳入同一个可恢复的控制面。

### 1. leader 创建 team

```text
leader assistant turn
  -> team_create tool
  -> team.Service.CreateTeam transaction
       -> teams row
       -> team_events row(seq=1)
       -> team_audit_events row
  -> workspace publishes TeamEvent notification
  -> TUI reloads TeamSnapshot if needed
```

结果：

- DB 中有 durable team。
- leader session 与 team 建立 link。
- TUI 可以在重连后从 snapshot 恢复，而不是依赖内存事件。

### 2. leader 创建 teammate

```text
team_spawn_member tool
  -> TeamService.SpawnMember
  -> AgentRegistry registers member identity
  -> TeamRunner starts MemberRunner(member_id)
  -> MemberRunner creates/owns one child session
  -> member state: created -> starting -> idle
```

结果：

- teammate 是长期成员，不是一次性 `/agent` sub-agent。
- member 有独立 session、状态、预算、当前 task/run。
- old coder 的 `currentAgent` 语义不改变。

### 3. leader 分配任务或发送消息

```text
team_create_task / team_send_message
  -> DB transaction writes task/mailbox/event/audit
  -> Scheduler observes queued work
  -> MemberRunner wakeup
  -> PromptEnvelope = system constraints + team context + task + unread mailbox
  -> agent.TurnRunner.Run(one turn)
  -> member writes status/message/artifact through team tools
```

结果：

- leader 和 teammate 不直接共享完整上下文。
- teammate 看到的是受控 prompt envelope。
- peer message 永远按 untrusted model input 处理。

### 4. teammate 需要权限或产生写作业

```text
tool call from member
  -> ActorContext(team/member/task/run/tool)
  -> ToolPolicy decides allow/deny/ask
  -> PermissionBridge creates team-aware permission request
  -> UI shows actor + scope + resource
  -> grant/deny writes audit
```

M5 前，teammate 不直接写主工作区。需要修改文件时：

```text
M3.5: teammate -> change_proposal artifact -> leader review/revise/discard
M5:   teammate -> patch artifact -> leader review -> apply/reject -> audit
```

## 代码落点

| 模块 | 新增/调整 | 说明 |
| --- | --- | --- |
| `internal/team` | 新包 | domain model、Service、TeamRunner、MemberRunner、tools、permission bridge |
| `internal/db/migrations` | 新 migration | `teams`、`team_members`、`team_tasks`、`team_runs`、`team_events`、audit 等 |
| `internal/db/sql` | 新 sqlc queries | task CAS、atomic claim、snapshot、event replay、audit 查询 |
| `internal/agent` | 窄接口抽取 | 暴露 `TurnRunner` / `AgentFactory`，避免 team 直接控制 `currentAgent` |
| `internal/permission` | 扩展 request source | 增加 team/member/task/run/tool actor 字段与 scoped grant |
| `internal/workspace` | 新 TeamWorkspace contract | local/client/server 都要支持 team API |
| `internal/server` | 新 `/v1/workspaces/{id}/teams/*` routes | REST API 与 SSE event payload |
| `internal/client` | 新 team client | server mode 与 local mode 行为一致 |
| `internal/pubsub` | 新 TeamEvent payload | 只做通知，不做事实源 |
| `internal/ui` | Team compact item、permission、patch review | 每个阶段有对应 UI，不做一次性大 dashboard |

## Feature flag 契约

AgentTeam Mode 必须默认关闭，并且 flag-off 时 old `/agent`、默认 coder、TUI、server/client API
和 prompt 都不能出现 team 行为。代码库当前没有 `experimental` 配置结构，因此 PR-0 必须先新增
明确字段，而不是在各模块里散落 bool：

```go
type ExperimentalOptions struct {
    AgentTeamPreview bool `json:"agent_team_preview,omitempty"`
    AgentTeam        bool `json:"agent_team,omitempty"`
}

type Options struct {
    // existing fields...
    Experimental *ExperimentalOptions `json:"experimental,omitempty"`
}
```

规则：

- `AgentTeamPreview` 控制 M1 read-only delegates 和隐藏 spike 可见入口。
- `AgentTeam` 控制 M2+ durable team API、TUI、tools、runtime。
- 两个 flag 默认 false；`Experimental == nil` 等价于全部 false。
- team tools、leader prompt 注入、TUI team item、server team routes、client team methods 都必须读取同一
  gate helper，不能各自解析配置。
- flag-off 的 team endpoint 返回稳定 `feature_disabled`，不创建 DB row、不发 SSE。
- preview flag 不能自动打开可写能力；M5 patch write 只能在 `AgentTeam=true` 且阶段代码已启用时出现。

## 核心模块

### `internal/team`

新增正式领域包，负责：

- team/member/task/run/artifact/mailbox/audit/event model。
- `Service` 事务接口。
- DB-backed mailbox、task board、artifact store。
- `TeamRunner` 和 `MemberRunner` 生命周期。
- `team_*` tool runtime。
- permission bridge。
- debug snapshot。

### `internal/agent`

保持默认 coder 行为稳定，只暴露窄接口给 team。`TeamAgentCall` 是 team runtime 调用 agent
的适配层契约，不能改写或重载现有 `internal/agent.SessionAgentCall`：

```go
// internal/agent/team_call.go
type TurnRunner interface {
    Run(ctx context.Context, call TeamAgentCall) (TurnRunResult, error)
    Cancel(sessionID string)
    IsSessionBusy(sessionID string) bool
}

type AgentFactory interface {
    BuildTeamRunner(ctx context.Context, spec AgentSpec) (TurnRunner, error)
}

type RuntimeMessageFlusher interface {
    FlushAll(ctx context.Context) error
}
```

`TeamAgentCall` 是 team runtime 调用 agent 的唯一契约。它不能与现有
`internal/agent.SessionAgentCall` 同名，因为现有结构已被 `SessionAgent.Run` 直接消费。
M0.5 spike 必须实现 `TeamAgentCallAdapter`，把 team 语义转换成现有 `SessionAgentCall`。

`AgentFactory` 的冻结语义：

- 每个 delegate/member runner 必须持有独立的 `SessionAgent` 实例；不能返回或复用
  `Coordinator.currentAgent`。
- `SessionAgent` 实例内持有 `tools`、`largeModel/smallModel`、`messageQueue` 和
  `activeRequests`。这些字段不是按 session 隔离的共享资源；共享实例会导致工具、模型、队列和
  cancel 状态互相污染。
- coder 的 `SetTools` / `SetModels` 只能影响 coder runner；delegate/member 的工具和模型策略在
  `BuildTeamRunner` 时按 `AgentSpec` 固化。
- `FlushAll` 不属于现有 `SessionAgent` 接口；team runtime 需要 flush 时通过注入的
  `RuntimeMessageFlusher` / message service 完成，不能假设 agent runner 自带 flush 能力。
- `AgentFactory` 可以复用现有模型、工具、prompt 构建逻辑，但必须以受限 tool policy 和
  instructions policy 创建新的 runner；不得让 team runtime 访问 coordinator private fields。

```go
type TeamAgentCall struct {
    SessionID      string
    ParentSessionID string
    PromptEnvelope string
    Actor          actor.ActorContext
    ToolPolicy     ToolPolicyProfile
    StreamSink      AgentStreamSink
}

type TeamAgentCallAdapter interface {
    ToSessionAgentCall(ctx context.Context, call TeamAgentCall) (SessionAgentCall, error)
}

type TurnStatus string

const (
    TurnCompleted TurnStatus = "completed"
    TurnQueued    TurnStatus = "queued"
    TurnCanceled  TurnStatus = "canceled"
    TurnFailed    TurnStatus = "failed"
)

type TurnRunResult struct {
    Status TurnStatus
    Result *fantasy.AgentResult
}
```

现有 `SessionAgent.Run` 在 session busy 时可能返回 `(nil, nil)` 表示已入队。adapter 必须把
这个 case 转成 `TurnRunResult{Status: TurnQueued}` 或等价 sentinel，禁止把 `(nil, nil)`
当作 completed。

更重要的是，team runtime 不把 `SessionAgent.messageQueue` 当作 team mailbox 或 run
lifecycle 来源。`MemberRunner` 默认 `max_concurrent_runs_per_member=1`，调用 `Run` 前先用
runner 自己的状态和 `IsSessionBusy(sessionID)` 做 single-flight gate；busy 时保留 wakeup/mailbox
状态，返回 queued/busy sentinel，不启动第二个 team run。若 adapter 仍收到 `(nil, nil)`，该
turn 只能停留在 non-terminal queued 状态；M0.5 必须证明该 case 不会写 completed run。若未来
确实要复用 `SessionAgent` 内部队列，必须先给 runner 增加 queued-start lifecycle callback，
不能靠递归 `Run` 的内部实现推断后续开始时间。

不要让 `Coordinator.currentAgent` 直接管理 team lifecycle。

`TurnRunner.Cancel(sessionID)` 只是向对应 runner 发送 cancel signal 的轻量 primitive。它不负责写
team run/member state，也不负责 audit/event。team 级 cancel 必须由
`TeamRunner.CancelMemberTurn` 编排，按 `05-runtime-control-plane.md` 的顺序写状态、调用 runner、
等待/flush 并落 terminal state。

### `internal/workspace` / `internal/server` / `internal/client`

新增 team 专用接口和 API：

```text
POST   /v1/workspaces/{id}/teams
GET    /v1/workspaces/{id}/teams
GET    /v1/workspaces/{id}/teams/{team_id}
POST   /v1/workspaces/{id}/teams/{team_id}/members
POST   /v1/workspaces/{id}/teams/{team_id}/messages
POST   /v1/workspaces/{id}/teams/{team_id}/tasks
PATCH  /v1/workspaces/{id}/teams/{team_id}/tasks/{task_id}
GET    /v1/workspaces/{id}/teams/{team_id}/snapshot
```

server/client mode 必须和 local mode 同步支持，不能只实现 `AppWorkspace`。
request/response/error/SSE 的 issue-ready contract 见 `04-team-domain-data-contract.md`。

`Workspace` 现有接口已经承载 session、agent、permission、MCP、config 等大量职责。team 方法不直接追加到
这个大接口；先新增独立 `TeamWorkspace`，由 local `AppWorkspace` 和 remote `ClientWorkspace` 同时实现，
再由 UI/API wiring 在需要 team mode 的地方组合使用：

```go
type TeamWorkspace interface {
    CreateTeam(ctx context.Context, req proto.CreateTeamRequest) (proto.TeamSnapshot, error)
    ListTeams(ctx context.Context, req proto.ListTeamsRequest) (proto.ListTeamsResponse, error)
    GetTeamSnapshot(ctx context.Context, workspaceID, teamID string) (proto.TeamSnapshot, error)
    SpawnTeamMember(ctx context.Context, req proto.SpawnTeamMemberRequest) (proto.TeamMember, error)
    SendTeamMessage(ctx context.Context, req proto.SendTeamMessageRequest) (proto.TeamMailboxMessage, error)
    CreateTeamTask(ctx context.Context, req proto.CreateTeamTaskRequest) (proto.TeamTask, error)
    UpdateTeamTask(ctx context.Context, req proto.UpdateTeamTaskRequest) (proto.TeamTask, error)
    ListTeamEventsAfter(ctx context.Context, workspaceID, teamID string, afterSeq int64, limit int) (proto.TeamEventsResponse, error)
}
```

`AppWorkspace` 直接调用 `team.Service`；`ClientWorkspace` 只做 HTTP/proto 翻译。两者都必须走
`feature gate -> workspace auth -> actor validation -> team.Service` 的同一顺序。

## 功能分层

| 层 | 负责什么 | 第一版能力 |
| --- | --- | --- |
| Runtime Control Plane | agent 创建、运行、取消、恢复、heartbeat | M0.5/M3 |
| Collaboration Data Plane | team DB、mailbox、task、artifact、event | M2/M3 |
| Safety & Isolation Plane | actor identity、permission、audit、tool policy | M1/M4 |
| Product Integration Plane | TUI、debug snapshot、成本、E2E | M1-M5 |

## 非目标

- M1 不做长期 teammate。
- M1 不做 teammate peer chat。
- M1-M3 不开放 teammate 写文件、bash、MCP write。
- M3 不实现完整 permission wait/approval。
- M5 前不允许 teammate direct write 主工作区。
- M6 前不接 A2A 作为内部协议。

## 实施口径

实现、评审、验收和外部知识库同步均以本目录为准。历史材料不作为实现输入。
