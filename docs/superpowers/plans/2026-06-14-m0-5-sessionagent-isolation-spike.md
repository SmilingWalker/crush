# M0.5 SessionAgent 实例隔离 Spike 实现计划

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 用两个跨平台、无 LLM/VCR 的白盒测试实证验证 `SessionAgent` 实例隔离（容器独立性 + `buildTools` 无 aliasing），并把它锁定为 M1 的长期回归闸门。

**Architecture:** 仅新增一个测试文件 `internal/agent/agent_isolation_test.go`（`package agent`，白盒）。Test 1 直接调 `NewSessionAgent` 两次，类型断言到 `*sessionAgent`，突变一个实例后断言另一个的 4 个容器（tools/models/messageQueue/activeRequests）未受影响。Test 2 用 `testEnv` + `config.Init` 构造一个足以跑通 `buildTools` 的 `*coordinator`，连续调用两次，按 tool 名比较对象指针同一性。不改动任何生产代码。

**Tech Stack:** Go 1.24+（用 `t.Context()`）、`github.com/stretchr/testify/require`、`charm.land/fantasy`（`fantasy.AgentTool` 接口）、`github.com/charmbracelet/crush/internal/csync`、`internal/config`、`internal/agent/tools`。

**关联文档：** `docs/superpowers/specs/2026-06-14-m0-5-sessionagent-isolation-spike-design.md`（已批准设计）

---

## 关键事实（实现前必读，来自源码核对）

实现者无需再翻代码即可下笔，以下均已核对：

- `NewSessionAgent(opts SessionAgentOptions) SessionAgent`（`internal/agent/agent.go:141`）返回**接口**；类型断言 `.(*sessionAgent)` 即可白盒访问内部字段。
- `*sessionAgent` 字段（`agent.go:109-125`）：
  - `largeModel *csync.Value[Model]`、`smallModel *csync.Value[Model]`
  - `tools *csync.Slice[fantasy.AgentTool]`
  - `messageQueue *csync.Map[string, []SessionAgentCall]`
  - `activeRequests *csync.Map[string, context.CancelFunc]`
- 方法：`SetModels(large, small Model)`（`agent.go:1275`）、`SetTools(tools []fantasy.AgentTool)`（`:1280`）、`Model() Model`（`:1288`，返回 `largeModel.Get()`）。`smallModel` 无公开 getter，但白盒可直接 `a.smallModel.Get()`。
- `csync` API（`internal/csync/`）：
  - `Value[T]`：`Get() T`、`Set(t T)`。
  - `Slice[T]`：`Len() int`、`SetSlice(items []T)`、`Copy() []T`。
  - `Map[K,V]`：`Set(k, v)`、`Get(k) (V, bool)`、`Del(k)`、`Len() int`。
- `Model` 结构体（`agent.go:102-107`）含 `ModelCfg config.SelectedModel`；`config.SelectedModel{Provider: "标记"}` 可作区分标记，无需真实 `fantasy.LanguageModel`。
- `SessionAgentCall`（package `agent`，导出）含 `Prompt string` 字段。
- `config.Agent`（`internal/config/config.go:496`）字段：`Name string`、`Model SelectedModelType`（字符串类型别名，作 map key）、`AllowedTools []string`、`AllowedMCP map[...]...`。
- `tools.NewJobOutputTool()`（`internal/agent/tools/job_output.go:14`）**零参数**真实工具，Test 1 直接塞进 `SetTools`，无需实现 `fantasy.AgentTool` 接口。
- `tools.New*` 构造函数对 `nil` 服务引用**容忍**（仅存储、运行期才解引用）—— 已由 `common_test.go:171-184` 的 `coderAgent` 以 `nil` lspManager 调 `NewEditTool(nil,...)` 等实证。
- `(*coordinator).buildTools(ctx, agent config.Agent, isSubAgent bool) ([]fantasy.AgentTool, error)`（`coordinator.go:480`）。
- `buildTools` 访问的 coordinator 字段：`cfg`、`sessions`、`permissions`、`questions`、`history`、`filetracker`（**必须设置**）+ `lspManager`、`allSkills`、`activeSkills`、`skillTracker`（**可 nil**）。
- `testEnv(t)`（`common_test.go:66`）已提供：`sessions, messages, permissions, history, *filetracker, lspClients, questions, workingDir`。
- 工具名常量（`internal/agent/tools/*.go` 与 `internal/agent/agent_tool.go`）：`BashToolName="bash"`、`EditToolName="edit"`、`MultiEditToolName="multiedit"`、`GlobToolName="glob"`、`GrepToolName="grep"`、`LsToolName="ls"`、`ViewToolName="view"`、`WriteToolName="write"`、`AskUserQuestionsToolName="ask_user_questions"`、`DownloadToolName="download"`、`FetchToolName="fetch"`、`TodosToolName="todos"`、`CrushInfoToolName="crush_info"`、`CrushLogsToolName="crush_logs"`、`JobOutputToolName="job_output"`、`JobKillToolName="job_kill"`、`AgentToolName="agent"`（coordinator 包）、`AgenticFetchToolName="agentic_fetch"`。

