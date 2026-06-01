# 09 Milestone Plan M0-M6

## M0: 方案冻结与边界对齐

目标：

- 冻结合并方案。
- 明确旧 `/agent` tool 不变。
- 明确 team 不复用 `messageQueue`。
- 明确 feature flag 默认关闭。

退出条件：

- 文档完成。
- 风险门禁完成。
- M0.5 spike 任务拆分完成。

## M0.5: Hidden Runtime Spike

目标：

- 验证长期 MateRunner 可行性。
- 不进用户产品。

退出条件：

- start/send/cancel/stop/flush 稳定。
- app shutdown 不泄漏 goroutine。
- leader 不被 mate busy 污染。

## M1: Safe Team Preview

目标：

- read-only parallel delegates。
- 验证产品价值。
- 安全基础前置。

退出条件：

- 1-3 个 delegate 并行运行。
- 只读工具可用。
- workspace 外路径拒绝。
- 敏感文件拒绝。
- MCP instructions 不泄露。
- result 回到 leader。
- flag off 无行为变化。

## M2: Durable Team Domain

目标：

- DB-backed team domain。
- TeamService。
- TeamEvent/snapshot/API。
- AgentRegistry。

退出条件：

- create team/spawn member/create task/list snapshot 可用。
- event replay 可用。
- cost budget 字段和检查可用。
- local/client workspace contract 一致。
- old `/agent` behavior 不变。

## M3: In-Process TeamRunner + Mailbox + Scheduler

目标：

- 单 member 长期运行。
- mailbox 投递。
- scheduler/heartbeat/recovery。
- compact TUI。

退出条件：

- leader spawn member。
- member 收到 message/task。
- member run one turn。
- member report status。
- heartbeat/recovery 可测。
- compact team item 显示状态。
- queued `Run` case 不误标完成。

## M4: Permission Bridge + Audit + Shared Task Board

目标：

- permission source team-aware。
- task CAS/dependency/claim。
- audit 覆盖 tool/permission/task。

退出条件：

- permission UI 显示 member identity。
- call/task/session scope 可用。
- deny 后 member blocked/report。
- task conflict 明确返回。
- audit 可查询。
- hook allow 写 audit。

## M5: Safe Patch Artifact Write

目标：

- teammate 不直接写主工作区。
- 先产 patch artifact。
- leader review/apply。

退出条件：

- patch artifact 可生成。
- base mismatch 不覆盖文件。
- conflict artifact 可见。
- apply/reject 有 audit。
- bash 安全分析覆盖 teammate 可用命令。

## M6: Advanced Runtime / Worktree / A2A Gateway

目标：

- worktree/process backend。
- direct write with lease。
- A2A gateway。

退出条件：

- worktree task 可独立测试。
- process crash 可恢复。
- A2A AgentCard/Task/Message/Artifact 可映射。
- A2A 不替代内部 TeamRuntime。

