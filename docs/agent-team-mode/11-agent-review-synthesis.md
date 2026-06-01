# 11 Agent Review Synthesis

本文件汇总本轮三个协作审查视角：

- 技术可行性 agent。
- 技术完整性 agent。
- 合并方案 agent。

## Agent A：技术可行性

关注问题：

- Notion Phase 0-9 与当前 M0-M5 的落地差异。
- 哪些 Notion 设计直接合入会放大工程风险。
- 哪些当前 repo 方案更现实。

结论：

- Notion 方案更完整，但直接合入会把 MVP 从 read-only delegate 拉成完整调度平台。
- 当前 repo 的安全前置、M1 read-only delegate、M3 拆分、成本预算、patch artifact
  更适合先落地。
- 必须补入 Notion Phase 0，作为 `M0.5 Hidden Runtime Spike`。
- A2A 应从当前 M5 拆出到 M6，避免和 worktree/direct write 混在一起。

必须合入：

- Phase 0 runtime spike。
- TeamRunner/MateRunner 状态机。
- mailbox envelope。
- TeamService 接口草案。
- PermissionBridge 流程。

可延后：

- 独立 audit/outbox 表。
- 自动 claim pending task。
- file locks。
- process backend。
- A2A gateway。

不建议早期合入：

- 完整 DB-backed mailbox + task dependencies + permission bridge 一次上线。
- session messages 作为通信协议。
- TeamRunner 塞入 `Coordinator.currentAgent`。
- `agent/team/workspace` permission scope。
- teammate direct write。
- A2A 内部协议。
- 复杂 dashboard。

## Agent B：技术完整性

关注问题：

- 旧版 `docs/deprecated/agent-team-mode` 相比 Notion 是否缺硬协议层。
- runtime/data/permission/API/TUI/test 是否形成闭环。

发现缺口：

1. `TeamRunner/MateRunner` 生命周期与状态机不够完整。
2. `internal/team.Service` 接口与事务边界缺少契约。
3. `team_* tools` 缺少工具级输入输出定义。
4. mailbox envelope / ack / correlation / prompt 注入不够硬。
5. permission bridge / audit / hook / ActorContext 需要合并成闭环。
6. API/SSE/workspace/client/TUI 需要端到端契约。
7. 测试矩阵应从阶段验收升级为 hard gates。

结论：

- 当前四线框架很好，不要推翻。
- 但正式 `docs/agent-team-mode` 必须补成契约文档，而不只是对比摘要。

## Agent C：合并方案

最终取舍：

- Notion 的 Phase 0 改成 M0.5 hidden runtime spike。
- 当前 repo 的 M1 read-only delegates 保留。
- Notion 的 Phase 1-4 合并为 M2/M3。
- Notion 的 permission bridge 与当前 repo 的安全线合并为 M4。
- 当前 repo 的 patch artifact write 保留为 M5。
- worktree/process/A2A 延后为 M6。

最终推荐：

```text
M0 -> M0.5 -> M1 -> M2 -> M3 -> M4 -> M5 -> M6
```

## 总结

`docs/deprecated/notion-agent-team` 做原始方案归档，`docs/agent-team-mode` 做最终实施口径。

最重要的调整是：

1. 把 Notion 的 runtime spike 补到当前 repo 的 M1 前面。
2. 继续保留当前 repo 的四线推进、安全前置和 patch-first 写入策略。
3. 把 Notion 的硬协议补为可执行契约：TeamRunner、TeamService、team tools、
   mailbox envelope、PermissionBridge、workspace/client/TUI round-trip。
