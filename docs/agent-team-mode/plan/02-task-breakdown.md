# Crush SubAgent → AgentTeam 完整开发任务拆分

> 产出日期：2026-06-04
> 总任务数：62 | 总工时：89 人天

---

## M1: SubAgent 单兵地基（8 任务 / 9 人天）

### M1-01: Agent 配置结构体扩展 + 可序列化 + 4 个内置 Agent
- **文件**: `internal/config/config.go` (修改)
- **具体改动**:
  - 移除 `Agents map[string]Agent` 的 `json:"-"` 标签 → `json:"agents"`
  - Agent 结构体新增: `DisallowedTools []string`, `SystemPrompt string`, `PermissionMode string`
  - 预留字段: `MaxTurns int`, `Skills []string`, `McpServers []string`
  - 重写 `SetupAgents()`: 4 内置 → 用户配置覆盖/新增
  - 新增常量: `AgentGeneralPurpose`, `AgentExplore`, `AgentPlan`
- **依赖**: 无
- **验收**: crush.json 可序列化; 用户配置覆盖内置; 自定义 Agent 可新增; golden file 测试通过
- **工时**: 1.5d

### M1-02: 全局安全底座 — 黑名单 + 递归深度防护 + buildTools 过滤
- **文件**: `internal/agent/tools/disallowed.go` (新建), `internal/agent/coordinator.go` (修改), `internal/agent/agent_tool.go` (修改)
- **具体改动**:
  - `SubAgentDisallowedTools = []string{"agent","ask_user_questions","job_output","job_kill","todos","crush_info","crush_logs"}`
  - 递归深度 context key + `withRecursionDepth`/`getRecursionDepth`，上限=3
  - `buildTools`: isSubAgent=true 时过滤 DisallowedTools + SubAgentDisallowedTools
- **依赖**: M1-01
- **验收**: 嵌套超 3 层返回 error; general-purpose 无 ask_user_questions 等; coder 不触发黑名单
- **工时**: 1d

### M1-03: ActorContext 身份系统
- **文件**: `internal/actor/actor.go` (新建)
- **具体改动**:
  - `ActorContext` 结构体: SessionID / ParentSessionID / MessageID / ToolCallID / WorkspaceID
  - 预留: TeamID/MemberID/MemberName/TaskID/RunID
  - context key: `actorContextKey`, `WithContext`/`FromContext`
- **依赖**: 无
- **验收**: context 往返存取正确; FromContext 返回 false 当未注入
- **工时**: 0.5d

### M1-04: TurnRunner + AgentFactory 接口定义
- **文件**: `internal/agent/team_call.go` (新建)
- **具体改动**:
  - `TurnStatus` 类型 + 常量: completed/queued/canceled/failed
  - `TeamAgentCall`: SessionID/ParentSessionID/PromptEnvelope/Actor/ToolPolicy
  - `TurnRunResult`: Status + Result
  - `TurnRunner` 接口: Run/Cancel/IsSessionBusy
  - `AgentSpec`/`AgentFactory` 接口: BuildRunner
  - `ToolPolicyProfile`: AllowedTools + DisallowedTools + PermissionMode
- **依赖**: M1-03
- **验收**: 接口编译通过; TeamAgentCall 与 SessionAgentCall 独立类型; TurnRunResult 区分终态
- **工时**: 0.5d

### M1-05: 异步执行 + Agent Kill
- **文件**: `internal/agent/coordinator.go` (修改), `internal/agent/agent_tool.go` (修改)
- **具体改动**:
  - `SubAgentMode` (Sync/Async), `SubAgentHandle` (RunID+StatusChan+Cancel), `SubAgentStatus` (State+Progress)
  - 拆分 runSubAgent → runSubAgentSync + runSubAgentAsync (goroutine + context.WithCancel + channel)
  - coordinator 新增 `activeSubAgents map[string]context.CancelFunc`
  - Cancel/CancelAll 扩展遍历 activeSubAgents
  - goroutine 退出清理 map
- **依赖**: M1-02, M1-04
- **验收**: 同步行为不变; 异步立即返回不阻塞; handle.Cancel() 终止; CancelAll() 全部终止; 无 goroutine 泄漏
- **工时**: 2d

