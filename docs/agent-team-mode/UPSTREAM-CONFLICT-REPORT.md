# Upstream Conflict Report — `agent-team` ⟕ `upstream-main`

> 调研日期：2026-07-11
> 方法：`git merge upstream-main`（dry-run，已 abort，工作区干净）
> 结论：**9 个文件有真实冲突，其中 2 个是结构性改动（需手工设计），7 个是机械合并**

## 基线

| Ref | SHA | 说明 |
|-----|-----|------|
| Fork 点 | `d14f3b1b` | 共同祖先（2026-05-26, v0.74.x）|
| `agent-team` HEAD | `78b9caf1` | 298 commits ahead of fork |
| `upstream-main` | `26bfd466` | 215 commits ahead of fork（charm v0.84.0）|
| fantasy 版本 | `0.26.0` → `0.36.0` | **跨 10 个 minor，coordinator 重构依赖新版** |

---

## 冲突全景（9 文件，按严重度排序）

| # | 文件 | 上游 | 本分支 | 区域数 | 严重度 | 性质 |
|---|------|------|--------|--------|--------|------|
| 1 | `internal/agent/coordinator.go` | +268/-49 | +175/-22 | 3 | 🔴 重 | run-path 重构 vs BuildSessionAgent |
| 2 | `internal/ui/model/ui.go` | +436/-62 | +183/-22 | 2 | 🟡 中 | bang mode shell vs team bar/panel |
| 3 | `internal/backend/agent.go` | +134/-5 | +1 | 1 | 🟡 中 | upstream 重写 SendMessage |
| 4 | `internal/server/server.go` | +37/-2 | +20/-33 | 2 | 🟡 中 | socket dir vs netutil 重导出 |
| 5 | `internal/workspace/app_workspace.go` | +56/-5 | +52/-2 | 1 | 🟢 低 | interface additive |
| 6 | `internal/server/proto.go` | +65/-7 | +17 | 1 | 🟢 低 | 订阅顺序 vs team error 映射 |
| 7 | `internal/workspace/workspace.go` | +2 | +25 | 1 | 🟢 低 | import + interface 叠加 |
| 8 | `internal/agent/coordinator_test.go` | +130 | +79 | (test) | 🟢 低 | mock 补两套方法 |
| 9 | `internal/server/sessions_isbusy_test.go` | +11/-2 | +5 | (test) | 🟢 低 | stub 补两套方法 |

**另有 17 个文件双方都改过但 git 自动合并成功**（含 `app.go`、`config.go`、`cmd/root.go`、`pubsub/events.go`、`coder.md.tpl` 等）。

---

## 🔴 结构性冲突详解

### 1. `internal/agent/coordinator.go`（3 个冲突区）

**根因**：上游重构了整个 run path，本分支在同一接口和构造函数里加了 team 用的工厂方法。

#### 区 1 — `Coordinator` interface（L106-119）

双方各往 interface 加方法，纯叠加，机械合并：

```go
// 本分支加：
AppendTools(tools []fantasy.AgentTool)
BuildSessionAgent(ctx context.Context, spec AgentSpec) (SessionAgent, error)

// 上游加：
GenerateTitle(ctx context.Context, sessionID, prompt string)
RunAccepted(ctx context.Context, accept *AcceptedRun, ...) (*fantasy.AgentResult, error)
BeginAccepted(sessionID string) *AcceptedRun
```

**解决**：全保留，六句话都要。⚠️ 注意：上游的 `RunAccepted`/`BeginAccepted` 也要加到本分支所有 mock/stub 里（见下文"连带工作"）。

#### 区 2 — `NewCoordinator` 构造函数（L181-210）

```go
c := &coordinator{
    // 双方共有字段...
<<<<<<< HEAD
    questions:       questions,       // 本分支加（team ask_user_questions）
    activeSubAgents: make(...),       // 本分支加
=======
    runComplete:     runComplete,     // 上游加（AcceptedRun 完成 broker）
>>>>>>> upstream-main
}
```

