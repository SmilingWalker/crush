# 08 M3a/M3b：Scheduler + Recovery + Mailbox + Dependencies

M3 是协作循环的核心阶段。为降低复杂度风险，拆分为 M3a（Scheduler + Recovery）和 M3b（Mailbox + Dependencies）。

## 1. M3a：Scheduler + Recovery

M3a 是调度和恢复的基础设施，不涉及 mailbox。

### 1.1 范围

包含：

- durable scheduler。
- task claim/lease。
- heartbeat 机制。
- startup recovery。
- cancel/pause/resume/retry/timeout。
- Tool 四钩子（isReadOnly/isDestructive/checkPermissions/validateInput）。
- member 并发度控制。

不包含：

- mailbox。
- message receipts。
- task dependencies。
- scoped grant。
- ToolExecutionEvent。

### 1.2 Scheduler 状态

Task：

```text
open -> claimed -> running -> waiting_permission -> completed
                 -> paused -> open
                 -> retry_wait -> open
                 -> failed/canceled/interrupted
```

Run：

```text
queued -> starting -> running -> waiting_tool/waiting_permission
       -> completed/failed/canceled/timed_out/interrupted
```

Member：

```text
starting -> idle -> running -> waiting_permission -> paused -> stopped
```

### 1.3 ClaimTaskTx

要求：

- atomic update。
- 只 claim `open` task。
- dependency 未完成不可 claim。
- lease expired 后可重新 claim。
- 同事务创建 run 或写 claim event。

### 1.4 Heartbeat

实现要求：

- `team_runs.heartbeat_at` 字段。
- 在 `SessionAgent.Run` 的 streaming 回调中更新（每 30-60s）。
- 不使用独立 ticker goroutine。
- startup recovery 检测 heartbeat 过期判断 interrupted。

### 1.5 Recovery

启动时：

1. 查询 non-terminal runs。
2. heartbeat 过期的 running run -> interrupted。
3. waiting_permission 若 pending request 丢失 -> interrupted 或重新请求。
4. 按 retry policy requeue。
5. 重新 register active members。

不承诺：

- 恢复 LLM stream。
- 恢复 goroutine。
- 恢复无 owner 的 shell job。

### 1.6 M3a 验收

- scheduler 并发 claim 同一 task 只成功一次。
- cancel run 不取消其他 run。
- 重启后 stale running -> interrupted。
- heartbeat 更新频率正确。
- Tool 四钩子覆盖 delegate 可用工具。
- member 并发度控制生效（`max_concurrent_runs`）。

## 2. M3b：Mailbox + Dependencies

M3b 在 scheduler 基础上增加通信和依赖。

### 2.1 范围

包含：

- team mailbox。
- message receipts。
- direct/broadcast message。
- ask leader。
- task dependencies。
- scoped grant（call/task/session 三级）。
- ToolExecutionEvent。

不包含：

- patch artifact。
- direct write。

### 2.2 Mailbox

表：

```text
team_messages
team_message_receipts
```

message 类型：

```text
question
answer
result
review
handoff
system
```

direct message：

- `to_member_id` 非空。

broadcast：

- `to_member_id` 为空。
- 为目标 member 生成 receipts。

### 2.3 Ask leader

流程：

```text
teammate blocked
  -> write question message to leader
  -> task status waiting_leader/blocked
leader answer
  -> write answer message
  -> task back to open
  -> scheduler creates continuation run
```

要求：

- 不在 LLM stream 内无限等待。
- 问题必须 durable。
- leader 回答后明确继续 task。

### 2.4 Message consumption prompt 构建

详见 `04-collaboration-data-plane.md` 第 8 节。

核心规则：

- 按优先级注入：task description > direct messages > result summary > broadcast > root session。
- context window 溢出时优先保留 direct message + task description。
- 大消息（> 64KB）走 artifact，prompt 只注入 summary。

### 2.5 Scoped grant（三级）

```text
call     -- 仅本次 tool call
task     -- 当前 task 的所有 run
session  -- 当前 session 内所有 tool call
```

不实现 `agent`/`team`/`workspace` 级别。

### 2.6 M3b 验收

- teammate 可问 leader。
- leader 回答后 task 继续。
- direct/broadcast message 投递正确。
- scoped grant（call/task/session）不扩散。
- 所有 tool call 有 audit event。
- hook allow 也有 audit。
- message consumption 算法在 context window 溢出时正确截断。
- 大消息走 artifact，prompt 只注入 summary。
- permission request 显示 actor。