### M1-06: 结构化返回结果 AgentToolResult
- **文件**: `internal/agent/agent_result.go` (新建), `internal/agent/coordinator.go` (修改)
- **具体改动**:
  - `AgentToolResult`: AgentID/AgentType/Content/TotalTokens/TotalToolUseCount/TotalDurationMs/Usage
  - runSubAgent 返回 (AgentToolResult, error)
  - handler 序列化 JSON 放入 ToolResponse.Content
- **依赖**: M1-05
- **验收**: 返回 JSON 格式结果; tool_use_count 正确; duration_ms 正确; agent_type 正确
- **工时**: 1d

### M1-07: 进度回调 — pubsub AgentProgressEvent
- **文件**: `internal/pubsub/events.go` (修改), `internal/agent/progress.go` (新建)
- **具体改动**:
  - 新增 `PayloadTypeAgentProgress`
  - `AgentProgressEvent{RunID, AgentType, ToolUseCount, LastToolName, TokenCount, ActivityDesc, ElapsedMs}`
  - coordinator 新增 progressBroker
  - 异步 goroutine 中每次工具调用后 Publish
- **依赖**: M1-05
- **验收**: subscribe 收到进度事件; tool_use_count 递增; 完成/取消后停止; 不阻塞 goroutine
- **工时**: 1d

### M1-08: Agent 模板系统
- **文件**: `internal/agent/templates/general_purpose.md`, `explore.md`, `plan.md` (新建), `internal/agent/prompt/agent_prompts.go` (新建)
- **具体改动**:
  - 3 个模板文件: 角色定义 + 工具指引 + 输出规范
  - go:embed 加载, 导出 map[string]string
  - SetupAgents 中给子 Agent 默认填入 SystemPrompt
- **依赖**: M1-01
- **验收**: general-purpose 含角色定义; 用户覆盖生效; explore/plan 无写操作指引
- **工时**: 1d

---

## M2: 并行搜索队（6 任务 / 6 人天）

### M2-01: DelegateRunner + DelegateRunGroup 核心
- **文件**: `internal/team/delegate_runner.go`, `internal/team/delegate_types.go` (新建)
- **具体改动**:
  - `DelegateTask`, `DelegateResult`, `DelegateRunGroup` 结构体
  - `DelegateRunner{factory, mu, groups}`, `RunGroup(ctx, tasks)`: 每 task goroutine: BuildRunner → Run → 写 Results
  - 纯内存操作
- **依赖**: M1-04, M1-05
- **验收**: 3 delegate 并行; panic 不影响其他; wg.Wait() 正确; Results 包含所有结果
- **工时**: 2d

### M2-02: CancelGroup 统一取消
- **文件**: `internal/team/delegate_runner.go` (修改)
- **具体改动**:
  - `CancelGroup(groupID)`: 调 cancel() → ctx.Done()
  - goroutine 收到 cancel 后 runner.Cancel + 写 TurnCanceled
  - 幂等; 自动清理 groups
- **依赖**: M2-01
- **验收**: 2秒内退出; 结果 Status=TurnCanceled; 重复调用不 panic
- **工时**: 0.5d

### M2-03: Delegate ToolPolicy 只读过滤
- **文件**: `internal/agent/team_call.go` (修改), `internal/team/delegate_runner.go` (修改)
- **具体改动**:
  - `ReadOnlyDelegatePolicy()`: AllowedTools: view/grep/glob/ls/sourcegraph
  - 每个 delegate 注入 ReadOnlyDelegatePolicy
- **依赖**: M1-02, M2-01
- **验收**: delegate 仅 5 个只读工具; write/edit/bash/agent 不可用; coder 不受影响
- **工时**: 0.5d

### M2-04: Delegate UI — compact item + 展开/折叠 + child transcript modal
- **文件**: `internal/ui/chat/delegate.go` (新建), `internal/ui/model/chat.go` (修改)
- **具体改动**:
  - `DelegateGroupMessageItem`: children/groupStatus/expanded
  - `DelegateChildItem`: agentType/status/toolUseCount/activityDesc/result/childSessionID
  - RenderDelegateCompact: 状态行 + mini 指示器
  - RenderDelegateExpanded: toggle + child 详情
  - Child transcript modal: 只读, 复用 tree 渲染
