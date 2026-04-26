# Claude Code — Tool Prune 分析（源码级）

## 结论

**Claude Code 拥有最完善的 Tool Prune 系统：三种 prune 路径、精细的工具分类、Prompt Cache 感知、时间触发+阈值触发。**

## 核心文件

| 文件 | 职责 |
|------|------|
| `src/services/compact/microCompact.ts` | Tool Prune 主逻辑（时间触发 + 缓存编辑） |
| `src/services/compact/timeBasedMCConfig.ts` | 时间触发配置 |
| `src/services/compact/apiMicrocompact.ts` | API 端 context management |
| `src/constants/toolLimits.ts` | 工具输出大小常量 |
| `src/utils/toolResultStorage.ts` | 大工具结果持久化 |
| `src/query.ts` (360-460) | Prune 在 query loop 中的精确位置 |

---

## 一、生命周期位置

Tool Prune 在 `query.ts` 的 query loop 中，**每次 API 调用前**执行：

```
query.ts 执行顺序：

[365] getMessagesAfterCompactBoundary()    ← 获取上次压缩后的消息
[379] applyToolResultBudget()              ← 大输出持久化到磁盘（不是 prune）
[403] snipCompact()                        ← Snip 裁剪
[414] microcompactMessages()               ← ★★★ Tool Prune 在这里 ★★★
[441] contextCollapse()                    ← 上下文折叠
[454] autoCompactIfNeeded()                ← 自动摘要压缩
→ [API 调用] 发送给 LLM
[866] 处理 cache_deleted_input_tokens      ← ★ Prune 结果后处理
```

Prune 在步骤 4，位于 API 调用之前。被裁剪后的消息才是实际发送给 LLM 的内容。

---

## 二、三种 Prune 路径

### 路径 1：时间触发 Prune（Time-Based Microcompact）

**触发条件**：距离最后一个 assistant 消息 > 60 分钟。

**为什么**：Anthropic API 的 prompt cache TTL 是 60 分钟。超过 60 分钟后缓存一定过期，下次 API 调用无论如何都要重写完整前缀。此时清空旧输出可以**缩小重写量**。

**怎么做**：
```typescript
// microCompact.ts: maybeTimeBasedMicrocompact()

const compactableIds = collectCompactableToolIds(messages)
const keepRecent = Math.max(1, config.keepRecent)  // 默认保留最近 5 个
const keepSet = new Set(compactableIds.slice(-keepRecent))
const clearSet = new Set(compactableIds.filter(id => !keepSet.has(id)))

// 直接修改消息内容
result = messages.map(message => {
  const newContent = message.message.content.map(block => {
    if (block.type === 'tool_result' && clearSet.has(block.tool_use_id)) {
      return { ...block, content: '[Old tool result content cleared]' }
    }
    return block
  })
})
```

**特征**：
- 直接修改本地消息（反正 cache 已过期，破坏无所谓）
- 触发后重置 cached MC 状态
- 保留最近 5 个可裁剪工具的输出

### 路径 2：缓存编辑 Prune（Cached Microcompact）

**触发条件**：时间触发不满足（cache 仍有效），但可裁剪工具数量超过阈值。

**为什么**：cache 有效时不能修改消息内容（会 cache miss），但可以通过 `cache_edits` API 在服务端删除。

**怎么做**：
```typescript
// microCompact.ts: cachedMicrocompactPath()

// 注册所有工具结果
for (const message of messages) {
  mod.registerToolResult(state, block.tool_use_id)
  mod.registerToolMessage(state, groupIds)
}

// 计算需要删除的
const toolsToDelete = mod.getToolResultsToDelete(state)
if (toolsToDelete.length > 0) {
  const cacheEdits = mod.createCacheEditsBlock(state, toolsToDelete)
  pendingCacheEdits = cacheEdits
  // ★ 本地消息不变！删除在 API 层执行
  return { messages, compactionInfo: { pendingCacheEdits } }
}
```

**特征**：
- 不修改本地消息 → 前缀不变 → cache 命中
- API 返回后通过 `cache_deleted_input_tokens` 确认实际删除量
- 是"零成本"的 prune（不破坏 cache）

### 路径 3：API 端 Context Management

**文件**：`src/services/compact/apiMicrocompact.ts`

这是 Anthropic API 原生支持的上下文管理功能，通过 `context_management` 参数传递：

