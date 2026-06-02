# 01 架构原则与模块边界

## 1. Team 是独立领域

Team 不是 `agent` tool 的增强版，也不是 `Coordinator.agents` map 的产品化。它是一个
独立领域：

```text
internal/team owns:
  Team
  TeamMember
  TeamTask
  TeamRun
  TeamMailboxMessage
  TeamArtifact
  TeamAuditEvent
  TeamEvent
```

`internal/agent` 继续负责构建和执行单个 agent turn。team 通过窄接口调用 agent，
不能访问 `coordinator` private fields。

## 2. DB 是用户可见状态的事实源

任何用户可见的 team 状态都必须落 SQLite：

- team/member/task/run 状态。
- mailbox delivery/read 状态。
- task version 和 dependency。
- artifact 和 patch apply 状态。
- permission request/decision audit。
- event seq 和 snapshot 修复点。

SSE/pubsub 只是通知。TUI 如果丢事件，必须能通过 `DebugSnapshot` 或 `TeamSnapshot`
恢复，而不是相信事件流永不丢。

## 3. leader 与 teammate 不共享完整上下文

禁止把 leader session 完整 transcript 注入 teammate，也禁止把某个 teammate 的普通
assistant output 当作发给 leader 的消息。

允许的协作通道只有：

- typed mailbox。
- shared task board。
- artifact。
- audit/event。
- debug snapshot。

peer mailbox 内容来自模型，必须视为不可信输入。system/developer/tool policy 永远高于
mailbox 指令。

## 4. Runtime 生命周期和 turn 生命周期分离

`SessionAgent.Run` 是一轮模型调用；`MemberRunner` 是长期 runtime loop。二者不能混为一谈。

```text
MemberRunner lifecycle:
  created -> starting -> idle -> queued -> running -> idle
                                      -> waiting_permission -> blocked
                                      -> failed
                                      -> shutting_down -> stopped

SessionAgent turn:
  build prompt -> stream model/tool loop -> flush messages -> return result/error
```

特别注意：`SessionAgent.Run` 可能因为同 session busy 返回 queued case。TeamRunner 不得
把 queued case 当作 task completed。

`SessionAgent` 实例本身也不是可随意共享的 session router。tools、models、messageQueue 和
activeRequests 都是实例字段；delegate/member runner 必须由 AgentFactory 创建独立实例，并由
MemberRunner 自己做 single-flight gate。

## 5. 权限默认最小化

M1 只允许 read-only delegates。M4 之前 teammate 不等待用户授权继续写工具。

早期 permission scope 只实现：

```text
call
task
session
```

预留但不实现：

```text
agent
team
workspace
```

任何非 call scope 都必须带 tool/action/path/server constraints、过期时间和 audit。

## 6. Patch 先于 direct write

M5 前 teammate 不直接修改主工作区。M3.5 可以先产 `change_proposal` 作为实现提案，但不
apply、不写文件。M5 的写作业流程是：

```text
teammate generates patch artifact
  -> leader reviews diff
  -> apply checks base hash
  -> success writes files
  -> failure creates conflict artifact
```

direct write 必须等 file lease、base hash、worktree 或 process backend 成熟后再开放。

## 7. A2A 是外部 gateway

A2A 解决的是“外部 agent 服务如何被发现、调用和追踪”。Crush 第一版 team mode 需要解决
的是本地 TUI、SQLite、permission、file write、session flush 和 tool policy。二者不是一层问题。

内部协议使用：

```text
team.Service + mailbox + task + event + permission bridge
```

A2A 在 M6 作为 adapter：

```text
TeamTask <-> A2A Task
MailboxMessage <-> A2A Message
TeamArtifact <-> A2A Artifact
AgentProfile <-> Agent Card
```

## 8. 旧行为不可回归

必须保证：

- 旧 `/agent` tool 行为不变。
- 单 agent chat 不依赖 `internal/team`。
- `AgentIsBusy` 默认仍表达 coder 状态。
- 普通 session list 不出现 teammate session。
- flag off 时 UI、prompt、tools、API 都不变化。
