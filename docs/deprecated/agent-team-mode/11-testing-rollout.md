# 11 测试、回滚与启动任务

## 1. 测试矩阵

### M1 E2E 冒烟测试

- 两个 delegate 并行执行。
- result 聚合正确。
- cancel group 生效。
- delegate 不能读 workspace 外路径。
- delegate 不能读敏感文件（`.env`）。
- delegate 不注入 MCP instructions。
- 日志包含 actor 归因。
- flag off 时 UI 不显示 delegate。

### Runtime

- delegate 并行执行。
- cancel group。
- registry 注册多个 runtime。
- per-agent ready failure。
- teammate busy 不影响 coder busy。
- shutdown cancel all runtime。
- heartbeat 更新频率正确。
- member 并发度控制。

### Data/API

- migration up/down。
- `CreateTeamTx`。
- `ClaimTaskTx` 并发。
- `FinishRunTx` 幂等。
- snapshot + replay。
- unknown SSE payload 兼容。
- cost budget 强制执行（超限 task 失败）。

### Safety

- workspace 外 path 被拒。
- symlink escape 被拒。
- symlink TOCTOU 场景。
- 敏感文件 deny-list 生效。
- 用户自定义 deny-list 覆盖。
- teammate 默认无 MCP instructions。
- teammate 默认无 global skills。
- permission request 有 actor。
- scoped grant（call/task/session）不扩散。
- tool audit 覆盖 read tools。
- hook allow 有 audit。

### Product

- flag off 无 UI。
- delegate panel status。
- team panel status。
- team panel 成本显示。
- mailbox ask/answer。
- patch review/apply/conflict。

## 2. 回滚策略

| 风险 | 回滚 |
| --- | --- |
| preview 不稳定 | 关闭 `agent_team_preview` |
| team API 不稳定 | 保持 flag off，不暴露 UI |
| event replay bug | UI 回退 snapshot-only |
| permission 噪音 | teammate 继续 read-only，不开放 write |
| patch apply bug | 禁用 apply，只保留 artifact |
| worktree bug | 禁用 worktree runner |
| 成本失控 | 降低 `max_cost`/`max_tokens` 或设为 0 禁止新 run |

## 3. 最小启动任务

第一批 issue：

1. 新增 `experimental.agent_team_preview`。
2. 新增 `ActorContext` skeleton，不改变旧行为。
3. 实现敏感文件 deny-list。
4. 实现workspace-scoped read-only helper。
5. 实现 MCP instructions 按 actor 过滤。
6. 新增 `DelegateRunner`。
7. 新增 delegate run group cancel registry。
8. 新增 TUI delegate panel。
9. 新增 E2E 冒烟测试框架。

完成后再进入：

10. team DB schema（含 cost budget 字段）。
11. AgentRegistry。
12. Team API/SSE。
13. Scheduler + heartbeat + recovery。
14. Tool 四钩子。
15. Mailbox + message consumption。
16. Permission/Audit hardening（三级 scope）。
17. Patch artifact。
18. Bash 安全分析（M4 前置条件）。

## 4. 不应提前做的事项

M1 前不要做：

- A2A。
- worktree。
- direct write。
- peer chat。
- complete task board。
- full audit DB。
- subagent hook 全覆盖。
- bash 安全分析（M4 前不需要）。

原因：

- 这些都不能证明 team mode 本身有价值。
- 它们会把第一阶段复杂度放大数倍。
- 当前更关键的是 read-only delegate 的体验和安全边界。

## 5. 性能回归监控

以下指标应在每个里程碑退出时测量：

| 指标 | 基线（无 team） | 告警阈值 |
| --- | --- | --- |
| `/v1/agent` 响应延迟 P50 | 测量 | +10% |
| `/v1/agent` 响应延迟 P99 | 测量 | +20% |
| 内存占用（无 team） | 测量 | +50MB |
| SQLite 写延迟 P99 | 测量 | 100ms |
| 3 delegate 并行时 CPU | N/A | 不超过单 delegate 的 4x |
