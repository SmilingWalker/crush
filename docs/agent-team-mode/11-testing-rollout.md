# 11 测试、回滚与启动任务

## 1. 测试矩阵

### Runtime

- delegate 并行执行。
- cancel group。
- registry 注册多个 runtime。
- teammate busy 不影响 coder busy。
- shutdown cancel all runtime。

### Data/API

- migration up/down。
- `CreateTeamTx`。
- `ClaimTaskTx` 并发。
- `FinishRunTx` 幂等。
- snapshot + replay。
- unknown SSE payload 兼容。

### Safety

- workspace 外 path 被拒。
- symlink escape 被拒。
- teammate 默认无 MCP instructions。
- teammate 默认无 global skills。
- permission request 有 actor。
- scoped grant 不扩散。
- tool audit 覆盖 read tools。

### Product

- flag off 无 UI。
- delegate panel status。
- team panel status。
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

## 3. 最小启动任务

第一批 issue：

1. 新增 `experimental.agent_team_preview`。
2. 实现 workspace-scoped read-only helper。
3. 新增 `DelegateRunner`。
4. 新增 delegate run group cancel registry。
5. 新增 TUI delegate panel。
6. 新增 `ActorContext` skeleton，不改变旧行为。

完成后再进入：

7. team DB schema。
8. AgentRegistry。
9. Team API/SSE。
10. Scheduler。
11. Permission/Audit hardening。
12. Mailbox。
13. Patch artifact。

## 4. 不应提前做的事项

M1 前不要做：

- A2A。
- worktree。
- direct write。
- peer chat。
- complete task board。
- full audit DB。
- subagent hook 全覆盖。

原因：

- 这些都不能证明 team mode 本身有价值。
- 它们会把第一阶段复杂度放大数倍。
- 当前更关键的是 read-only delegate 的体验和安全边界。

