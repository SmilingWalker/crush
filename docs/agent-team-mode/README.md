# Agent Team Mode 文档集

> 这是 `crush` Agent Team Mode 的拆分版技术设计。  
> 背景调研见：`../agent-team-mode-architecture.md`。

## 阅读顺序

1. `00-current-constraints.md`  
   先读当前代码约束：`currentAgent` singleton、`SessionAgent` queue、permission、SSE、session schema。

2. `01-roadmap.md`  
   看整体技术路线和 M0-M5 迭代节奏。

3. `02-m1-team-preview.md`  
   第一阶段具体实现：read-only parallel delegates，不做完整 team runtime。

4. `03-runtime-control-plane.md`  
   A 线：`DelegateRunner`、`AgentRegistry`、`RunAgent`、per-agent ready gate、cancel registry。

5. `04-collaboration-data-plane.md`  
   B 线：team DB schema、sqlc、事务、API、SSE replay。

6. `05-safety-isolation-plane.md`  
   C 线：ActorContext、workspace-scoped read-only、permission scope、ToolExecutionEvent、MCP/skills 过滤、patch 安全写。

7. `06-product-integration-plane.md`  
   D 线：feature flag、TUI 面板、observability、E2E 验收。

8. `07-m2-durable-team-skeleton.md`  
   第二阶段具体实现：durable team/member/task/run/event。

9. `08-m3-mailbox-scheduler-recovery.md`  
   第三阶段具体实现：mailbox、scheduler、pause/resume/retry/recovery。

10. `09-m4-patch-artifact-write.md`  
    第四阶段具体实现：teammate 产 patch artifact，leader review/apply。

11. `10-m5-advanced-worktree-a2a.md`  
    后续高级阶段：direct write、file lease、worktree、A2A gateway。

12. `11-testing-rollout.md`  
    测试矩阵、回滚策略、最小启动任务清单。

## 四条工程线

| 工程线 | 文档 | 核心职责 |
| --- | --- | --- |
| A Runtime Control Plane | `03-runtime-control-plane.md` | 多 agent 创建、运行、取消、恢复 |
| B Collaboration Data Plane | `04-collaboration-data-plane.md` | task/message/run/artifact/event 持久化 |
| C Safety & Isolation Plane | `05-safety-isolation-plane.md` | 权限、审计、读写隔离、安全边界 |
| D Product Integration Plane | `06-product-integration-plane.md` | UI、feature flag、observability、验收 |

## 迭代节奏

| 里程碑 | 目标 | 关键产物 |
| --- | --- | --- |
| M0 | 设计冻结 | 文档集、feature flag 草案、issue 拆分 |
| M1 | Team Preview | read-only parallel delegates |
| M2 | Durable Team Skeleton | `AgentRegistry` + team DB/API/SSE |
| M3 | Collaboration Loop | mailbox + scheduler + recovery |
| M4 | Safe Patch Write | patch artifact + review/apply |
| M5 | Advanced Runtime | direct write + worktree + A2A gateway |

## 当前默认策略

- 旧 `/agent` API 永远代表默认 coder，不改成多 agent 路由。
- P0/M1 不做长期 teammate，只做 read-only delegates。
- P0/M1 不允许写文件、bash、job tools、MCP write。
- Team 状态以 SQLite 为事实源，SSE 只做通知。
- teammate session 不进入普通 session list。
- teammate 间 peer chat 不进入 M1。
- 写作业第一阶段是 patch artifact，不直接写主工作区。