---

## 文件结构

| 文件 | 操作 | 职责 |
|---|---|---|
| `internal/agent/agent_isolation_test.go` | 新建 | Test 1（容器隔离）+ Test 2（buildTools aliasing 探测）+ `sameTool`/`toolsByName` 辅助。`package agent` 白盒。 |

无生产代码改动。两个测试各自独立提交。

---

### Task 1: 创建测试文件 + Test 1（容器隔离不变量）

**Files:**
- Create: `internal/agent/agent_isolation_test.go`

- [ ] **Step 1: 写测试文件骨架与 Test 1**

创建 `internal/agent/agent_isolation_test.go`，内容如下：

```go
package agent

import (
	"context"
	"testing"

	"charm.land/fantasy"
	"github.com/charmbracelet/crush/internal/agent/tools"
	"github.com/charmbracelet/crush/internal/config"
	"github.com/stretchr/testify/require"
)

// TestSessionAgentInstancesAreIsolated locks the invariant that each
// NewSessionAgent call yields an independent instance: mutating one instance's
// tools / models / messageQueue / activeRequests must NOT leak into another.
//
// This is the M1 fatal-risk gate (see
// docs/superpowers/specs/2026-06-14-m0-5-sessionagent-isolation-spike-design.md).
// It is a regression test for an invariant that holds by construction today
// (NewSessionAgent allocates fresh csync containers per instance); a failure
// here means the isolation assumption is broken and M1 must not proceed.
func TestSessionAgentInstancesAreIsolated(t *testing.T) {
	a1 := NewSessionAgent(SessionAgentOptions{}).(*sessionAgent)
	a2 := NewSessionAgent(SessionAgentOptions{}).(*sessionAgent)

	// Two distinct pointers — truly separate instances.
	require.NotSame(t, a1, a2, "NewSessionAgent must return distinct instances")

	// Sentinel models distinguishable via ModelCfg.Provider; no real LLM needed.
	largeA := Model{ModelCfg: config.SelectedModel{Provider: "iso-large-A"}}
	smallA := Model{ModelCfg: config.SelectedModel{Provider: "iso-small-A"}}

	// Mutate ONLY a1 across all four container kinds.
	a1.SetModels(largeA, smallA)
	a1.SetTools([]fantasy.AgentTool{tools.NewJobOutputTool()})
	a1.messageQueue.Set("sess-x", []SessionAgentCall{{Prompt: "probe"}})
	a1.activeRequests.Set("sess-x", context.CancelFunc(func() {}))

	// Confirm the mutation landed on a1 (sanity, so a silent no-op is caught).
	require.Equal(t, 1, a1.tools.Len(), "a1 tools should hold the one tool we set")
	require.Equal(t, "iso-large-A", a1.Model().ModelCfg.Provider)

	// a2 must be completely unaffected across all four containers.
	require.Equal(t, 0, a2.tools.Len(), "a1.SetTools leaked into a2's tools")
	require.Equal(t, "", a2.Model().ModelCfg.Provider, "a1.SetModels leaked into a2's large model")
	require.Equal(t, "", a2.smallModel.Get().ModelCfg.Provider, "a1.SetModels leaked into a2's small model")

	_, mqOK := a2.messageQueue.Get("sess-x")
	require.False(t, mqOK, "a1.messageQueue.Set leaked into a2's message queue")

	_, arOK := a2.activeRequests.Get("sess-x")
	require.False(t, arOK, "a1.activeRequests.Set leaked into a2's active requests")
}
```

- [ ] **Step 2: 运行 Test 1，确认通过**

Run:
```bash
go test ./internal/agent/ -run 'TestSessionAgentInstancesAreIsolated' -v
```
Expected: `PASS`（`--- PASS: TestSessionAgentInstancesAreIsolated`）。这是对"构造即隔离"不变量的回归测试，**预期即绿**；若 FAIL，说明 `NewSessionAgent` 本身破坏了隔离——不得进入 M1，先排查。

- [ ] **Step 3: gofmt 规范化**

Run:
```bash
gofmt -w internal/agent/agent_isolation_test.go
```
Expected: 无输出（已规范化）。若文件有改动，重新确认 `go test` 仍 PASS。

- [ ] **Step 4: 提交**

```bash
git add internal/agent/agent_isolation_test.go
git commit -m "test(agent): lock SessionAgent instance isolation invariant"
```

---

### Task 2: Test 2（buildTools aliasing 探测，真实路径）

