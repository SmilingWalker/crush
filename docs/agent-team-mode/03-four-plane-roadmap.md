# 03 四线推进路线

AgentTeam Mode 按四条工程线推进。每个里程碑都必须同时考虑 runtime、data、安全和产品，
否则很容易做出能跑但不能用、能用但不安全、能演示但不可恢复的半成品。

## 四条工程线

| 线 | 名称 | 职责 |
| --- | --- | --- |
| A | Runtime Control Plane | AgentRegistry、TeamRunner、MemberRunner、scheduler、cancel、heartbeat、recovery |
| B | Collaboration Data Plane | DB schema、TeamService、mailbox、task、artifact、event、snapshot |
| C | Safety & Isolation Plane | ActorContext、read-only policy、permission bridge、audit、MCP/bash/tool policy |
| D | Product Integration Plane | feature flag、TUI、debug snapshot、event replay、cost、E2E demo |

## M0-M6 功能矩阵

| 里程碑 | 用户可见能力 | A Runtime | B Data | C Safety | D Product/UI |
| --- | --- | --- | --- | --- | --- |
| M0 | 无，方案冻结 | 接口草案 | schema 草案 | ActorContext 草案 | flag 草案 |
| M0.5 | 无，hidden spike | in-memory MemberRunner | in-memory mailbox | 不开放写权限 | debug log |
| M1 | 并行只读 delegate 演示 | DelegateRunner | RunGroup，可内存 | read-only + sensitive deny + MCP 过滤 | delegate compact panel |
| M2 | 创建 team/member/task，可查看 snapshot | AgentRegistry | teams/members/tasks/runs/events | ActorContext 全链路 + budget | team debug/snapshot view |
| M3 | 长期 teammate 可收消息、跑一轮、汇报 | TeamRunner + scheduler + heartbeat | mailbox/deps/artifacts | read-only team tool policy | compact team item |
| M4 | 权限桥、审计、共享任务板 | permission wait/retry | audit + task CAS/deps | scoped grant + hook audit | permission/member blocked UI |
| M5 | teammate 产 patch，leader review/apply | patch runner | patch artifact + conflict | apply validation + bash safety | patch review/apply UI |
| M6 | worktree/process/A2A gateway | process/worktree adapter | external mapping | network/direct-write policy | advanced team panel |

## 里程碑推进原则

### M1 是演示，但不是 team runtime

M1 展示“leader 可以并行派出多个只读 delegate”。delegate 之间不互相通信，不共享任务板，
也不是长期 teammate。

### M2 是领域建模，不急着跑 teammate

M2 的价值是把 durable team state 建起来：team、member、task、run、event、snapshot。
M2 可以创建 member，但不要求长期 runner 产品化。

### M3 是第一个真正 team loop

M3 要让一个 teammate 长期存在，通过 mailbox/task 被唤醒，执行一轮后汇报状态。M3 可以
先限制为 read-only，权限桥和写能力放到 M4/M5。

### M4 是写能力前的安全闸

M4 不一定要开放写文件，但必须让 permission、audit、task CAS 和 actor identity 闭环。
没有 M4，M5 patch write 不允许上线。

### M5 只做 patch artifact，不做 direct write

M5 的写能力必须经过 leader review/apply。direct write、file lease、worktree 是 M6 以后。

## 分阶段演示

| 阶段 | 演示脚本 | 证明什么 |
| --- | --- | --- |
| M1 | 同时启动 2 个 read-only delegates，聚合结果 | 多 agent 产品价值和安全只读边界 |
| M2 | 创建 team、member、task，查看 snapshot/events | durable domain 和 API 可用 |
| M3 | leader 发消息给 teammate，teammate 运行并 report | 长期 teammate runtime 可用 |
| M4 | teammate 请求权限，UI 展示身份，用户拒绝，audit 记录 | 权限和审计闭环 |
| M5 | teammate 生成 patch，leader review/apply，冲突生成 artifact | 安全写作业闭环 |
| M6 | process/worktree teammate 或外部 A2A agent 接入 | 隔离和扩展能力 |
