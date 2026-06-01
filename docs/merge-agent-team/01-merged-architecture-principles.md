# 01 Merged Architecture Principles

## 1. Team 是独立领域

整合来源：

- Notion：新增 `internal/team`，不要把 team 塞进 `agent` tool。
- 当前 repo：避免 `coordinator.go` 继续膨胀。

取舍：

`internal/team` 负责 team domain，`internal/agent` 只负责构建和运行 agent turn。
两者通过接口连接，避免 `Coordinator.currentAgent` 语义被污染。

## 2. DB 是产品态真相源

整合来源：

- Notion：DB-backed roster/mailbox/task/audit/outbox。
- 当前 repo：SQLite 是事实源，SSE/pubsub 只做通知。

取舍：

M0.5 runtime spike 可以 in-memory，但任何用户可见 team mode 必须 DB-backed。
事件丢失不能导致状态丢失；TUI 必须能用 snapshot 修复 event gap。

## 3. Leader/Mate 不共享上下文

整合来源：

- Notion：普通 assistant 输出不等于给 leader 发消息。
- 当前 repo：message consumption algorithm 明确不注入完整 leader session。

取舍：

协作只能通过 typed mailbox、shared task board、artifact、audit/event 暴露。
不注入 leader 完整 session、其他 mate 完整 transcript、全 workspace history。

## 4. Runtime Spike 早于产品化 Runtime

整合来源：

- Notion Phase 0：先证伪长期 mate 生命周期。
- 当前 repo M1：先做 read-only delegates 验证产品价值。

取舍：

新增 M0.5 hidden runtime spike。它不面向用户，不承诺 DB、UI、权限，只验证
`start/send/cancel/stop/flush` 和 app shutdown。M1 仍保留 read-only delegates。

## 5. 安全基础前置

整合来源：

- 当前 repo：ActorContext、workspace-scoped read-only、敏感文件 deny-list、MCP 过滤。
- Notion：permission bridge 和 team identity。

取舍：

M1 前必须有 ActorContext skeleton 和只读隔离。M4 再做完整 permission bridge。
任何 teammate 获得写能力前必须有 audit 和用户可见授权。

## 6. Patch 先于 Direct Write

整合来源：

- 当前 repo：teammate 第一阶段产 patch artifact，不直接写主工作区。
- Notion：file coordination 与 file lock 是多 mate 写作核心风险。

取舍：

M5 前不允许 teammate 直接写主工作区。先产 patch artifact，由 leader review/apply。
direct write 必须等 file lease/hash check/worktree 策略成熟。

## 7. A2A 是 Gateway，不是内部协议

整合来源：

- 两边一致认为 A2A 不适合作为第一阶段内部协议。

取舍：

内部使用 `team.Service + mailbox + task + event outbox`，A2A 在 M6 作为 external
adapter。A2A 不替代 native TeamRuntime。

## 不变量

1. 旧 `/agent` tool 行为不变。
2. `SessionAgent.messageQueue` 不是 team mailbox。
3. Session messages 不是 team 通信事实源。
4. 所有用户可见 team 写操作都要有 team/mate/task/run 归因。
5. Permission 默认最小授权，非一次性授权必须受 tool/action/path 约束。
6. Team event 可以丢，DB 状态不能丢。
7. Local workspace 与 client workspace 必须同契约。