**解决**：三行都留。⚠️ **前置工作**：上游给 `NewCoordinator` 加了 `runComplete *pubsub.Broker[notify.RunComplete]` 参数，本分支的调用点（`internal/app/app.go` 等已自动合并）要补这个参数和 broker 构造。

#### 区 3 — `runSubAgent` 结尾（L1509-1535）

本分支把"cost 更新失败"当硬错误返回；上游改成 warn-log 并引入 `subAgentOutput()` 提取输出。**逻辑分叉**：

```go
// 本分支：
if err := c.updateParentSessionCost(...); err != nil {
    return fantasy.ToolResponse{}, nil, err   // 硬失败
}
return fantasy.NewTextResponse(result.Response.Content.Text()), result, nil

// 上游：
if err := c.updateParentSessionCost(...); err != nil {
    slog.Warn("Failed to update parent session cost", ...)  // 软失败
}
output := subAgentOutput(result)
return fantasy.NewTextResponse(output), nil
```

**解决**：建议采纳上游语义（cost 失败不应吞掉已产出的 sub-agent 结果，本分支自己在 M5 里也踩过类似的 late-result 坑）。但本分支返回三元组 `(ToolResponse, *AgentResult, error)`（`runSubAgentStructured` 路径），上游是二元组——**要看 `runSubAgentStructured` 是否复用这段尾部**。需要手工对齐。

---

## 🟡 中等冲突

### 2. `internal/ui/model/ui.go`（2 区）

**根因**：上游加 bang mode（shell 命令执行 + 主题切换 + scrollbar），本分支加 team bar / team panel / delegate transcript。两边都改 `Update()` 的消息循环和结构体字段。

- 区 1（L971-980）：`SelectPrev`/`ScrollToSelected` 的缩进和逻辑分支微调，双方改了相邻行——**手工对齐**。
- 区 2（L985-997）：本分支重复了一段 `SelectLast/SelectNext` 逻辑，上游已清理——**直接采上游**。

**风险评估**：双方加的函数互不重叠（本分支的 `handleOpenDelegateTranscript` vs 上游的 `bangPromptFunc`/`runShellCommand`），主要是 `Update` switch 和 struct 字段叠加。中等机械活。

### 3. `internal/backend/agent.go`（1 区，但范围大）

上游重写了 `SendMessage`：加 `ValidateCall`、改用 `BeginAccepted`/`RunAccepted`、异步 dispatch。本分支只在开头加了一行 `ws.SetCurrentSession(msg.SessionID)`。

**解决**：保留本分支的 `SetCurrentSession` 调用（team 的 session 跟踪依赖它），其余采上游重写。

### 4. `internal/server/server.go`（2 区）

- 区 1（import）：本分支加 `netutil`，上游加 `os`/`path/filepath`——纯叠加。
- 区 2（socket 配置）：上游加 `socketDir()` + `maxUnixSocketPathLen`，本分支有 `DefaultHost`/`ParseHostURL` 重导出——不同函数，叠加即可。

---

## 🟢 低风险（机械合并，已列出解决方式）

### 5. `internal/workspace/app_workspace.go`

import 块：本分支加 `questions`/`team`，上游加 `proto`/`shell`/`slog`。interface 方法叠加。全留。

### 6. `internal/server/proto.go`（L1105-1117）

本分支在 error→HTTP status 映射里加 team 错误码（`ErrFeatureDisabled`→403 等），上游改了 `SubscribeEvents` 的订阅时序注释。**位置不同，叠加**。

### 7. `internal/workspace/workspace.go`（L20-24）

import 块冲突：本分支加 `questions`/`team`，上游加 `proto`。interface 方法叠加。全留。

### 8-9. 测试文件

