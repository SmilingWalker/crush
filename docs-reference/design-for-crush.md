# Tool Prune 技术方案设计

> 目标：为 Crush 设计 Tool Prune 机制。本文档对比现有实现的技术细节，给出方案建议。

---

## 一、现有实现的技术对比

### 1.1 两种技术路线

```
路线 A（OpenCode）：客户端直接修改消息内容
路线 B（Claude Code）：客户端标记 + API 层处理（缓存编辑）
```

#### 路线 A：客户端直接改消息

**OpenCode 的做法**：
```
遍历消息 → 找到旧工具输出 → 清空 output 字段 → 标记 compacted 时间戳 → 写回
```

**优势**：
- 实现简单，不依赖任何 API 特性
- 跨 Provider 兼容（OpenAI/Anthropic/Bedrock 都能用）
- 裁剪行为完全在客户端控制

**劣势**：
- 修改消息内容导致 prompt cache 失效，下次请求全部重传
- 需要最小裁剪量阈值（OpenCode 设 20K），否则裁剪收益抵不上 cache miss 代价
- 裁剪不可逆（OpenCode 清空 output 后无法恢复，Claude Code 至少保留了标记文本）

#### 路线 B：缓存编辑 / API 层处理

**Claude Code 的做法**：
```
遍历消息 → 找到旧工具输出 → 生成 cache_edits 删除指令 → 发给 API → 本地消息不变
```

**优势**：
- 不破坏 prompt cache，裁剪多少就省多少 token，永远是正收益
- 不需要最小裁剪量阈值
- 本地消息保持完整，可以随时取消裁剪

**劣势**：
- 依赖 Anthropic API 的 `cache_edits` 能力，其他 Provider 不可用
- 实现复杂度高（需要维护 cachedMCState、跟踪注册/删除、处理 API 响应中的 deleted tokens）
- 需要 feature flag 管理多路径切换

**结论对 Crush 的影响**：Crush 是 Go 实现，支持多 Provider（Anthropic/Bedrock/OpenAI/Vertex），路线 B 不可行。**应该走路线 A。**

---

### 1.2 触发时机对比

| 时机 | OpenCode | Claude Code | 适合 Crush？ |
|------|----------|-------------|-------------|
| **溢出时被动触发** | ✓ | ✓（兜底） | ✓ — 最简单，先做这个 |
| **时间触发**（cache 过期前） | ✗ | ✓（>60min） | 取决于是否用 Anthropic cache |
| **每轮主动触发** | ✗ | ✓（cached MC） | 太重，不建议 |

**建议**：Crush 先实现**溢出时被动触发**，与现有的 `shouldSummarize` 检测点复用。

---

### 1.3 工具分类策略对比

#### OpenCode：不区分（除 skill）

```
所有工具的输出都可以被裁剪，只要超出保护范围。
保护条件：最近 2 轮 + 最近 40K token。
例外：skill 工具永不裁剪。
```

**问题**：Agent 工具、MCP 工具的输出也被同等裁剪，可能丢失重要上下文。

#### Claude Code：精细区分三类

```
可裁结果（Read/Bash/Grep/Glob/WebSearch/WebFetch）：清空 tool_use + tool_result
只裁调用（Edit/Write/NotebookEdit）：只清 tool_use，保留 tool_result
不可裁（Agent/Task/Skill/MCP）：不动
```

**原理**：
- Read/Bash/Grep 输出是"消费型"——读完了就不需要了
- Edit/Write 的 tool_result 包含确认信息（"file edited successfully"），有参考价值
- Agent/MCP 的输出影响执行流，不可丢失

**建议**：Crush 应该采用 Claude Code 的分类思路，但可以根据自身工具集简化。

---

### 1.4 保护策略对比

