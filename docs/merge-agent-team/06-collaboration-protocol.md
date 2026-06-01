# 06 Collaboration Protocol

## 整合来源

- Notion：typed mailbox、team tools、task protocol、control message。
- 当前 repo：message consumption algorithm、task dependencies、M3a/M3b 拆分。

## 取舍

M3 引入 mailbox 和基础 task 协作；M4 强化 task CAS、permission response、依赖关系。

## MVP Team Tools

```text
team_create
team_spawn_member
team_send_message
team_task_create
team_task_update
team_task_claim
team_task_list
team_report_status
team_shutdown_member
```

每个 tool 必须定义：

- 调用方：leader、member，或二者都可。
- 输入 schema。
- 事务写入。
- 产生的 `TeamEvent`。
- 是否 wake runner。
- 返回的 id/version。
- 权限要求。
- 失败/重试语义。
- prompt 约束。

## Mailbox Envelope

```json
{
  "id": "msg_01",
  "team_id": "team_01",
  "from_member_id": "leader",
  "from_role": "leader",
  "to_member_id": "member_researcher",
  "to_role": "researcher",
  "kind": "message",
  "correlation_id": "corr_01",
  "summary": "parser findings",
  "payload": {
    "text": "请分析 parser 的 error recovery"
  },
  "created_at": 1779850000,
  "delivered_at": null,
  "read_at": null
}
```

## MVP Message Kinds

```text
message
task_assignment
task_status
permission_request
permission_response
shutdown_request
shutdown_approved
```

延后：

```text
shutdown_rejected
mode_set
plan_approval_request
plan_approval_response
```

延后原因：Crush 当前没有真实 plan/mode 状态机，过早加协议壳会变成假能力。

## Delivery Semantics

普通 message：

1. 解析 `to` 为 `member_id`。
2. 写 `team_mailbox_messages(kind=message)`。
3. 写 outbox `mailbox.message_created`。
4. wake member runner。
5. member `ListUnread(teamID, memberID, limit)`。
6. member `MarkDelivered(ids)`。
7. runner 包装 prompt。
8. turn 完成或明确消费后 `MarkRead(ids)`。

control message：

- runner 先处理。
- `shutdown_request` 进入 shutting_down，不裸投给模型。
- `permission_response` 唤醒 permission wait channel。
- `task_assignment` 更新 current task，再决定是否投给模型。

## Prompt 构建优先级

采用当前 repo 的算法：

1. task description，不可截断。
2. unread direct messages，按 timestamp 升序。
3. dependency completed result summary。
4. task-related broadcast。
5. latest leader instruction。
6. own session summary。

不注入：

- leader 完整 session。
- 其他 member 完整 transcript。
- 全 workspace history。

Context 溢出：

- 保留 task description。
- 保留 direct messages；如果 direct messages 自身超限，只保留最新 N 条。
- 截断 dependency result summary。
- 截断 broadcast messages。
- 移除 own session summary。
- 标记 `[truncated, N messages omitted]`。

## Task Protocol

所有 update 必须带：

```json
{
  "task_id": "task_01",
  "expected_version": 3
}
```

冲突返回：

```json
{
  "error": "version_conflict",
  "current_version": 4
}
```

`team_task_claim` 必须用 atomic SQL。不要 select 后 update。

## Tool Prompt Constraints

Leader prompt 必须明确：

- `agent` tool 是一次性 delegated task。
- `team_spawn_member` 是长期 teammate。
- `todos` 是私有 todo。
- `team_task_*` 是共享 task board。
- 不要假设 member 普通输出对 leader 可见。

Member prompt 必须明确：

- 普通输出只属于自己的 session。
- 进度、阻塞、完成通过 `team_report_status` 或 `team_send_message`。
- 更新 task 需要 `expected_version`。
- permission denied 后不要重复同一个请求，改为报告替代方案。

