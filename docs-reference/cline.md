# Cline — Tool Prune 分析

## 结论

**Cline 没有真正的 Tool Prune。** 它做的是 Message Prune——删除整个对话轮次（user+assistant 消息对），而不是只清空工具输出保留对话结构。

## Cline 的上下文压缩机制

### 一、文件读取去重（最接近 Tool Prune 的机制）

**文件**：`src/core/context/context-management/ContextManager.ts` (606-691)

**做什么**：检测同一文件被多次读取时，将较早的读取内容替换为通知。

```
// 之前：完整的文件内容（可能几千 token）
{ role: "user", content: "Read file src/app.ts:\n1000行代码..." }

// 之后：替换为通知
{ role: "user", content: "[NOTE] This file read has been removed to save space. The file has been read again more recently." }
```

**触发时机**：`useAutoCondense` 开启且 token 接近阈值时，在尝试更激进的截断之前。

**效果**：可节省 30%+ 字符数。

**为什么这么做**：Code Agent 经常反复读取同一文件（修改前读一次、修改后读一次、调试时再读一次），旧的读取内容冗余。

### 二、按比例删除对话轮次（Message Prune）

**文件**：`src/core/context/context-management/ContextManager.ts` (240-282)

**四种策略**：

| 策略 | 行为 |
|------|------|
| `half` | 删除剩余 user-assistant 对的一半 |
| `quarter` | 删除剩余的 3/4 |
| `lastTwo` | 只保留最后一对 |
| `none` | 删除首对核心消息之后的所有消息 |

**触发时机**：每轮 API 调用前检查总 token，超过 `maxAllowedSize` 时触发。

**各模型的 maxAllowedSize**：
- DeepSeek：128K - 27K = 101K
- 大多数模型：128K - 30K = 98K
- Claude：200K - 40K = 160K

**这为什么不是 Tool Prune**：删除的是整个消息对，包括用户的指令和 assistant 的推理，不是只清空工具输出。

### 三、summarize_task 完全重置

**文件**：`src/core/prompts/system-prompt/tools/SummarizeTaskHandler.ts`

删除所有对话历史，只保留初始任务消息。是 auto-condense 链的最后手段。

## 裁剪时机

```
工具执行 → 即时截断(400KB上限)
    ↓
每轮 API 调用前：
  → 文件读取去重（如果 token 接近阈值）
  → 按比例删消息对（如果超过阈值）
  → summarize_task（如果去重和删消息对都不够）
    ↓
API 返回溢出错误 → 更激进策略重试
```

## 与 Tool Prune 的对比

| 维度 | Tool Prune（理想） | Cline 的做法 |
|------|-------------------|-------------|
| 裁剪粒度 | 工具输出级别 | 消息对级别 |
| 对话结构 | 保留 | 破坏 |
| user 指令 | 保留 | 随消息对一起删除 |
| assistant 推理 | 保留 | 随消息对一起删除 |
| 工具调用记录 | 保留 | 随消息对一起删除 |
| 工具输出内容 | 清空 | 随消息对一起删除 |
| 信息损失 | 只丢失工具输出 | 丢失整个对话轮次 |
