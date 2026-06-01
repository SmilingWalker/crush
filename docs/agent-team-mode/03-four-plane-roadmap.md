# 03 Four Plane Roadmap

## 整合来源

- 当前 repo 的四线推进法保留。
- Notion 的 Phase 0-9 被重排到 M0-M6。

## 四线定义

| 线 | 名称 | 职责 |
| --- | --- | --- |
| A | Runtime Control Plane | TeamRunner、MateRunner、AgentRegistry、cancel、heartbeat、recovery |
| B | Collaboration Data Plane | DB schema、TeamService、mailbox、task、artifact、event outbox |
| C | Safety & Isolation Plane | ActorContext、tool policy、permission bridge、audit、MCP/skills 过滤 |
| D | Product Integration Plane | TUI、debug snapshot、SSE replay、feature flag、cost observability |

## 合并后的矩阵

| 里程碑 | A Runtime | B Data | C Safety | D Product |
| --- | --- | --- | --- | --- |
| M0 | 边界冻结 | schema 草案 | ActorContext 草案 | flag 草案 |
| M0.5 | in-memory MateRunner spike | in-memory mailbox | 不开放写权限 | debug log |
| M1 | DelegateRunner | 可选 RunGroup | read-only + deny-list + MCP 过滤 | delegate preview |
| M2 | AgentRegistry + TeamService 接口 | teams/mates/tasks/runs/events | ActorContext 全链路 + budget | snapshot/debug API |
| M3 | TeamRunner + scheduler + mailbox | mailbox/deps/artifacts | Tool 四钩子 | compact team item |
| M4 | permission wait/retry | audit/scoped grant/task CAS | permission bridge | permission UI |
| M5 | patch runner | patch artifact/file lock | apply validation/bash safety | review/apply UI |
| M6 | process/worktree/A2A adapter | external mapping | network/direct-write policy | advanced team UI |

## 分期原则

- M0.5 验证 runtime，不对用户承诺。
- M1 验证产品价值和安全边界。
- M2 建 durable domain，但不急着让所有协作闭环都上线。
- M3 才让长期 mate 通过 mailbox/task 真实工作。
- M4 才让权限桥和审计闭环成为写能力前提。
- M5 只允许 patch artifact 写作，不直接写主工作区。
- M6 才进入 worktree/process/A2A。

## M3 内部拆分

虽然合并路线把长期 runtime、scheduler、mailbox 放在 M3，但实现上应继续拆成：

```text
M3a  TeamRunner + Scheduler + Heartbeat + Recovery
M3b  Mailbox + Dependencies + Prompt Injection
```

这样保留当前 repo 的风险控制，不把 scheduler、mailbox、recovery、dependencies 一次性压进一个 PR。

