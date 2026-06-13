# 开发任务拆分 — 索引

> 产出日期：2026-06-04
> 总任务数：62 | 总工时：89 人天

---

## 文档地图

| 文档 | 里程碑 | 任务数 | 工时 | 目标 |
|------|--------|--------|------|------|
| [02-m1-subagent-foundation.md](02-m1-subagent-foundation.md) | M1 地基 | 8 | 9d | ActorContext + AgentFactory + 异步 + 配置 |
| [03-m2-delegate-runner.md](03-m2-delegate-runner.md) | M2 并行 | 6 | 6d | DelegateRunner + DelegateRunGroup |
| [04-m3-team-data-domain.md](04-m3-team-data-domain.md) | M3 数据 | 9 | 12d | 7张DB表 + TeamService + API |
| [05-m4-long-lived-teammate.md](05-m4-long-lived-teammate.md) | M4 队员 | 13 | 25d | MemberRunner + Mailbox + Scheduler |
| [06-m5-permission-bridge.md](06-m5-permission-bridge.md) | M5 权限 | 10 | 16d | PermissionBridge + Scoped Grant |
| [07-m6-patch-artifact.md](07-m6-patch-artifact.md) | M6 补丁 | 10 | 14.5d | Patch + ContentStore + Apply |
| [08-m7-advanced-isolation.md](08-m7-advanced-isolation.md) | M7 隔离 | 6 | 6.5d | Worktree + Process + A2A |

---

## 进度总览

```
M1 ████████░░  8任务   9d   2-3周   地基
M2 ██████░░░░  6任务   6d   1.5-2周 并行
M3 ██████████  9任务  12d   2.5-3周 数据
M4 ██████████████ 13任务 25d   5-6周  队员
M5 ██████████ 10任务  16d   3-4周   权限
M6 █████████  10任务  14.5d 3-4周   补丁
M7 ████░░░░░░  6任务   6.5d 远期     隔离
────────────────────────────────────
   62任务  89d  ~6个月 (2人并行)
```

## 风险总览

| 风险 | 位置 | 等级 |
|------|------|------|
| SessionAgent 实例隔离 | M0.5 spike | 🔴 致命 |
| CancelMemberTurn 并发竞争 | M4-02 | 🔴 高 |
| Apply Service rollback 失败 | M6-04 | 🔴 高 |
| SQLite atomic claim 并发 | M4-03 | 🟡 中 |
| PermissionBridge 集成 | M5-01 | 🟡 中 |
| PromptEnvelope context overflow | M4-05 | 🟡 中 |
| Compact Team UI 24行约束 | M4-09 | 🟢 低 |
| M7 远期接口变化 | M7-01 | 🟢 低 |

## 相关文档

- `../00-final-ruling.md` — 最终裁决 + M1-M7 递进路线
- `../01-progressive-architecture.md` — 递进架构详解
- `../02-task-breakdown.md` — 总览版任务拆分
- [`../../`](../../) — 原 AgentTeam 技术方案（17文档，位于 `docs/agent-team-mode/`）