- **依赖**: M2-01
- **验收**: compact 正确显示; 展开 child 详情; 完成状态颜色区分; child transcript 只读 Esc 关闭
- **工时**: 2d

### M2-05: 结果聚合
- **文件**: `internal/team/delegate_runner.go` (修改)
- **具体改动**:
  - `AggregateResults(group)`: markdown 摘要, 含类型/耗时/工具数/token 数/前 500 字符
  - 失败标注原因; 总长 ≤2000 字符
- **依赖**: M2-01, M1-06
- **验收**: 3 delegate 完成生成摘要; 失败不静默; 长度限制
- **工时**: 0.5d

### M2-06: M2 端到端演示脚本
- **文件**: `scripts/demo-m2.sh`, `scripts/demo-m2-task.md` (新建)
- **具体改动**: 启动→搜索→验证并行→验证 compact/expanded/canceled UI
- **依赖**: M2-01 到 M2-05
- **验收**: 手动可执行; 覆盖并行搜索+取消+聚合
- **工时**: 0.5d

---

## M3: 团队数据领域（9 任务 / 12 人天）

### M3-01: DB migration — 7 张核心表
- **文件**: `internal/db/migrations/XXXXXX_create_team_tables.sql` (新建)
- **具体改动**: teams/team_members/team_tasks/team_runs/team_events/team_event_counters/team_audit_events; 10 个索引
- **依赖**: 无
- **验收**: 7 表创建; 索引存在; unique 约束生效; rollback 可删除
- **工时**: 1d

### M3-02: sqlc queries — CRUD + CAS + atomic claim + event seq
- **文件**: `internal/db/sql/team_queries.sql` (新建)
- **具体改动**: TeamStore/MemberStore/TaskStore/RunStore/EventStore/AuditStore 全部 query
- **依赖**: M3-01
- **验收**: sqlc generate 无错误; UpdateTaskCAS version 冲突; ClaimNextTask 并发安全; event seq 单调
- **工时**: 2d

### M3-03: Domain models
- **文件**: `internal/team/models.go` (新建)
- **具体改动**: Team/Member/Task/Run/Event/Audit 6 个 model + status enum + request 类型
- **依赖**: M3-01
- **验收**: status Valid() 单测; JSON 往返正确
- **工时**: 1d

### M3-04: Store 实现层
- **文件**: `internal/team/store_*.go` (6 个文件新建)
- **具体改动**: 6 个 store 接口 (接受 Tx) + sqlc 实现; 自己不 Begin/Commit
- **依赖**: M3-02, M3-03
- **验收**: SQLite in-memory 测试覆盖; CAS version 冲突; ClaimNextTask 并发安全; AppendEvent seq 正确
- **工时**: 2d

### M3-05: TeamService facade
- **文件**: `internal/team/service.go` (新建)
- **具体改动**: Service 接口 + 实现; 事务编排 domain row + event + audit; 未到阶段返回 feature_disabled
- **依赖**: M3-04
- **验收**: CreateTeam 同一 tx 写 team+event+audit; DebugSnapshot 完整; version conflict 稳定错误
- **工时**: 2d

### M3-06: TeamWorkspace 接口 + local 实现
- **文件**: `internal/workspace/team.go`, `internal/proto/team.go` (新建)
- **具体改动**: TeamWorkspace 独立接口; AppWorkspace 代理到 team.Service; proto 类型
- **依赖**: M3-05
- **验收**: CreateTeam 通过 AppWorkspace 创建; 接口不依赖 server/client
- **工时**: 1d

### M3-07: API routes + server/client 双端
- **文件**: `internal/server/team.go`, `internal/client/team.go` (新建)
- **具体改动**: 9 条 REST routes; client HTTP 方法; events.go PayloadTypeTeamEvent; SSE 广播
- **依赖**: M3-06
- **验收**: POST 201+TeamSnapshot; GET snapshot 完整; client/server 双端一致; unknown event 不破坏旧 client
- **工时**: 2d

### M3-08: Feature flag 集成
- **文件**: `internal/config/config.go` + 所有 team 入口 (修改)
- **具体改动**: ExperimentalOptions{AgentTeamPreview, AgentTeam}; IsAgentTeamEnabled() helper; flag off 返回 feature_disabled
- **依赖**: M3-05, M3-07
- **验收**: flag off API 返回 feature_disabled; 不创建 DB row 不发 SSE; flag on 功能可用
- **工时**: 0.5d