| 策略 | OpenCode | Claude Code | 分析 |
|------|----------|-------------|------|
| **按轮次保护** | 最近 2 轮 | 无 | 轮次保护简单有效，LLM 最可能引用近期输出 |
| **按 token 保护** | 最近 40K token | 无 | 与轮次保护互补，防止近期大输出被误裁 |
| **按数量保护** | 无 | 最近 5 个工具 | 数量保护不精确（5 个大输出 vs 5 个小输出 token 差距大） |
| **按工具类型保护** | skill 永不裁 | Agent/Task/Skill/MCP 永不裁 | 类型保护更精准 |

**建议**：轮次保护 + token 保护 + 工具类型保护三重组合。

---

### 1.5 裁剪后的内容处理对比

| 做法 | 谁 | 优劣 |
|------|-----|------|
| **清空为空字符串** | OpenCode | 最省 token，但 LLM 不知道这个工具之前做了什么 |
| **替换为固定标记** | Claude Code | 多占几个 token，但 LLM 知道"之前有个工具调用被清了" |
| **替换为摘要** | 无人实现 | 理想但需要额外 LLM 调用 |

**建议**：替换为固定标记（如 `[tool output pruned]`），让 LLM 知道有内容被清空。

---

## 二、Crush 的现状分析

### 2.1 现有代码结构

```
internal/agent/coordinator.go  → buildTools() 构建工具列表
                               → Stream() 中 StopWhen 检查 shouldSummarize

internal/agent/agent.go        → Summarize() 调用 LLM 生成摘要
                               → preparePrompt() 过滤孤立消息

internal/config/config.go      → resolveAllowedTools() 工具白名单
                               → resolveReadOnlyTools() 只读工具集
```

### 2.2 现有工具集

```go
func allToolNames() []string {
    return []string{
        "agent", "bash", "crush_info", "crush_logs", "job_output", "job_kill",
        "download", "edit", "multiedit", "lsp_diagnostics", "lsp_references",
        "lsp_restart", "fetch", "agentic_fetch", "glob", "grep", "ls",
        "sourcegraph", "todos", "view", "write",
        "list_mcp_resources", "read_mcp_resource",
    }
}
```

### 2.3 现有溢出检测点

```go
// coordinator.go StopWhen
tokens := currentSession.CompletionTokens + currentSession.PromptTokens
remaining := contextWindow - tokens
if remaining <= threshold && !disableAutoSummarize {
    shouldSummarize = true
}
```

目前：`shouldSummarize = true` → 直接跳到全量摘要。
改造：`shouldPrune = true` → 先尝试 prune，不够再 shouldSummarize。

---

## 三、Crush 的工具分类建议

### 3.1 建议分类

| 类别 | 工具 | 理由 |
|------|------|------|
| **可裁剪** | `bash`, `grep`, `glob`, `view`, `ls`, `sourcegraph`, `fetch`, `agentic_fetch` | 输出大，一次性消费，裁剪收益高 |
| **只裁结果** | `edit`, `multiedit`, `write` | tool_use（编辑参数）开销大可裁，tool_result（确认信息）保留 |
| **不可裁剪** | `agent`, `crush_info`, `crush_logs`, `job_output`, `job_kill`, `download`, `todos`, `lsp_*`, MCP 工具 | 控制流/状态类工具 |

### 3.2 为什么这么分

**可裁剪的工具共性**：
- 输出是文件内容/搜索结果/命令输出——"看过了就不需要原文了"
- 输出通常很大（文件内容可能数千行）
- 即使输出被清空，LLM 通过 tool_use（调用参数）仍然知道"读了什么文件、搜了什么关键词、跑了什么命令"

**编辑类工具**：
- tool_use 包含完整的编辑参数（old_string + new_string），token 开销大
- tool_result 是简短的确认信息，开销小但有参考价值
- 选择：清空 tool_use，保留 tool_result

**不可裁剪的工具共性**：
- `agent`：子代理的输出摘要影响当前任务决策
- `job_output/job_kill`：后台任务状态，丢失会导致任务管理混乱
- `todos`：待办事项状态
- `lsp_*`：诊断信息、引用关系，开发上下文的一部分
- MCP 工具：第三方定义，行为不可预测

