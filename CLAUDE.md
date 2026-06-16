# CLAUDE.md — crush agent-team mode development conventions

## Superpowers Workflow（铁律）

每个 task 必须走完整 superpowers 循环：
1. `superpowers:writing-plans` 写 plan → **plan 文件必须 commit 到分支**（`docs/superpowers/plans/YYYY-MM-DD-mX-XX-task.md`）
2. SendMessage team-lead 报告 plan 路径 + seam 决策
3. team-lead review gate → approve
4. TDD execute（test red → impl green → verify pass → commit）
5. verification before completion
6. two-stage review（spec-reviewer + quality reviewer，opus）
7. `merge --no-ff` to agent-team

**🔴 PLAN COMMIT RULE**: Plan 文档必须在写完 plan 后 **立刻 commit 到 worktree 分支**。不能留在 untracked 状态。merge 到 agent-team 时 plan 文档必须跟随。违反此规则导致 plan 丢失的情况已发生 3 次（M4-05, M4-09, M4-14）。

## Commit Convention
- single-line conventional commit（`feat(team): ...` / `fix(merge): ...` / `test(team): ...`）
- **NO trailer**（no Co-Authored-By, no Signed-off-by）
- 每 task TDD：一 task 一 commit

## Team Mode
- 所有 teammate 使用 **opus**（best model）
- 每个 task 一个 worktree，off agent-team tip
- team-lead 负责 plan-review gate + two-stage review + merge

## Constraints
- `-race` broken（cgo toolchain fault）— 测试不加 `-race`，用 `-count=3` jitter + 手动并发审查
- `go build` 是权威（gopls diagnostics in worktree 常误报 "use of internal package not allowed"/undefined）
- SQLite :memory: test fixture 必须 `SetMaxOpenConns(1)` + `runTx` 模式（防 :memory: per-conn 问题）

## Key Design Patterns
- sqlc package 名是 `db`，**不是** `sqlc`（master doc 写错了）
- goose migration 是 Go library（`//go:embed` + `goose.Up`），不是 CLI
- proto 类型独立于 team domain 类型（proto 不 import team）
- Service 层用 func() bool gate（依赖注入），不直接 import config
- M3b tables（mailbox 等）在 M4-04 才创建，M3-01 deferred

## Known Gaps
- M2 production AgentFactory — M4-14 修复
- ListNewFiles/is_new dormant bug（pre-existing，不阻塞）
- M3-04 InsertRun 硬编码 status='queued'（M3-05 StartRun 绕过）
- M2 timing flake（TestDelegateRunner_CancelGroup_Idempotent，-race broken 导致）
