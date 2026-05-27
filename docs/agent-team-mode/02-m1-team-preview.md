# 02 M1 Team Preview / Parallel Delegates

M1 是第一个可交付阶段。它不实现完整 team mode，只验证“leader 能否并行派出多个只读 delegate，并把结果聚合回来”。

## 1. 范围

包含：

- 一个 leader session。
- 1-3 个 one-shot read-only delegates。
- 每个 delegate 独立 child session。
- delegate 并行执行。
- result 聚合回 leader。
- cancel group。
- feature flag 控制。

不包含：

- 长期 teammate。
- durable team DB。
- teammate peer chat。
- task dependencies。
- pause/resume/retry。
- write tools。
- bash/job tools。
- MCP write。

## 2. 用户体验

用户在当前 session 中触发 parallel delegates：

```text
Delegates
  researcher-a  running   00:21
  reviewer-b    done      00:34
```

完成后 leader 收到聚合结果：

```text
Delegate results:

researcher-a:
...

reviewer-b:
...
```

## 3. Runtime 实现

新增：

```text
internal/agent/delegate_runner.go
internal/agent/delegate_types.go
```

核心类型：

```go
type DelegateRunGroupRequest struct {
    ParentSessionID string
    Items           []DelegateRunRequest
    MaxParallel     int
}

type DelegateRunRequest struct {
    Name   string
    Title  string
    Prompt string
}

type DelegateRunGroupResult struct {
    GroupID string
    Runs    []DelegateRunResult
}

type DelegateRunResult struct {
    RunID          string
    Name           string
    ChildSessionID string
    Status         string
    Result         string
    Error          string
    Cost           float64
}
```

执行要求：

- 每个 delegate 创建 child session。
- child session 的 parent 是 leader session。
- delegate prompt 必须包含明确的只读约束。
- 每个 delegate 使用 read-only tool policy。
- group cancel 时取消所有 active child runs。

## 4. Child session id

M1 可以沿用现有 agent tool child session 模式，也可以新建 delegate session id。

推荐：

```text
delegate:{groupID}:{runID}
```

原因：

- 不复用 `messageID$$toolCallID`。
- 后续更容易迁移到 durable `team_session_links`。

实现前需确认：

- `sessions.id` 是否允许冒号。
- UI 是否对 session id 格式有隐式假设。

如果有兼容风险，使用 uuid，并在内存 run group 保存映射。

## 5. Tool policy

允许：

```text
view
grep
glob
ls
```

可选但默认关闭：

```text
sourcegraph
fetch
```

禁止：

```text
bash
edit
multiedit
write
download
job_output
job_kill
crush_logs
MCP tools
```

路径要求：

- 所有 path resolve 到 workspace root 内。
- symlink resolve 后仍必须在 workspace 内。
- Windows 下路径比较要 case-insensitive。

## 6. UI 接入

M1 不需要完整 Team panel。只在当前 session 页面加一个 compact delegates 区域。

状态：

```text
pending
running
done
failed
canceled
interrupted
```

操作：

- start delegates。
- cancel all。
- open child transcript。
- insert/append aggregated result。

## 7. 验收测试

必须通过：

- flag off 时 UI 不显示 delegates。
- 同时运行两个 delegate。
- 两个 delegate 的 child session 不相同。
- delegate result 分别可追踪。
- cancel group 后 active delegate 停止。
- delegate `grep` workspace 外路径被拒绝。

建议测试：

- delegate 失败时 group 仍能返回其他成功结果。
- parent session 删除时 child session 清理策略明确。
- sourcegraph 默认不暴露。

