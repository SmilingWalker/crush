# 10 Testing Risk Gates

## 测试矩阵

| 层 | 测试 |
| --- | --- |
| DB/sqlc | migration、CAS、atomic claim、partial index |
| Service | create team、spawn member、mailbox ack、task update、audit/outbox |
| Runtime | MateRunner loop、cancel、shutdown、flush、queued run |
| Permission | source metadata、scope、deny、audit、hook |
| Safety | workspace scope、symlink、sensitive deny-list、MCP filtering |
| Proto/SSE | round-trip、unknown type、workspace isolation、replay |
| ClientWorkspace | proto/internal translate |
| TUI reducer | idempotent reducer、event gap、snapshot recovery |
| Integration | leader spawn member、mailbox、task、permission、status |
| Regression | old `/agent` tool、single chat、permission dialog、SSE |

## 风险门禁

- M0.5 不通过，不进入 M2/M3 runtime 产品化。
- M1 安全基础不通过，不允许 delegate 扩权。
- M2 DB event replay 不通过，不允许 TUI 依赖 team event。
- M3 shutdown/flush 不通过，不允许默认开启 team mode。
- M4 audit 不完整，不允许 teammate 写工具。
- M5 patch conflict 不可靠，不允许 direct write。
- M6 native runtime 不稳定，不接 A2A gateway。

## 关键失败模式

| 风险 | 必须覆盖的测试 |
| --- | --- |
| `SessionAgent.Run` queued `nil,nil` 被误判完成 | MateRunner unit/integration |
| mate goroutine 泄漏 | app shutdown integration |
| leader busy 被 mate 污染 | coordinator/registry test |
| task claim race | DB atomic claim concurrency |
| mailbox 重复消费 | delivered/read/idempotency test |
| permission response 丢失 | bridge timeout/retry test |
| server/client 模式漏实现 | `AppWorkspace` + `ClientWorkspace` round-trip |
| event 乱序覆盖新状态 | TUI reducer seq test |
| `.env` 被 read-only delegate 读取 | safety E2E |
| patch base mismatch 覆盖文件 | artifact apply test |

## 最小 E2E

M1：

```text
flag on
start two read-only delegates
aggregate result
cancel all
verify .env denied
```

M3：

```text
leader creates team
leader spawns member
leader sends message
member receives mailbox
member runs one turn
member reports status
compact item updates
```

M4：

```text
member asks write permission
UI shows member identity
user denies
member reports blocked alternative
audit contains request and denial
```

M5：

```text
member creates patch artifact
leader reviews diff
base hash mismatch creates conflict artifact
no file overwritten
```

