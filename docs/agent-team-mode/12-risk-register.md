# 12 风险登记表

本文档汇总 Agent Team Mode 设计中识别出的所有风险，按严重度和领域分类。每条风险附缓解措施和责任里程碑。

## 1. 风险矩阵

| ID | 严重度 | 领域 | 风险 | 缓解 | 里程碑 |
| --- | --- | --- | --- | --- | --- |
| R-01 | **严重** | 产品-工程匹配 | 多 agent 模式在终端 AI 助手场景中未验证价值，用户可能不愿为并行调研支付 3 倍 API 成本 | M1 仅做 read-only parallel delegates，快速验证产品假设；M1 后根据反馈决定方向 | M1 |
| R-02 | **严重** | 安全 | `ActorContext` 缺失导致 M1 阶段无法追踪 delegate 行为归属，debug 和安全审计无据可查 | M1 即引入 `ActorContext` skeleton（仅日志+permission 归因），旧调用为空兼容 | M1 |
| R-03 | **严重** | 安全 | read-only delegate 可通过 `grep/view` 读取 `.env`、`credentials.json`、`*.key`、`*.pem` 等敏感文件 | M1 增加 workspace-scoped 敏感文件默认 deny-list；支持用户配置覆盖 | M1 |
| R-04 | **严重** | 安全 | MCP server instructions 当前全局注入所有 agent，delegate 可获取用户未授权的 MCP 上下文 | M1 实现按 actor policy 过滤 MCP instructions；delegate 默认不注入 user/global MCP | M1 |
| R-05 | **高** | Runtime | `coordinator.go`（1279 行）+ `agent.go`（1454 行）改造回归风险：`currentAgent` → `defaultAgentID`、全局 `readyWg` → per-agent gate 涉及所有消费者 | M1 完全旁路 Coordinator，用独立 `DelegateRunner`；M2 用 feature flag 控制 registry 改造 | M1/M2 |
| R-06 | **高** | Runtime | 多 teammate 同时运行 LLM 调用、LSP 查询、MCP tool 调用时，现有共享资源（LSP Manager、filetracker、MCP 连接）可能存在并发安全问题 | M1 限制 delegate 为 read-only 且不用 LSP/MCP；M2 进行并发压力测试 | M1/M2 |
| R-07 | **高** | 成本 | 多 teammate 同时烧 token，缺少 team 级和 per-agent 的 token/cost 预算强制执行机制，用户可能收到巨额 API 账单 | M2 在 `team_runs` 和 `team_members` 增加 `max_cost`/`max_tokens` 字段，scheduler 层强制执行 | M2 |
| R-08 | **高** | 工程 | M3 复杂度跳升过陡：同时引入 mailbox（含 receipt/idempotency）、scheduler（claim/lease/timeout）、recovery（startup restore）、task dependencies、pause/resume/retry | 将 M3 拆为 M3a（Scheduler + Recovery）和 M3b（Mailbox + Dependencies），降低单阶段交付风险 | M3 |
| R-09 | **高** | 工程 | `coordinator.go` 进一步膨胀：team 工具注册逻辑可能使文件超过 1500 行 | team 相关逻辑提取到 `internal/team/` 包和 `internal/agent/team_adapter.go`，coordinator 只持有接口 | M2 |
| R-10 | **中** | 数据 | 消息消费策略细节缺失：未读消息排序规则、context window 溢出处理、broadcast vs direct 优先级未定义 | M3b 前完成 prompt 构建算法文档；消息按 timestamp 排序；overflow 时优先保留 direct message + task description + 最近 broadcast | M3 |
| R-11 | **中** | Runtime | Recovery 边界条件不完整：LLM streaming 中崩溃已消耗 token 但无响应；background shell job 崩溃可能已修改文件系统；permission request 等待中崩溃 pending request 丢失 | M3a 增加 heartbeat 机制（streaming 回调中每 30-60s 更新 `team_runs.heartbeat_at`）；startup 时检测 heartbeat 过期判断 interrupted | M3 |
| R-12 | **中** | 安全 | Grant scope 层级过多（7 级：call/run/task/session/agent/team/workspace）增加实现复杂度和用户认知负担 | 初期只实现 `call`/`task`/`session` 三级 scope，`agent`/`team`/`workspace` 留到 M4+ | M3 |
| R-13 | **中** | Runtime | `AgentRuntime.Status` 使用 `csync.Value[AgentStatus]` 表示 enum 级状态，但 agent 同时跑多个 task run 时 `running` 语义模糊 | Status 只表示 agent 启停状态（starting/idle/stopped），run 级别状态放 `team_runs` 表 | M2 |
| R-14 | **中** | 产品 | TUI 多 agent 状态面板设计偏简，Bubble Tea 中 team event → tea.Msg 的转化、split pane 键盘导航、更新频率控制未明确 | M1 prototype delegate panel，M2 明确 team event → tea.Msg 映射方案和更新 debounce | M1/M2 |
| R-15 | **中** | 工程 | `team_members.current_task_id` 和 `current_run_id` 可能成为写热点，member 频繁切换 task 时每次都更新 member 记录 | 这两个字段只用于 UI 展示，通过查询 `team_runs` 表 join 获取，不冗余存储 | M2 |
| R-16 | **低** | 数据 | `team_messages.body TEXT` 对大段代码或 diff 不够友好，消息 body 可能包含 artifact 级内容 | 大 body 走 `team_artifacts` 表，`team_messages` 只存 summary + artifact_id 引用 | M3 |
| R-17 | **低** | Runtime | Session 语义扩展兼容性：`team_session_links` 新映射不影响 `ListSessions`，但 `Delete`/`Get` 等方法可能有隐式假设 | M2 逐一审查 session service 所有方法，确认无隐式假设；增加 session type 区分 | M2 |
| R-18 | **低** | 数据 | SQLite WAL 模式下多 agent 同时写 `team_events` 可能产生 contention | 本地终端场景 agent 数量有限（3-10 个），单写不太可能成为瓶颈；监控 write latency 指标 | M2 |
| R-19 | **低** | 安全 | Symbolic link TOCTOU 攻击：check 和 use 之间可能被替换指向 workspace 外 | 路径校验使用 `filepath.EvalSymlinks` + 即时校验 + open 后 fstat 验证，缩小窗口 | M1 |
| R-20 | **低** | 安全 | 环境变量和 API key 通过 LLM provider 间接泄露：provider 返回的 content 可能包含之前对话中的敏感信息 | 这是 LLM 层面的通用问题，不在 Crush 控制范围；可通过 prompt 注入提醒 teammate 不输出敏感信息 | M2 |

