# 02 M1 Team Preview / Parallel Delegates

M1 是第一个可交付阶段。它不实现完整 team mode，只验证"leader 能否并行派出多个只读 delegate，并把结果聚合回来"。

## 1. 范围

包含：

- 一个 leader session。
- 1-3 个 one-shot read-only delegates。
- 每个 delegate 独立 child session。
- delegate 并行执行。
- result 聚合回 leader。
- cancel group。
- feature flag 控制。
- `ActorContext` skeleton（日志+permission 归因）。
- workspace-scoped 敏感文件 deny-list。
- MCP instructions 按 actor 过滤。
- E2E 冒烟测试框架。

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
- 每个 delegate 带 `ActorContext`（至少包含 delegate name、parent session ID、group ID）。

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

## 6. 敏感文件 deny-list

即使 read-only 工具也不应暴露以下文件内容：

默认 deny 模式：

```text
.env
.env.*
credentials.json
credentials.*
*.key
*.pem
*.p12
*.pfx
id_rsa
id_ed25519
*.ssh/config
```

匹配规则：

- 基于 filepath.Ext 和 filepath.Match。
- 路径 normalize 后匹配（resolve symlink + clean）。
- 匹配时拒绝读取，返回 "access denied: sensitive file"。
- deny-list 存储在 config，用户可追加或移除条目。

实现位置：

- `internal/agent/tools` 中 view/grep 的路径校验环节。
- 在 workspace scope 检查之后、文件读取之前执行。

## 7. MCP instructions 过滤

M1 delegate 默认行为：

- 不注入 user 级 MCP server instructions。
- 不注入 global 级 MCP server instructions。
- 不暴露 MCP tools 给 delegate。
- 只有 project 级 MCP 且标记 `team_visible: true` 才注入。

实现位置：

- `buildTools` / `buildAgent` 中 MCP instructions 收集逻辑。
- 通过 `ToolBuildOptions.MCPPolicy` 控制。

## 8. UI 接入

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

## 9. E2E 冒烟测试

M1 必须建立最小 E2E 测试框架，验证核心链路：

测试用例：

- 启动两个 delegate，验证并行执行。
- 验证两个 delegate 的 child session 不相同。
- 验证 result 聚合正确。
- 验证 cancel group 后 active delegate 停止。
- 验证 delegate `grep` workspace 外路径被拒绝。
- 验证 delegate 不能读取 `.env` 文件。
- 验证 delegate 不包含 MCP instructions。

建议测试：

- delegate 失败时 group 仍能返回其他成功结果。
- parent session 删除时 child session 清理策略明确。
- sourcegraph 默认不暴露。
- flag off 时 delegate 功能完全不可见。

测试框架位置：

- 集成到现有测试体系。
- 可使用 Go 的 `testing` + `testify`。
- 需要模拟 LLM provider 返回（或使用真实 provider 的 mock server）。

## 10. 验收测试

必须通过：

- flag off 时 UI 不显示 delegates。
- 同时运行两个 delegate。
- 两个 delegate 的 child session 不相同。
- delegate result 分别可追踪。
- cancel group 后 active delegate 停止。
- delegate `grep` workspace 外路径被拒绝。
- delegate 不能读取敏感文件。
- delegate 不包含 MCP instructions。
- 日志包含 delegate actor 归因。
- E2E 冒烟测试通过。