**Files:**
- Modify: `internal/agent/agent_isolation_test.go`（追加 Test 2 + 辅助函数 + `reflect` 导入）

- [ ] **Step 1: 追加 `reflect` 导入**

把文件顶部 import 块改为（增加 `"reflect"`）：

```go
import (
	"context"
	"reflect"
	"testing"

	"charm.land/fantasy"
	"github.com/charmbracelet/crush/internal/agent/tools"
	"github.com/charmbracelet/crush/internal/config"
	"github.com/stretchr/testify/require"
)
```

- [ ] **Step 2: 追加 Test 2 与两个辅助函数**

在文件末尾追加：

```go
// sameTool reports whether two fantasy.AgentTool interface values reference the
// same underlying object. It compares pointer addresses for pointer-kind
// dynamic types; for any non-pointer kind it returns false (a freshly-allocated
// value type is not aliased mutable state in the sense M1 must guard against).
// It never panics, regardless of comparability.
func sameTool(a, b fantasy.AgentTool) bool {
	va, vb := reflect.ValueOf(a), reflect.ValueOf(b)
	if va.Kind() != reflect.Pointer || vb.Kind() != reflect.Pointer {
		return false
	}
	return va.Pointer() == vb.Pointer()
}

// toolsByName indexes a tool slice by tool name for cross-call comparison.
func toolsByName(ts []fantasy.AgentTool) map[string]fantasy.AgentTool {
	m := make(map[string]fantasy.AgentTool, len(ts))
	for _, t := range ts {
		m[t.Info().Name] = t
	}
	return m
}

// TestBuildToolsReturnsDistinctObjects probes whether two consecutive
// buildTools calls on the same coordinator hand back the SAME tool object
// pointers (aliasing) or distinct ones.
//
// Why this matters for M1: even though Test 1 proves each SessionAgent's tool
// SLICE is independent (SetTools copies the slice), the slice ELEMENTS are
// interface values holding pointers — copying the slice copies the pointer, so
// two agents could still point at the same underlying tool object. If that
// object carries per-agent mutable state, agents would interfere.
//
// Approach: construct a coordinator rich enough to run buildTools (via the
// existing testEnv fixtures + config.Init), call buildTools twice with a
// sub-agent config, and assert every same-named tool is a distinct object.
// "agent" and "agentic_fetch" are intentionally excluded from AllowedTools so
// buildTools does not invoke c.agentTool / c.agenticFetchTool (which need more
// setup); this matches real sub-agents. isSubAgent=true makes
// wrapToolsWithHooks short-circuit, so a nil hookRunner is never dereferenced.
//
// Prediction (from reading buildTools: each tools.New*() mints a fresh object,
// no memoization for non-"agent" tools): all distinct -> green. A failure here
// is the spike's payoff: it surfaces aliasing M1 must handle by rebuilding
// tools per-runner (the net M1 contract regardless of outcome).
func TestBuildToolsReturnsDistinctObjects(t *testing.T) {
	env := testEnv(t)

	cfg, err := config.Init(env.workingDir, "", false)
	require.NoError(t, err)

	coord := &coordinator{
		cfg:         cfg,
		sessions:    env.sessions,
		messages:    env.messages,
		permissions: env.permissions,
		questions:   env.questions,
		history:     env.history,
		filetracker: *env.filetracker,
		// lspManager, allSkills, activeSkills, skillTracker left nil —
		// tool constructors tolerate nil (proven by coderAgent in common_test.go).
	}

	// A representative sub-agent toolset. Deliberately excludes "agent" and
	// "agentic_fetch" (see comment above).
	subAgentCfg := config.Agent{
		Name: "iso-probe",
		AllowedTools: []string{
			tools.BashToolName,
			tools.EditToolName,
			tools.MultiEditToolName,
			tools.GlobToolName,
			tools.GrepToolName,
			tools.ViewToolName,
			tools.WriteToolName,
			tools.AskUserQuestionsToolName,
			tools.DownloadToolName,
			tools.FetchToolName,
			tools.TodosToolName,
			tools.CrushInfoToolName,
			tools.CrushLogsToolName,
			tools.JobOutputToolName,
			tools.JobKillToolName,
		},
	}

	ctx := t.Context()
	tools1, err := coord.buildTools(ctx, subAgentCfg, true)
	require.NoError(t, err)
	tools2, err := coord.buildTools(ctx, subAgentCfg, true)
	require.NoError(t, err)

	require.NotEmpty(t, tools1, "buildTools returned no tools — AllowedTools filtering left nothing to compare")

	first := toolsByName(tools1)
	second := toolsByName(tools2)

	// Alias table (logged for the gate artifact; visible with -v).
	t.Log("buildTools aliasing table (name -> same object across two calls?):")
	var shared []string
	for name, t1 := range first {
		t2, ok := second[name]
		require.True(t, ok, "tool %q present in first buildTools call but absent in second", name)
		aliased := sameTool(t1, t2)
		t.Logf("  %s: %v", name, aliased)
		if aliased {
			shared = append(shared, name)
		}
	}

	require.Empty(t, shared,
		"aliasing detected — these tools are the SAME object across two buildTools "+
			"calls: %v. See spec section 五; M1 AgentFactory must rebuild tools "+
			"per-runner regardless (the net contract), and any tool here that carries "+
			"per-agent mutable state must be documented before M1-04/M1-05.", shared)
}
```

