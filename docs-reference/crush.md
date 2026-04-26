# Crush — Tool Prune 分析

## 结论

**Crush 没有 Tool Prune。** 没有任何 prune 机制。它从"全量上下文"直接跳到"LLM 摘要替换全部"。

## Crush 的上下文压缩机制

### 一、溢出检测

**文件**：`internal/agent/agent.go` (434-459)

**阈值策略**：

```go
const (
    largeContextWindowThreshold = 200_000  // 大上下文窗口阈值
    largeContextWindowBuffer    = 20_000   // 大窗口预留 buffer
    smallContextWindowRatio     = 0.2      // 小窗口用 20% 作为 buffer
)

// 每轮 LLM 响应后检查
remaining := contextWindow - (completionTokens + promptTokens)

// 大窗口(>200K)：remaining <= 20K 时触发
// 小窗口：remaining <= 20% 时触发
if remaining <= threshold && !disableAutoSummarize {
    shouldSummarize = true
}
```

**为什么区分大小窗口**：大窗口（如 Claude 200K）有足够的绝对空间，固定 20K buffer 合理。小窗口（如 GPT-4 8K）20K 可能占 250%，用百分比更合适。

### 二、直接跳到摘要（无中间层）

**文件**：`internal/agent/agent.go` (618-730)

当 `shouldSummarize = true` 时，直接调用 LLM 生成结构化摘要替换全部对话历史。没有 prune（清空旧工具输出）这个中间步骤。

**摘要模板**（`internal/agent/templates/summary.md`）包含：
- Current State
- Files
- Technical Context
- Strategy
- Next Steps

模板明确声明："This summary will be the ONLY context available when the conversation resumes. Assume all previous messages will be lost."

### 三、消息预处理（不是 prune）

**文件**：`internal/agent/agent.go` (767-832)

`preparePrompt` 在每次构建 API 消息序列时做清理：
- 过滤孤立 tool_result（没有对应 tool_call 的）
- 为孤立 tool_call 注入合成 tool_result（API 要求配对）
- 跳过空 assistant 消息

这不是 prune，而是 API 兼容性处理。

### 四、工具输出截断（不是 prune）

**文件**：`internal/agent/tools/view.go`

只有 view 工具有三维截断：100KB / 2000行 / 2000字符/行。其他工具（bash、grep 等）的输出完全不受限。

## 裁剪时机

```
view 工具 → 三维截断（100KB/2000行/2000字符）
其他工具 → 无截断
    ↓
每轮 LLM 响应后 → 检查 remaining tokens
    ↓ (remaining <= threshold)
直接调用 LLM 生成摘要 → 替换全部对话历史
    ↓
构建消息序列时 → 过滤孤立消息
```

## 问题

没有 Tool Prune 意味着：
1. 每轮 API 调用都发送**完整的**历史工具输出，即使那些输出已经没有价值
2. 没有渐进释放空间的手段，只能等到溢出后一次性全量替换
3. 长对话中，大部分上下文被旧工具输出占据，真正有价值的对话内容反而被挤掉
