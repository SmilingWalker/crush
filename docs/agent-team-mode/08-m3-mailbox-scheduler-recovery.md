# 08 M3 Mailbox + Scheduler + Recovery

M3 才是真正的协作循环：teammate 可以提问、收消息、重试、恢复。

## 1. 范围

包含：

- durable scheduler。
- task claim/lease。
- team mailbox。
- message receipts。
- artifacts。
- task dependencies。
- cancel/pause/resume/retry/timeout。
- startup recovery。
- scoped permission。
- ToolExecutionEvent。

## 2. Scheduler 状态

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

## 3. ClaimTaskTx

要求：

- atomic update。
- 只 claim `open` task。
- dependency 未完成不可 claim。
- lease expired 后可重新 claim。
- 同事务创建 run 或写 claim event。

## 4. Mailbox

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

## 5. Ask leader

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

## 6. Recovery

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

## 7. 验收

- teammate 可问 leader。
- leader 回答后 task 继续。
- scheduler 并发 claim 同一 task 只成功一次。
- cancel run 不取消其他 run。
- 重启后 stale running -> interrupted。
- permission request 有 actor。
- tool audit 覆盖 read tools。