---

## 四、实现方案建议

### 4.1 整体流程

```
现有流程：
  StopWhen → shouldSummarize → 直接 Summarize()

改造后：
  StopWhen → shouldPrune → Prune()
                            ↓ (prune 后空间不够)
                           shouldSummarize → Summarize()
```

### 4.2 Prune 函数签名

```go
const (
    PruneProtectTurns  = 2       // 保护最近 2 轮
    PruneProtectTokens = 40_000  // 保护最近 40K token
    PruneMinimum       = 20_000  // 最少裁剪 20K token
)

var prunableTools = map[string]bool{
    "bash": true, "grep": true, "glob": true, "view": true,
    "ls": true, "sourcegraph": true, "fetch": true, "agentic_fetch": true,
}

var clearableToolUses = map[string]bool{
    "edit": true, "multiedit": true, "write": true,
}

func (c *coordinator) prune(ctx context.Context, messages []fantasy.Message) ([]fantasy.Message, int, error) {
    // 返回：裁剪后的消息列表，释放的 token 数，错误
}
```

### 4.3 Prune 逻辑

```
1. 从最新消息到最旧消息逆向扫描
2. 跳过最近 2 轮（保护）
3. 累计工具输出的 token 估算
4. 在 40K token 保护带内的工具输出保留
5. 超出保护带的、属于 prunableTools 的工具输出 → 标记裁剪
6. 属于 clearableToolUses 的工具 → 清空 tool_use 参数，保留 tool_result
7. 计算总释放 token
8. 如果 >= PruneMinimum → 执行裁剪
9. 如果 < PruneMinimum → 不裁剪（不值得 cache miss 的代价）
```

### 4.4 裁剪后内容

```go
// 可裁剪工具：替换 tool_result 为标记
toolResult.Content = "[tool output pruned]"

// 可清调用工具：清空 tool_use 参数
toolUse.Input = "{}"  // 或移除参数中的大字段
```

### 4.5 集成点

```go
// coordinator.go StopWhen
if remaining <= threshold {
    // 新增：先尝试 prune
    prunedMessages, freed, err := c.prune(ctx, currentMessages)
    if err == nil && freed >= PruneMinimum {
        currentMessages = prunedMessages
        // 重新计算 remaining，可能不再需要 summarize
    }
    
    // 如果 prune 不够，仍然 summarize
    if stillNeedsSummarize {
        shouldSummarize = true
    }
}
```

---

## 五、风险与注意事项

### 5.1 Prompt Cache 代价

直接修改消息会破坏 Anthropic API 的 prompt cache。需要确保裁剪释放的 token 远大于 cache miss 的代价。

**缓解**：PruneMinimum = 20K。只有裁剪量 >= 20K 时才执行，确保正收益。

### 5.2 孤立 tool_result / tool_use

裁剪 tool_result 时不能只删 result 不删 use——API 要求配对。裁剪 tool_use 时不能只删 use 不删 result——同理。

**缓解**：
- 可裁剪工具：替换 tool_result 内容为标记，保留 tool_use（结构完整）
- 可清调用工具：只清 tool_use 的参数内容，保留 tool_result

### 5.3 多 Provider 兼容

Crush 支持多 Provider，不同 Provider 对消息格式要求不同。

**缓解**：Prune 只修改 content 字段，不改变消息结构，所有 Provider 都能处理。

### 5.4 Prune 后 LLM 行为

LLM 看到 `[tool output pruned]` 后可能：
- 重新调用同一工具获取输出（浪费）
- 困惑于缺少上下文

**缓解**：在 system prompt 中告知 LLM 旧工具输出可能被裁剪，如果需要可以重新调用工具。

### 5.5 与 Summarize 的协调

Prune 后如果空间仍然不够，需要继续 Summarize。需要确保 Summarize 消息序列正确处理已裁剪的工具输出。

**缓解**：已裁剪的 `[tool output pruned]` 内容很短，不影响摘要质量。