```typescript
// API 端可裁剪工具
const TOOLS_CLEARABLE_RESULTS = [
  'Bash', 'Glob', 'Grep', 'Read', 'WebFetch', 'WebSearch'
  // tool_use + tool_result 都可清除
]

// API 端只清调用
const TOOLS_CLEARABLE_USES = [
  'Edit', 'Write', 'NotebookEdit'
  // 只清除 tool_use，保留 tool_result
]

// 策略配置
{
  type: 'clear_tool_uses_20250919',
  trigger: { type: 'input_tokens', value: 180_000 },
  clear_at_least: { type: 'input_tokens', value: 140_000 },
  clear_tool_inputs: TOOLS_CLEARABLE_RESULTS
}
```

**特征**：由 API 服务端根据 token 预算自动决定清除哪些工具结果。

---

## 三、哪些工具可以裁剪

### 客户端可裁剪（COMPACTABLE_TOOLS）

```typescript
const COMPACTABLE_TOOLS = new Set([
  'Read',           // 文件读取 — 输出大，一次性消费
  'Bash',           // Shell 命令 — 输出大，一次性消费
  'Grep',           // 内容搜索 — 输出大，一次性消费
  'Glob',           // 文件搜索 — 输出中等，一次性消费
  'WebSearch',      // Web 搜索 — 输出大，一次性消费
  'WebFetch',       // Web 获取 — 输出大，一次性消费
  'Edit',           // 文件编辑 — 缓存编辑可裁
  'Write',          // 文件写入 — 缓存编辑可裁
])
```

### API 端可清结果（TOOLS_CLEARABLE_RESULTS）

```typescript
// tool_use + tool_result 都可清除
['Bash', 'Glob', 'Grep', 'Read', 'WebFetch', 'WebSearch']
```

### API 端只清调用（TOOLS_CLEARABLE_USES）

```typescript
// 只清 tool_use，保留 tool_result
['Edit', 'Write', 'NotebookEdit']
```

**为什么 Edit/Write 只清调用**：tool_result 包含编辑确认信息（"The file has been edited successfully"），对理解"做了什么修改"有后续参考价值。tool_use 包含完整编辑参数（schema 开销大），清除它可以节省 token。

### 不可裁剪的工具

| 工具 | 为什么 |
|------|--------|
| Agent | 子代理输出摘要，影响执行流 |
| Task | 任务管理，结果是任务状态 |
| Skill | Skill 执行结果和状态 |
| MCP 工具 | 第三方定义，行为不可预测 |
| ToolSearch | 工具搜索结果 |
| CronCreate/Delete | 定时任务管理 |
| Memory 相关 | 记忆管理 |

**设计原则**：
- 裁剪"大输出、一次性消费"的工具（Read/Bash/Grep 等）
- 保留"影响执行流"的工具输出（Agent/Task/Skill）
- 编辑类工具保留结果、只清调用（Edit/Write）

---

## 四、裁剪后消息变成什么

### 时间触发（直接修改）

```
之前：{ type: 'tool_result', content: '1000行文件内容...' }
之后：{ type: 'tool_result', content: '[Old tool result content cleared]' }
```

### 缓存编辑（API 层删除）

```
本地消息不变。
API 请求中添加 cache_edits 删除指令。
服务端处理删除，返回 cache_deleted_input_tokens 确认。
```

---

## 五、设计决策分析

### 为什么区分三种裁剪路径？

| 路径 | Cache 状态 | 代价 | 收益 |
|------|-----------|------|------|
| 时间触发 | 已过期 | 修改消息（反正 cache 已失效） | 缩小重写量 |
| 缓存编辑 | 有效 | 零（不破坏 cache） | 节省被删输出的 token |
| API 端管理 | — | API 服务端处理 | 自动化 |

**核心思想**：根据 cache 状态选择不同策略。cache 过期时粗暴清理（反正没损失），cache 有效时用精细手段（不破坏缓存）。

### 为什么有最小裁剪量限制？

时间触发没有限制（cache 已过期，裁多裁少都是净收益）。
缓存编辑没有限制（不破坏 cache，裁多少省多少）。
OpenCode 的 20K 限制存在是因为它直接改消息会破坏 cache——少量裁剪的收益抵不上 cache miss 的代价。

### 为什么时间触发阈值是 60 分钟？

Anthropic API 的 prompt cache TTL 是 60 分钟。60 分钟是"安全选择"——服务器的 cache 一定已经过期，不会强制产生本来不会发生的 miss。