`coordinator_test.go` 的 `mockSessionAgent` 和 `sessions_isbusy_test.go` 的 `stubCoordinator`：双方各给自己的 interface 方法写 mock 实现。**补齐两套方法即可**（本分支的 `AppendTools`/`BuildSessionAgent` + 上游的 `BeginAccepted`/`RunAccepted`/`GenerateTitle`）。

---

## 连带工作（冲突文件之外的 ripple）

合并这 9 个文件后，还要补的下游适配：

1. **`SessionAgent` 接口**（`internal/agent/agent.go`）：上游加了 `GenerateTitle`，本分支加了 `AppendTools`。所有实现该接口的 mock/stub（散落在多个 test 包）都要补两套方法，否则编译失败。
2. **`SessionAgentCall` 结构体**：上游加了 `RunID`/`OnComplete`/推理参数字段。本分支的 `BuildSessionAgent` 和 member runner 构造 `SessionAgentCall` 的地方要确认字段兼容。
3. **`NewCoordinator` 新参数 `runComplete`**：所有调用点（`app.go` 等）要补 broker 构造和传参。
4. **fantasy 0.26 → 0.36**：go.mod 升级后，跑一次全量 `go build ./...` 和 `go test ./...`，修 API 破坏。这是最大的未知数——coordinator 重构深度依赖新版 fantasy 的 step/callback API。
5. **`subAgentOutput` 辅助函数**：上游新增，本分支的 `runSubAgentStructured` 可复用。

---

## 上游新增能力清单（合并后会进来的）

影响 agent-team 架构的标 ⚠️：

1. ⚠️ **Coordinator `AcceptedRun` 机制** — `BeginAccepted`/`RunAccepted` 把 `Run` 拆成"预约+执行"，本分支的 member runner / delegate runner 都走 `SessionAgent.Run`，要评估是否迁移到新路径。
2. ⚠️ **fantasy v0.36.0** — provider 选项、step callback、reasoning effort API 变化。
3. **Bang Mode** (#3013) — TUI 直接跑 shell，独立功能，不冲突架构。
4. **Provider 扩展** — fireworks、gpt-5.6、ollama/lmstudio/litellm/llamacpp enricher、openai-compat 自动发现。
5. **Server 稳定性** — per-user runtime socket、stale socket 检测、event 订阅时序修正、prompts 与 client 解耦。
6. **Hooks** 加 `name` 字段；**schema.json** 扩展。
7. **herdr socket 集成** — workspace 层 `ReportCurrentSession`/`AgentRunShellCommand`。
8. **clipboard** 迁移到 `golang.design/x/clipboard`。
9. **UI** — scrollbar、toolcall 名展开、expanded content 保留换行、滚动 delta coalescing。

---

## 建议的合并执行顺序

如果决定合并，建议：

1. **先在干净 worktree 上做**（`git worktree add` off `agent-team`），不污染主工作区。
2. 升级 fantasy：先单独把 `go.mod` fantasy 升到 0.36，跑 `go build`，修 provider/step API 破坏——**这步最可能爆大量编译错误**，单独成一个 commit。
3. 解 `coordinator.go` + `agent.go`：补齐 interface 方法、`NewCoordinator` 参数、run-path 对齐。这是设计性工作。
4. 解 `ui.go` + `backend/agent.go` + `server.go`：手工对齐。
5. 解剩余 4 个低风险文件 + 2 个测试：机械活。
6. 全量 `go build ./...` + `go test ./internal/team/... ./internal/agent/... ./internal/ui/...`。
7. 重点回归：member runner / delegate runner 的 run 路径是否被上游 AcceptedRun 改动影响。

**预估工作量**：fantasy 升级 + coordinator 对齐是大头，约 1-2 天；其余机械活半天。总计 **2-3 天**，前提是 fantasy 0.36 没有 breaking change 雪崩。

---

## 当前状态

- ✅ `upstream-main` 已推到 origin（`26bfd466`）
- ✅ 调研完成，工作区干净（merge 已 abort）
- ⏸️ **未执行合并**——等用户决定时机
- `agent-team` 和 `local main` 均未改动