### M3-09: Debug snapshot UI
- **文件**: `internal/ui/team/panel.go` (新建)
- **具体改动**: TeamPanel: teams 列表 + member 详情 + tasks + events; SSE 订阅实时更新; Ctrl+T; q/Esc 关闭
- **依赖**: M3-06, M3-07, M3-08
- **验收**: Ctrl+T 打开; 空闲状态; 数据正确; SSE 更新; flag off 无效果
- **工时**: 1.5d

---

## M4: 长期队员运行（13 任务 / 25 人天）

### M4-01: MemberRunner 状态机
- **文件**: `internal/team/member_runner.go`, `internal/team/models.go` (新建)
- **具体改动**: MemberState 枚举 (11 种); MemberRunner 结构体; Start/Loop/singleFlight/transition
- **依赖**: M1 的 TurnRunner/AgentFactory, M3 的 team_members/team_runs 表
- **验收**: 状态机全部合法转换测试; version CAS 冲突; singleFlight gate; busy 时排队
- **工时**: 3d

### M4-02: TeamRunner 生命周期
- **文件**: `internal/team/runner.go` (新建)
- **具体改动**: TeamRunner 接口 (StartTeam/StopTeam/SpawnMember/StopMember/CancelMemberTurn/Status); 实现 teamRunner
- **依赖**: M4-01, M3 的 team.Service
- **验收**: goroutine 不泄漏; StopMember graceful 顺序; StopTeam 所有 member; CancelMemberTurn 幂等; late result 不覆盖
- **工时**: 3d

### M4-03: Scheduler (claim/wakeup/heartbeat/stale recovery)
- **文件**: `internal/team/scheduler.go`, `internal/team/recovery.go` (新建)
- **具体改动**: Scheduler 结构体; ClaimNextTask atomic SQL; heartbeat goroutine; 恢复逻辑
- **依赖**: M4-01, M3 的 team_tasks/team_runs
- **验收**: ClaimNextTask 不 double-assign; heartbeat 正常; startup 恢复标记 interrupted; member 恢复 idle/blocked
- **工时**: 2.5d

### M4-04: Mailbox + Message Receipt
- **文件**: `internal/team/mailbox.go`, `internal/team/store_mailbox.go` (新建)
- **具体改动**: MailboxStore/ReceiptStore; SendMessage 流程 (direct/broadcast/role); delivery/read 追踪
- **依赖**: M3 的 team_mailbox_messages/team_message_receipts 表
- **验收**: direct 1 msg + 1 receipt + wake; broadcast N receipts 全部 wake; delivered/read 可追踪
- **工时**: 2.5d

### M4-05: PromptEnvelope Builder
- **文件**: `internal/team/prompt_builder.go` (新建)
- **具体改动**: PromptEnvelope 固定 section 顺序 (9 层); 低优先级截断; UNTRUSTED PEER INPUT 边界
- **依赖**: M4-04, M4-01
- **验收**: section order 单测; task 不截断; direct before broadcast; overflow 截断标记; task 不可截断时 ErrContextOverflow
- **工时**: 1.5d

### M4-06: Member Tools (team_report_status / team_send_message)
- **文件**: `internal/team/tools.go` (新建)
- **具体改动**: team_report_status (CAS update task → wake leader); team_send_message (direct/broadcast/role); ToolActorPolicy
- **依赖**: M4-04, M3 的 team_tasks
- **验收**: member 更新 task; send_message direct/broadcast/role; actor policy 正确
- **工时**: 1.5d

### M4-07: Shutdown 与 Recovery 顺序
- **文件**: `internal/team/shutdown.go` (新建)
- **依赖**: M4-01, M4-02
- **具体改动**: ShutdownSequence 9 步: stopWakeups→event→cancel→wait→flush→terminal→stopped→event→publish
- **工时**: 2d

### M4-08: Stale Run Recovery
- **文件**: `internal/team/recovery.go` (修改)
- **依赖**: M4-03
- **具体改动**: StartupRecovery; InterruptedReason; usage_status (final/partial/unknown)
- **工时**: 1.5d

### M4-09: Compact Team Item UI
- **文件**: `internal/ui/chat/team.go` (新建)
- **依赖**: M4-02
- **具体改动**: TeamCompactItem; MemberCompactState; 5 种状态渲染; ExpandedTeamView; 24 行约束
- **工时**: 2d