- [ ] **Step 3: 运行 Test 2，确认通过（spike 的核心发现点）**

Run:
```bash
go test ./internal/agent/ -run 'TestBuildToolsReturnsDistinctObjects' -v
```
Expected: `PASS`（所有 tool `false`，即两两不同对象）。`-v` 输出含 aliasing 表（每个 tool 名 → `false`）。

**若 FAIL**：`shared` 非空——这就是 spike 的价值。按 spec 第五节判定：shared 但均无状态 → 🟡，仍每 runner 重建；shared 且含可变状态 → 🔴，记录清单后才能进 M1。无论哪种，"每 runner 重建 tools" 都是 M1 硬约束（见 Task 3 Step 2 的记录要求）。

- [ ] **Step 4: gofmt 规范化**

Run:
```bash
gofmt -w internal/agent/agent_isolation_test.go
```
Expected: 无输出。若有改动，重跑 Step 3 确认仍 PASS。

- [ ] **Step 5: 提交**

```bash
git add internal/agent/agent_isolation_test.go
git commit -m "test(agent): add buildTools aliasing probe for sub-agent tools"
```

---

### Task 3: 全量验证 + 记录闸门结果

**Files:**
- （可选）Modify: `docs/superpowers/specs/2026-06-14-m0-5-sessionagent-isolation-spike-design.md`（在第六节末尾追加"Spike 结果"一行）

- [ ] **Step 1: 同时跑两个 spike 测试**

Run:
```bash
go test ./internal/agent/ -run 'TestSessionAgentInstancesAreIsolated|TestBuildToolsReturnsDistinctObjects' -v
```
Expected: 两个测试均 `PASS`。aliasing 表打印在输出里（满足 spec 第六节"aliasing 表已记录"的闸门条件——表即测试 `-v` 输出）。

- [ ] **Step 2: 截取 aliasing 表，记入 spec（可选但推荐）**

把 Step 1 输出中 `buildTools aliasing table:` 那一段贴入 `docs/superpowers/specs/2026-06-14-m0-5-sessionagent-isolation-spike-design.md` 第六节末尾，形如：

```markdown

### Spike 结果（2026-06-14 执行）

- Test 1（容器隔离）：PASS。
- Test 2（buildTools aliasing）：PASS —— 所有 tool 两两不同对象（全 `false`）。判定 🟢 绿灯。
- 结论：M1 的 `AgentFactory.BuildRunner` 每 runner 重建 tools + models 为"安全默认"（spec 第五节）→ 可进入 M1-01。
```

若 Test 2 出现 shared：改为记录 shared 清单 + 判定（🟡/🔴）+ "每 runner 重建 tools" 提升为 M1-04/M1-05 硬约束。

- [ ] **Step 3: 包级 go vet**

Run:
```bash
go vet ./internal/agent/
```
Expected: 无输出（无告警）。

- [ ] **Step 4: 提交 spec 结果记录（若做了 Step 2）**

```bash
git add docs/superpowers/specs/2026-06-14-m0-5-sessionagent-isolation-spike-design.md
git commit -m "docs(specs): record M0.5 SessionAgent isolation spike result"
```

- [ ] **Step 5: 闸门判定**

- 两个测试均绿 + aliasing 表已记录 → **M0.5 通过**，进入 M1-01。
- Test 1 红 → `NewSessionAgent` 隔离破坏 → 暂停，重构后再进 M1。
- Test 2 红（shared + 可变状态）→ 暂停 M1，先记录有状态 tool 清单。

---

## 验收清单

- [ ] `internal/agent/agent_isolation_test.go` 存在，`package agent`，含两个导出测试 + 两个辅助函数。
- [ ] `go test ./internal/agent/ -run 'TestSessionAgentInstancesAreIsolated|TestBuildToolsReturnsDistinctObjects' -v` 两测试 PASS。
- [ ] `go vet ./internal/agent/` 无告警。
- [ ] 无 Windows skip、无 API key 依赖、无 VCR 磁带 → CI 友好（跨平台）。
- [ ] 两次提交（Test 1、Test 2），可选第三次（spec 结果）。
- [ ] aliasing 表已记录（测试 `-v` 输出 + 可选 spec 第六节）。
