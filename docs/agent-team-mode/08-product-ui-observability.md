# 08 Product UI Observability

## 整合来源

- Notion：compact team item，不做复杂 dashboard。
- 当前 repo：Team panel、Recovery UI、Mailbox UI、成本监控、E2E 验收。

## 取舍

M3 先做 compact team item；M4/M5 再展开 mailbox、permission、patch review。

## M1 UI

- delegate status。
- cancel all delegates。
- result aggregation into leader。
- read-only preview 标识。

示例：

```text
Delegates
  researcher-a  running   00:21
  reviewer-b    done      00:34
```

## M3 UI

compact item：

```text
Team: parser-migration | 2 running / 1 blocked / 3 done | $0.23
```

expanded rows：

```text
role/name   state               tool   elapsed   cost   note
researcher  waiting_permission  write  01:12     $0.08  foo.go
tester      running             grep   00:38     $0.04  parser tests
```

actions：

```text
cancel team
cancel member
debug snapshot
copy trace id
```

## M4 UI

- permission dialog 显示 member name/role/session/tool/path。
- blocked member pill。
- retry/resume task。
- mailbox ask leader / answer / continue。

## M5 UI

- patch artifact list。
- touched files summary。
- diff review。
- apply/reject。
- conflict artifact。
- verification logs。

## Observability

日志字段：

```text
team_id
member_id
task_id
run_id
session_id
tool_call_id
```

指标：

- active teams。
- active members。
- active runs。
- run duration。
- queue depth。
- last activity。
- heartbeat age。
- token/cost。
- cancel/retry count。
- permission wait duration。
- tool execution count by actor。
- patch conflict count。

## Snapshot And Replay

TUI 不依赖事件绝对可靠：

```text
GET /snapshot -> snapshot_seq
GET /events?since_seq=snapshot_seq
subscribe SSE
if seq gap -> reload snapshot
```

Reducer 必须按 `team_id + member_id + seq` 幂等处理。

## Cost Display

M2 起：

- 每个 teammate 的 token/cost。
- team 总 token/cost。
- budget 使用百分比。
- 80/90/100% 阈值提示。
- 超预算时 teammate 自动停止，leader 收到通知。