### M4-10: Team Session Links
- **文件**: `internal/team/store_session_link.go` (新建)
- **依赖**: M3 team_session_links 表
- **具体改动**: SessionLinkStore; TeamSessionLink; RecoverMemberSession
- **工时**: 1d

### M4-11: Task Dependencies (blocks/blockedBy)
- **文件**: `internal/team/store_dependency.go`, `internal/team/task_deps.go` (新建)
- **依赖**: M3 team_task_dependencies 表
- **具体改动**: DependencyStore; 循环依赖检测 DFS; OnTaskCompleted 级联 wake
- **工时**: 1.5d

### M4-12: Partial Cost Accounting
- **文件**: `internal/team/cost.go` (新建)
- **依赖**: M4-01, M3 team_runs 表
- **具体改动**: UsageRecord; RecordUsage; CalculateTeamCost; UI unknown 显示
- **工时**: 1d

### M4-13: M4 E2E 测试
- **文件**: `internal/team/m4_e2e_test.go` (新建)
- **依赖**: M4-01 到 M4-12 全部
- **具体改动**: 6 个 E2E 场景 (spawn/message/report/cancel/shutdown/recovery/prompt)
- **工时**: 2d

---

## M5: 权限审批（10 任务 / 16 人天）

### M5-01: PermissionBridge (team-aware wrapper)
- **文件**: `internal/team/permission_bridge.go` (新建)
- **具体改动**: 实现 permission.Service; team session → check grant→create request→audit→wait; 非 team → delegate inner
- **依赖**: M4-01, permission.Service
- **验收**: 非 team 不变; team 走 bridge; hook allow 写 audit; hook deny 阻止; allow once/task 作用正确
- **工时**: 2d

### M5-02: team_permission_requests 表 + Store
- **文件**: `internal/db/migrations/NNN_team_permission_requests.sql`; `internal/team/store_permission.go` (新建)
- **具体改动**: 创建 requests 表 (20+ 字段); PermissionStore 接口; 索引
- **依赖**: M3 DB schema
- **验收**: migration 正常; CRUD 正确; ListPendingByTeam 排序; FindByRun 批量查找
- **工时**: 1.5d

### M5-03: team_permission_grants 表 + Store
- **文件**: 同上 grant 版本
- **具体改动**: grants 表; GrantStore; FindActiveGrant (scope 窄到宽匹配)
- **依赖**: M5-02
- **验收**: grant 创建后 FindActiveGrant 正确; scope 优先级 call>task>session; revoked/expired 不返回
- **工时**: 1.5d

### M5-04: Scoped Grant 状态机
- **文件**: `internal/team/permission_state.go` (新建)
- **具体改动**: PermissionFSM; 合法转换 5 种; Resolve (late response 检测); Expire 批量; CancelByRun; Orphan startup
- **依赖**: M5-02, M5-03
- **验收**: pending→allowed 创建 grant; denied/expired/canceled/orphaned 全路径; late response 只 audit
- **工时**: 2d

### M5-05: Permission UI 扩展
- **文件**: `internal/ui/dialog/permissions.go` (修改)
- **具体改动**: TeamPermissionDialog 嵌入现有; 显示 member/task/run/tool/path; allow once/for task/deny; 队列
- **依赖**: M5-01, M5-04
- **验收**: UI 展示完整 team 归因; allow once 正确; allow for task scope 正确; allow for session 默认不可见
- **工时**: 2d

### M5-06: Permission Queue + Timeout
- **文件**: `internal/team/permission_queue.go` (新建)
- **具体改动**: PermissionQueue; maxPending=3; defaultTTL=5min; time.AfterFunc expire; 队列激活
- **依赖**: M5-02, M5-04
- **验收**: 超 max_pending 返回 error; expiry timer 触发; queue 激活; restart 恢复 pending
- **工时**: 1.5d

### M5-07: Late Response Handling
- **文件**: `internal/team/permission_bridge.go` (修改)
- **具体改动**: Resolve() 检查 run.IsTerminal(); 已结束只 audit 不 grant
- **依赖**: M5-04, M5-01
- **验收**: completed/canceled run late response 只 audit; UI 显示 "request ended"
- **工时**: 1d

