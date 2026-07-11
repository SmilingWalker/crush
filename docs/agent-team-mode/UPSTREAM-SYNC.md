# Upstream Sync Notes

> 这份文件是跨会话记忆。新会话开始时，先读这里恢复 fork 拓扑和同步状态。
> 最后更新：2026-07-11

## Fork 拓扑

```
origin = github.com:SmilingWalker/crush.git   (fork)
upstream = github.com:charmbracelet/crush.git (charm 原仓)

共同祖先 (fork 点):  d14f3b1b  feat(tools): add diff view for denied tools  (2026-05-26, v0.74.x)

local main:        c2e04177  (2026-06-14)  ← 停在 fork 后不久，未追上游
agent-team HEAD:   ab306e65  (2026-07-11)  ← 已合并 charm v0.84.0，团队功能主开发线
upstream/main:     26bfd466  (2026-07-10)  ← charm v0.84.0
```

## 关键分支

| 分支 | 位置 | 用途 |
|------|------|------|
| `agent-team` | local + origin | 团队功能主开发线（M0-M5 进行中）。**永远只在 SmilingWalker fork 内演进，不合回上游**。已合并 charm v0.84.0（2026-07-11）。 |
| `main` | local + origin | fork 的默认分支，停在 6-14。**不要动 local main 去追上游**。 |
| `upstream-main` | local + **origin** | charm `upstream/main` 的镜像。2026-07-11 推到 origin。用于给 `agent-team` 做 merge/rebase 的上游来源。 |

## 合并历史

### 2026-07-11: agent-team ⟕ upstream-main（charm v0.84.0）

- **Merge commit**: `ab306e65`
- **双父**: `78b9caf1`（agent-team 旧 HEAD）+ `26bfd466`（charm v0.84.0）
- **fantasy 版本**: 0.26.0 → 0.36.0
- **冲突**: 9 个文件，详见 `UPSTREAM-CONFLICT-REPORT.md`
- **关键决策**:
  - `coordinator.go` — 合并双方接口方法（agent-team 的 `AppendTools`/`BuildSessionAgent` + 上游的 `AcceptedRun`/`RunAccepted`/`BeginAccepted`/`GenerateTitle`），`runSubAgentStructured` 采上游 warn-on-cost-failure 语义
  - `backend/agent.go` — 采上游异步 `SendMessage` 重写，保留 agent-team 的 `SetCurrentSession`（nil-guard）
  - `ui.go` — 采上游干净的滚动选择逻辑
- **Ripple 修复**: `PermissionBridge.PublishMustDeliver`、5 个 test stub 补齐两套接口方法、`delegate.go` 的 `toolHeader` API 变更
- **遗留**: 2 个上游预存 Windows 路径分隔符测试失败（`internal/agent/tools/glob_test.go`、`internal/fsext`），在纯 upstream-main 上也复现，与本次合并无关
- **状态**: ✅ 已推送到 origin/agent-team

## 同步 upstream-main 的流程

以后想刷新上游进展：

```bash
git fetch upstream
git checkout upstream-main
git merge --ff-only upstream/main
git push origin upstream-main
```

## 给 agent-team 合并上游的流程

以后再做合并时（下次 charm 发新版本）：

```bash
# 1. 刷新 upstream-main 镜像
git fetch upstream
git checkout upstream-main
git merge --ff-only upstream/main
git push origin upstream-main

# 2. 合并到 agent-team
git checkout agent-team
git merge upstream-main
# 解冲突 → go build → go test → commit → push
```

## 上游重要新增能力（影响 agent-team 的）

1. **Coordinator run-path 重构** — `AcceptedRun`/`RunAccepted`/`BeginAccepted`，`SessionAgentCall` 加 `RunID`/`OnComplete`/推理参数。
2. **Bang Mode** (#3013) — TUI 直接跑 shell，整套 `shellResultMsg`/`runShellCommand`，是 `ui.go` 冲突来源。
3. **fantasy 0.26.0 → 0.36.0** — LLM 抽象层大升级。
4. **Provider 扩展** — fireworks、gpt-5.6、ollama/lmstudio/litellm/llamacpp enricher、openai-compat 自动发现。
5. **Server** — per-user runtime socket、stale socket 检测、prompts 与 client 连接解耦。
6. **Hooks** 加 `name` 字段；**schema.json** 扩展；**clipboard** 迁移到 `golang.design/x/clipboard`。
7. **herdr socket 集成** — workspace 层新增 `ReportCurrentSession`/`AgentRunShellCommand`。

## 工作约束（沿用 CLAUDE.md）

- `-race` 坏（cgo toolchain fault），测试用 `-count=3` jitter + 手动并发审查
- `go build` 是权威（gopls 在 worktree 常误报）
- SQLite :memory: fixture 必须 `SetMaxOpenConns(1)` + `runTx`
- plan 文档写完立刻 commit 到 worktree 分支（违反已丢 3 次）
