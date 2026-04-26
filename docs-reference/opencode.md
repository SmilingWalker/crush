# OpenCode — Tool Prune 分析

## 结论

**OpenCode 有 Tool Prune，是第二个实现此机制的 agent。** 采用逆向扫描 + 位置保护策略，不区分工具类型。

## 核心文件

| 文件 | 职责 |
|------|------|
| `packages/opencode/src/session/compaction.ts` | prune 主逻辑 |
| `packages/opencode/src/session/overflow.ts` | 溢出检测 |

## Prune 机制详解

### 什么时候触发

**溢出后被动触发**：当 `isOverflow` 返回 true 时，先尝试 prune，不够再 compaction。

```typescript
// overflow.ts
const COMPACTION_BUFFER = 20_000

export function isOverflow(input) {
  const count = tokens.total || tokens.input + tokens.output + tokens.cache.read + tokens.cache.write
  const usable = input.limit - reserved  // reserved = min(20K, maxOutputTokens)
  return count >= usable
}
```

**在什么生命周期**：每轮 LLM 调用之后，检测是否溢出。如果溢出，在下一轮 LLM 调用之前执行 prune。

### 怎么裁剪

```typescript
// compaction.ts
const PRUNE_MINIMUM = 20_000   // 最少裁剪 20K token 才执行
const PRUNE_PROTECT = 40_000   // 保护最近 40K token
const PRUNE_PROTECTED_TOOLS = ["skill"]

const prune = Effect.fn("SessionCompaction.prune")(function* (input) {
  let total = 0, pruned = 0
  const toPrune: MessageV2.ToolPart[] = []
  let turns = 0

  // 从最新到最旧逆向扫描
  loop: for (let msgIndex = msgs.length - 1; msgIndex >= 0; msgIndex--) {
    const msg = msgs[msgIndex]
    if (msg.info.role === "user") turns++
    if (turns < 2) continue                     // ★ 保护最近 2 轮
    if (msg.info.role === "assistant" && msg.info.summary) break  // 遇到之前的摘要停

    for (let partIndex = msg.parts.length - 1; partIndex >= 0; partIndex--) {
      const part = msg.parts[partIndex]
      if (part.type === "tool" && part.state.status === "completed") {
        if (PRUNE_PROTECTED_TOOLS.includes(part.tool)) continue  // ★ skill 永不裁
        if (part.state.time.compacted) break loop                // 已裁过停

        const estimate = Token.estimate(part.state.output)
        total += estimate
        if (total > PRUNE_PROTECT) {  // ★ 超出 40K 保护范围
          pruned += estimate
          toPrune.push(part)
        }
      }
    }
  }

  // ★ 最小裁剪量：低于 20K token 不执行
  if (pruned > PRUNE_MINIMUM) {
    for (const part of toPrune) {
      part.state.time.compacted = Date.now()  // 标记防重复
      yield* session.updatePart(part)
    }
  }
})
```

### 哪些工具可以裁剪

**所有工具都可以被裁剪，除了 `skill`。**

没有工具类型白名单。Bash、Read、Edit、Write、MCP 工具、Agent 工具——全部一视同仁。

### 保护策略

**不是按工具类型保护，而是按位置保护**：

```
消息序列：  [user] [asst] [user] [asst] [user] [asst] [user] [asst]
位置：       旧 ←──────────────────────────────────────────────→ 新
                                                         ↑
                                                      最近 2 轮
                                                      全部保护

再往前：最近 40K token 内的工具输出全部保护
        超出 40K 的工具输出 → 裁剪候选
```

### 裁剪后变成什么

清空工具的 output 字段，标记时间戳防重复裁剪：

```
// 之前
part.state.output = "1000 行工具输出..."
part.state.time.compacted = undefined

// 之后
part.state.output = ""  // 清空
part.state.time.compacted = Date.now()  // 标记
```

### 为什么这么做

1. **逆向扫描**：从最新到最旧，确保最早裁剪的是最旧、价值最低的工具输出
2. **保护最近 2 轮**：最近的工具调用最可能被 LLM 引用，必须保留
3. **40K token 保护带**：即使超过 2 轮，只要在 40K token 范围内也保护，防止误裁近期重要输出
4. **20K 最小裁剪量**：少量裁剪的收益不值得破坏 prompt cache 的代价
5. **时间标记防重复**：`compacted = Date.now()` 确保已裁剪的部分不会被再次处理

### 问题

1. **不区分工具类型**：可能裁剪掉 Agent/MCP 工具的重要输出
2. **无 Cache 感知**：直接修改消息内容导致 cache miss
3. **清空不持久化**：清空的 output 直接丢弃，无法恢复
4. **只在溢出时触发**：没有主动清理机制，不会在 cache 过期前提前释放空间