### M5-08: Audit 覆盖
- **文件**: `internal/team/audit.go` (新建)
- **具体改动**: 15 种 audit action; AuditEntry 结构体; AuditQuery; audit 不可删除
- **依赖**: M3 team_audit_events 表
- **验收**: 全部 15 种 action 可检索; 完整归因; 查询性能 <100ms
- **工时**: 1.5d

### M5-09: Hook Allow Audit (旁路审计)
- **文件**: `internal/agent/hooked_tool.go` (修改)
- **具体改动**: hook allow/deny team mode 下写 team_audit_events; 非 team 不写
- **依赖**: M5-01, M5-08
- **验收**: hook allow 写 audit; hook deny 写 audit; PermissionBridge 不绕过 hook deny
- **工时**: 1d

### M5-10: M5 E2E 测试
- **文件**: `internal/team/m5_e2e_test.go` (新建)
- **依赖**: M5-01 到 M5-09 全部
- **具体改动**: 7 个 E2E 场景 (allow/deny/scope/late/timeout/hook/non-team)
- **工时**: 2d

---

## M6: 安全补丁（10 任务 / 14.5 人天）

### M6-01: Patch Artifact Schema
- **文件**: `internal/team/artifact_patch.go`; `internal/db/migrations/NNN_team_artifacts.sql` (新建)
- **具体改动**: PatchArtifact 结构体; TouchedFile; ApplyStatus 枚举; ArtifactStore 扩展
- **依赖**: M3 team_artifacts 表, M4-01
- **验收**: patch 含 base_hash/touched_files/change_kinds; binary 返回 ErrBinaryNotApplicable; content_ref opaque
- **工时**: 1.5d

### M6-02: ContentStore (opaque ref + hash verification)
- **文件**: `internal/team/content_store.go` (新建)
- **具体改动**: ContentStore 接口; contentRef=SHA256[:16]; contentHash=SHA256; Get verify hash
- **依赖**: M6-01
- **验收**: Put→Get 一致性; Verify mismatch error; workspace 隔离; ref 格式检查
- **工时**: 1d

### M6-03: Filesystem Blob Store
- **文件**: `internal/team/blob_store.go` (新建)
- **具体改动**: FilesystemBlobStore 实现 ContentStore; root=DataDirectory/blobs/; 不出现在 API/DB
- **依赖**: M6-02
- **验收**: store root 安全; content_ref 不透出 path; workspace 隔离
- **工时**: 1d

### M6-04: Apply Service (base hash + atomic write + rollback)
- **文件**: `internal/team/apply_patch.go` (新建)
- **具体改动**: ApplyService; 6 步 apply 流程; per-path mutex; temp file+atomic rename; rollback
- **依赖**: M6-01/02/03, M5-01
- **验收**: base_hash 全匹配→apply; 任一 mismatch→no-write→conflict; pre-write recheck→rollback; binary error
- **工时**: 2.5d

### M6-05: Apply Conflict 处理
- **文件**: `internal/team/store_apply_conflict.go`; `internal/db/migrations/NNN_team_apply_conflicts.sql` (新建)
- **具体改动**: team_apply_conflicts 表; ApplyConflictStore; conflict artifact
- **依赖**: M6-04
- **验收**: base mismatch→conflict row+artifact; rollback failed→conflict; UI 可查询
- **工时**: 1d

### M6-06: Patch Review UI
- **文件**: `internal/ui/dialog/patch_review.go`, `internal/ui/diffview/diff.go` (新建)
- **具体改动**: PatchReviewModal; UnifiedDiff; file/hunk 导航; 大 diff summary mode; binary review-only; keyboard
- **依赖**: M6-01, M6-02
- **验收**: 导航可用; unified diff 渲染; 大 diff summary; binary review-only; viewport 滚动
- **工时**: 2d

### M6-07: Patch Apply/Reject UI + Audit
- **文件**: `internal/ui/dialog/patch_review.go` (修改)
- **具体改动**: Apply/Reject 确认弹窗; 结果+audit 显示; conflict 详情
- **依赖**: M6-04, M6-06
- **验收**: apply 成功显示+audit; reject 状态+audit; conflict 文件+hash 差异
- **工时**: 1.5d