## 2. 按里程碑分组

### M1 必须缓解

- R-01 产品价值验证（通过 M1 本身验证）
- R-02 ActorContext skeleton
- R-03 敏感文件 deny-list
- R-04 MCP instructions 过滤
- R-05 Coordinator 旁路（DelegateRunner）
- R-06 并发安全（限制为 read-only）
- R-19 Symlink TOCTOU 防护

### M2 必须缓解

- R-05 Coordinator 改造（feature flag）
- R-06 并发压力测试
- R-07 成本预算控制
- R-09 coordinator 膨胀控制
- R-13 AgentStatus 语义明确
- R-14 TUI event mapping
- R-15 current_task_id 写热点
- R-17 Session service 兼容
- R-18 SQLite contention 监控

### M3 必须缓解

- R-08 M3 拆分
- R-10 消息消费算法
- R-11 Heartbeat + Recovery
- R-12 简化 Grant scope
- R-16 大消息走 artifact

## 3. 风险趋势监控

以下指标应在各里程碑退出时检查：

| 指标 | 阈值 | 触发动作 |
| --- | --- | --- |
| M1 用户反馈"并行调研没用"的比例 | > 50% | 重新评估方向，不进入 M2 |
| M2 并发 3 teammate 时 LLM 调用失败率 | > 5% | 增加 retry/backoff，排查 provider rate limit |
| M2 team_runs 写延迟 P99 | > 100ms | 检查 SQLite contention |
| M3a recovery 后状态不一致数 | > 0 | 修复 recovery 逻辑，增加 heartbeat 频率 |
| M3b 消息丢失率 | > 0% | 检查 idempotency_key 和 receipt 逻辑 |

## 4. 与 Claude Code 的实现差距

以下差距需按优先级逐步补齐：

| 差距 | 优先级 | 对应里程碑 | 备注 |
| --- | --- | --- | --- |
| Bash 安全分析（Claude Code 有 80+ 危险模式检测） | 高 | M4 前 | teammate 获得 bash 权限前必须补齐 |
| Worker → Leader 权限转发协议 | 中 | M3b | 参考Claude Code 的 `swarmWorkerHandler` |
| Sandbox 进程级隔离 | 低 | M5+ | 参考Claude Code 的 `SandboxManager` |
| Tool 四钩子（checkPermissions/validateInput/isReadOnly/isDestructive） | 中 | M3a | 每个工具显式标记读写属性 |
| Fork/prompt cache 共享 | 低 | 远期 | Crush 用 Go，LLM 抽象层不同 |