### M6-08: Verification Log
- **文件**: `internal/team/verification.go` (新建)
- **具体改动**: VerificationLog artifact; bash allow-list (5 种测试命令); leader-triggered; output redaction
- **依赖**: M6-04, M5-01
- **验收**: leader 触发 verification; allow-list 限制; output redaction; log 关联 patch
- **工时**: 1.5d

### M6-09: Binary Artifact Handling
- **文件**: `internal/team/apply_patch.go` (修改), `internal/ui/dialog/patch_review.go` (修改)
- **具体改动**: binary→ErrBinaryNotApplicable; UI "[binary - review only] (256KB)"
- **依赖**: M6-01, M6-04
- **验收**: binary 不 allow apply; UI 显示 review-only
- **工时**: 0.5d

### M6-10: M6 E2E 测试
- **文件**: `internal/team/m6_e2e_test.go` (新建)
- **依赖**: M6-01 到 M6-09 全部
- **具体改动**: 7 个 E2E 场景 (generate/review/apply/conflict/reject/binary/verification/integrity)
- **工时**: 2d

---

## M7: 高级隔离（6 任务 / 6.5 人天，远期概要）

### M7-01: Runtime Adapter Interface
- **文件**: `internal/team/runtime_adapter.go` (新建)
- **具体改动**: `RuntimeBackend` 接口 (5 方法); InProcessBackend 保持 M4 行为
- **依赖**: M4-01, M6
- **验收**: 接口清晰; InProcessBackend 保持 M4
- **工时**: 1d

### M7-02: Worktree Backend
- **文件**: `internal/team/worktree_backend.go` (新建)
- **具体改动**: `WorktreeBackend` 实现 RuntimeBackend; git worktree add/remove; 冲突检测
- **依赖**: M7-01
- **验收**: worktree 创建+隔离; Stop 清理; 冲突检测
- **工时**: 1.5d

### M7-03: Process Backend
- **文件**: `internal/team/process_backend.go` (新建)
- **具体改动**: `ProcessBackend`; JSON-RPC IPC; SIGTERM→SIGKILL; 资源控制
- **依赖**: M7-01
- **验收**: 进程启动/停止/转发; crash IPC 检测
- **工时**: 1.5d

### M7-04: Crash Recovery
- **文件**: `internal/team/crash_recovery.go` (新建)
- **具体改动**: CrashDetector; per 30s check; crash → interrupted + draft + cleanup
- **依赖**: M7-01/02/03
- **验收**: crash 检测; audit+event 不丢; worktree 可清理; draft 保存
- **工时**: 1d

### M7-05: A2A Gateway Skeleton
- **文件**: `internal/team/a2a_gateway.go`, `internal/team/a2a_types.go` (新建)
- **具体改动**: AgentCard; Crush<->A2A 映射; auth 延后
- **依赖**: M7-01
- **验收**: AgentCard 可解析; 映射定义清晰; 不与内部 TeamRuntime 冲突
- **工时**: 1d

### M7-06: Advanced Team Panel UI
- **文件**: `internal/ui/chat/team.go` (修改)
- **具体改动**: backend 列; 仅 Experimental.AgentTeamAdvanced flag
- **依赖**: M7-01
- **验收**: backend 类型正确; flag off 不显示
- **工时**: 0.5d

---

## 风险标注

### 高风险
1. **M4-01 MemberRunner (3d)**: SessionAgent.Run (nil,nil) queued case 转换依赖 M0.5 spike 结论
2. **M4-02 CancelMemberTurn (3d)**: cancel/late result/heartbeat 三者并发竞争 terminal state
3. **M6-04 Apply Service (2.5d)**: rollback 可能中间失败, best-effort only

### 中风险
4. **M4-03 Scheduler claim (2.5d)**: SQLite UPDATE LIMIT RETURNING 在并发下非严格 atomic
5. **M5-01 PermissionBridge (2d)**: permission.Service 不是为 team wrapper 设计的接口
6. **M4-05 PromptEnvelope overflow (1.5d)**: 超长 task 无法放入 context window

---

> **文档版本**: v1.0
> **产出**: task-architect-a (M1-M3: 23 任务 27 人天) + task-architect-b (M4-M7: 39 任务 62 人天)
> **总计**: 62 任务 · 89 人天 · ~6 个月 (2 人并行)
