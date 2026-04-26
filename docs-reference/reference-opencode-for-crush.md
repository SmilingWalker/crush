# OpenCode → Crush 参考实现

> 以 OpenCode 的 prune 算法为蓝本，映射到 Crush 的 Go 代码结构，给出可直接实现的伪代码。

---

## 一、数据模型映射

### 1.1 消息结构

```
OpenCode (TypeScript)                    Crush (Go)
─────────────────────                    ──────────
MessageV2.WithParts                      message.Message
  info.role: "user"|"assistant"            Role: MessageRole ("user"|"assistant"|"tool")
  info.summary: boolean                   IsSummaryMessage: bool
  parts: MessagePart[]                    Parts: []ContentPart

MessageV2.ToolPart (嵌入在消息中)         message.ToolCall + message.ToolResult（分离存储）
  tool: string                            ToolCall.Name
  state.output: string                    ToolResult.Content
  state.status: "completed"              ToolCall.Finished == true 且存在对应 ToolResult
  state.time.compacted: number           ★ 不存在，需新增 PrunedAt 字段
```

**关键差异**：OpenCode 的 tool 调用和结果是同一个 `ToolPart`，Crush 的 tool 调用在 assistant 消息的 `Parts` 中（`ToolCall` 类型），tool 结果是独立的 tool-role 消息（`ToolResult` 类型）。

### 1.2 需要新增的字段

```go
// message/content.go — ToolResult 新增字段
type ToolResult struct {
    ToolCallID string `json:"tool_call_id"`
    Name       string `json:"name"`
    Content    string `json:"content"`
    Data       string `json:"data"`
    MIMEType   string `json:"mime_type"`
    Metadata   string `json:"metadata"`
    IsError    bool   `json:"is_error"`
    PrunedAt   int64  `json:"pruned_at,omitempty"` // ★ 新增：裁剪时间戳，0 表示未裁剪
}
```

**为什么加在 ToolResult 而不是 ToolCall 上**：OpenCode 裁剪的是 `state.output`（工具输出），对应 Crush 的 `ToolResult.Content`。标记也应在 ToolResult 上，与裁剪目标一致。

### 1.3 数据库迁移

```
ToolResult 是 JSON 序列化存储在 message.Parts 字段中。
PrunedAt 字段会随 JSON 自动持久化，无需数据库迁移。
旧数据的 PrunedAt 默认为 0（JSON omitempty 已处理）。
```

---

## 二、工具分类定义

### 2.1 OpenCode 的做法

```typescript
// 不区分工具类型，除 skill 外全部可裁
const PRUNE_PROTECTED_TOOLS = ["skill"]
```

### 2.2 Crush 应该做的

OpenCode 不区分工具类型是它的一个已知问题（可能裁掉 agent/MCP 的重要输出）。Crush 应采用 Claude Code 的分类思路，但简化实现：

```go
// internal/agent/prune.go

// 可裁剪工具：输出大、一次性消费，裁剪后 LLM 仍可通过 tool_use 知道"做了什么"
var prunableTools = map[string]bool{
    "bash":           true, // 命令输出，通常很大
    "grep":           true, // 搜索结果，一次性消费
    "glob":           true, // 文件列表，一次性消费
    "view":           true, // 文件内容，读过后不需要原文
    "ls":             true, // 目录列表，一次性消费
    "sourcegraph":    true, // 代码搜索结果，一次性消费
    "fetch":          true, // Web 内容，一次性消费
    "agentic_fetch":  true, // Web 内容，一次性消费
    "crush_info":     true, // 信息查询，一次性
    "crush_logs":     true, // 日志输出，一次性
}

// 只清调用的工具：tool_use 参数开销大可清，tool_result 保留
var clearableToolUses = map[string]bool{
    "edit":     true, // old_string + new_string 开销大
    "multiedit": true, // 同 edit
    "write":    true, // 完整文件内容开销大
}

// 以下工具永不裁剪（不在上面两个 map 中的）：
// agent, job_output, job_kill, download, todos,
// lsp_diagnostics, lsp_references, lsp_restart,
// list_mcp_resources, read_mcp_resource,
// 以及所有 MCP 动态工具
```

### 2.3 为什么这样分

| 类别 | 思路 | 参考来源 |
|------|------|---------|
| **prunableTools** | 输出是文件内容/搜索结果/命令输出，LLM 通过 tool_use 参数仍知"做了什么" | Claude Code 的 TOOLS_CLEARABLE_RESULTS |
| **clearableToolUses** | tool_result 是简短确认（"file edited"），有参考价值；tool_use 是完整编辑内容，开销大 | Claude Code 的 TOOLS_CLEARABLE_USES |
| **不在任何 map 中** | 控制流/状态类工具，裁剪会破坏执行逻辑 | Claude Code 的不可裁剪工具集 |

---

## 三、核心算法：prune 函数

### 3.1 OpenCode 原始算法（TypeScript）

```typescript
// compaction.ts:86-132
const prune = Effect.fn("SessionCompaction.prune")(function* (input) {
  const msgs = yield* session.messages({ sessionID: input.sessionID })

  let total = 0, pruned = 0
  const toPrune: MessageV2.ToolPart[] = []
  let turns = 0

  // 从最新到最旧逆向扫描
  loop: for (let msgIndex = msgs.length - 1; msgIndex >= 0; msgIndex--) {
    const msg = msgs[msgIndex]
    if (msg.info.role === "user") turns++
    if (turns < 2) continue                        // 保护最近 2 轮
    if (msg.info.role === "assistant" && msg.info.summary) break loop

    for (let partIndex = msg.parts.length - 1; partIndex >= 0; partIndex--) {
      const part = msg.parts[partIndex]
      if (part.type === "tool" && part.state.status === "completed") {
        if (PRUNE_PROTECTED_TOOLS.includes(part.tool)) continue
        if (part.state.time.compacted) break loop   // 已裁过停

        const estimate = Token.estimate(part.state.output)
        total += estimate
        if (total > PRUNE_PROTECT) {                // 超出 40K 保护范围
          pruned += estimate
          toPrune.push(part)
        }
      }
    }
  }

  if (pruned > PRUNE_MINIMUM) {                     // 少于 20K 不裁
    for (const part of toPrune) {
      part.state.time.compacted = Date.now()
      yield* session.updatePart(part)
    }
  }
})
```

### 3.2 Crush 的消息模型差异分析

OpenCode 的 ToolPart 把调用和结果合在一个对象里，遍历时一次就能拿到 `tool`（工具名）、`state.output`（输出内容）、`state.time.compacted`（裁剪标记）。

Crush 的模型是**分离的**：

```
assistant 消息: Parts = [TextContent, ToolCall{ID:"t1", Name:"bash", Input:"..."}, ToolCall{ID:"t2", Name:"edit", Input:"..."}]
tool 消息:      Parts = [ToolResult{ToolCallID:"t1", Content:"命令输出..."}, ToolResult{ToolCallID:"t2", Content:"edit result"}]
```

这意味着扫描策略有两种选择：

#### 方案 A：只扫描 tool-role 消息（推荐）

**思路**：prune 的主要目标是 `ToolResult.Content`（占 token 大头），只遍历 tool-role 消息即可。

- 优点：逻辑简单，只需关注一类消息
- 对于 `clearableToolUses`（edit/multiedit/write），通过 `ToolResult.ToolCallID` 回溯 assistant 消息找对应 `ToolCall` 来清空 Input
- 预构建 `toolCallID → assistantMsgIndex` 映射，回溯查找 O(1)

#### 方案 B：按 assistant-turn 分组扫描

**思路**：把 assistant 消息 + 紧随的 tool 消息视为一个 "turn"，在 turn 内部处理配对。

- 更接近 OpenCode 的 `msg.parts` 遍历语义
- 实现更复杂，需要额外的分组逻辑

**结论：用方案 A。** prune 的核心目标——释放 `ToolResult.Content` 占用的 token——只需要扫描 tool-role 消息。ToolCall 的清空是辅助操作，用索引映射即可高效完成。

### 3.3 clearableToolUses 的回溯查找

edit/multiedit/write 需要清空 `ToolCall.Input`。如果每个裁剪候选都遍历所有 assistant 消息查找对应 ToolCall，复杂度是 O(n*m)。

**解决**：在 prune 开始时一次遍历构建索引：

```go
// 预构建 ToolCallID → assistant 消息索引
toolCallIndex := make(map[string]int) // toolCallID → msgs 中的索引
for i, msg := range msgs {
    if msg.Role != message.Assistant {
        continue
    }
    for _, part := range msg.Parts {
        if tc, ok := part.(message.ToolCall); ok {
            toolCallIndex[tc.ID] = i
        }
    }
}
```

后续清空 ToolCall.Input 时直接 `msgs[toolCallIndex[tr.ToolCallID]]` 定位，O(1)。

### 3.4 ToAIMessage 的兼容性

裁剪后 `ToolResult.Content = "[tool output pruned]"`，经过 `ToAIMessage()` 转换时（`content.go:536-563`）：

```go
case Tool:
    for _, result := range m.ToolResults() {
        content = fantasy.ToolResultOutputContentText{Text: result.Content}
        parts = append(parts, fantasy.ToolResultPart{ToolCallID: result.ToolCallID, Output: content})
    }
```

`Content` 是一个普通字符串，被包裹为 `ToolResultOutputContentText`。替换为 `[tool output pruned]` 不会破坏任何结构——API 只要求 tool_result 存在且非空。

### 3.5 完整 Go 实现

```go
// internal/agent/prune.go

package agent

import (
    "context"
    "fmt"
    "time"

    "github.com/anthropics/crush/internal/message"
)

const (
    PruneProtectTurns  = 2       // 保护最近 2 轮（与 OpenCode 一致）
    PruneProtectTokens = 40_000  // 保护最近 40K token（与 OpenCode 一致）
    PruneMinimum       = 20_000  // 最少裁剪 20K token（与 OpenCode 一致）
)

// 可裁剪工具：输出大、一次性消费
var prunableTools = map[string]bool{
    "bash": true, "grep": true, "glob": true, "view": true,
    "ls": true, "sourcegraph": true, "fetch": true, "agentic_fetch": true,
    "crush_info": true, "crush_logs": true,
}

// 只清调用的工具：清空 ToolCall.Input，保留 ToolResult
var clearableToolUses = map[string]bool{
    "edit": true, "multiedit": true, "write": true,
}

// pruneResult 记录裁剪结果
type pruneResult struct {
    FreedTokens int
    PrunedCount int
}

// prune 对消息列表执行 Tool Prune。
// 调用方在 Stream 结束后调用此函数，prune 修改数据库中的消息。
func (a *sessionAgent) prune(ctx context.Context, msgs []message.Message) (pruneResult, error) {
    var result pruneResult

    // ★ 预构建 ToolCallID → assistant 消息索引（用于 clearableToolUses 回溯）
    toolCallIndex := make(map[string]int) // toolCallID → msgs 中的索引
    toolCallPartIndex := make(map[string]int) // toolCallID → Parts 中的索引
    for i, msg := range msgs {
        if msg.Role != message.Assistant {
            continue
        }
        for j, part := range msg.Parts {
            if tc, ok := part.(message.ToolCall); ok {
                toolCallIndex[tc.ID] = i
                toolCallPartIndex[tc.ID] = j
            }
        }
    }

    total := 0    // 从最新往旧扫描累计的 token 估算
    pruned := 0   // 裁剪候选的总 token

    type pruneTarget struct {
        msgIndex  int    // tool-role 消息在 msgs 中的索引
        partIndex int    // ToolResult 在 Parts 中的索引
        toolName  string // 工具名称，决定裁剪策略
    }
    var targets []pruneTarget

    turns := 0

    // 从最新到最旧逆向扫描
loop:
    for msgIndex := len(msgs) - 1; msgIndex >= 0; msgIndex-- {
        msg := msgs[msgIndex]

        // 轮次判定：user-role 消息标志着新的一轮
        if msg.Role == message.User {
            turns++
        }
        if turns < PruneProtectTurns {
            continue
        }

        // 遇到摘要消息停止
        if msg.Role == message.Assistant && msg.IsSummaryMessage {
            break loop
        }

        // 只处理 tool-role 消息中的 ToolResult
        if msg.Role == message.Tool {
            for partIndex := len(msg.Parts) - 1; partIndex >= 0; partIndex-- {
                tr, ok := msgs[msgIndex].Parts[partIndex].(message.ToolResult)
                if !ok {
                    continue
                }

                // 已裁剪过，停止扫描
                if tr.PrunedAt > 0 {
                    break loop
                }

                toolName := tr.Name

                // 检查是否属于可裁剪工具
                if !prunableTools[toolName] && !clearableToolUses[toolName] {
                    continue
                }

                // 估算 token 数
                estimate := estimateTokens(tr.Content)
                total += estimate

                if total > PruneProtectTokens {
                    pruned += estimate
                    targets = append(targets, pruneTarget{
                        msgIndex:  msgIndex,
                        partIndex: partIndex,
                        toolName:  toolName,
                    })
                }
            }
        }
    }

    // 最小裁剪量检查
    if pruned < PruneMinimum {
        return result, nil
    }

    // 执行裁剪
    now := time.Now().UnixMilli()
    // 记录需要持久化的消息（去重）
    updatedMsgs := make(map[int]bool)

    for _, t := range targets {
        tr := msgs[t.msgIndex].Parts[t.partIndex].(message.ToolResult)

        if prunableTools[t.toolName] {
            // 可裁剪工具：替换 ToolResult.Content 为标记
            tr.Content = "[tool output pruned]"
            tr.Data = ""
            tr.PrunedAt = now
            msgs[t.msgIndex].Parts[t.partIndex] = tr
            updatedMsgs[t.msgIndex] = true
        } else if clearableToolUses[t.toolName] {
            // 只清调用工具：标记 ToolResult，清空对应 ToolCall.Input
            tr.PrunedAt = now
            msgs[t.msgIndex].Parts[t.partIndex] = tr
            updatedMsgs[t.msgIndex] = true

            // 通过预构建索引回溯查找 ToolCall
            if assstIdx, ok := toolCallIndex[tr.ToolCallID]; ok {
                if partIdx, ok2 := toolCallPartIndex[tr.ToolCallID]; ok2 {
                    tc := msgs[assstIdx].Parts[partIdx].(message.ToolCall)
                    tc.Input = "{}"
                    msgs[assstIdx].Parts[partIdx] = tc
                    updatedMsgs[assstIdx] = true
                }
            }
        }
    }

    // 持久化修改（去重，每条消息只 Update 一次）
    for msgIdx := range updatedMsgs {
        if err := a.messages.Update(ctx, msgs[msgIdx]); err != nil {
            return result, fmt.Errorf("prune: update message %s: %w", msgs[msgIdx].ID, err)
        }
    }

    result.FreedTokens = pruned
    result.PrunedCount = len(targets)
    return result, nil
}

// 粗略估算 token 数（4 字符 ≈ 1 token）
func estimateTokens(text string) int {
    return len(text) / 4
}
```

---

## 四、集成点：Stream 结束后 → Prune → Summarize

### 4.1 现有流程（agent.go 完整调用链）

```
Run() [agent.go:230]
  ├─ line 245: preparePrompt(msgs) → 构建 history
  ├─ line 257: agent.Stream(call) → 开始流式调用
  │   内部循环（每轮 tool 调用）：
  │   ├─ PrepareStep [line 268] → 每轮 API 调用前准备消息、创建 assistant 消息
  │   ├─ OnToolResult [line 392] → 工具执行结果写入 tool-role 消息
  │   ├─ OnStepFinish [line 404] → 更新 session token 计数
  │   └─ StopWhen [line 430] → 检查是否停止
  │       → shouldSummarize = true, return true → Stream 返回
  ├─ line 582: 检查 shouldSummarize
  │   → Summarize() [line 613] → 全量摘要
  │   → 重新排队继续对话 [line 588-596]
  └─ line 603: 处理消息队列中的后续消息
```

### 4.2 prune 执行时机的选择

有两种可能的集成位置：

#### 选项 1：在 StopWhen 回调内执行 prune（❌ 不推荐）

```go
StopWhen: []fantasy.StopCondition{
    func(_ []fantasy.StepResult) bool {
        // ... 计算 remaining <= threshold
        msgs, _ := a.getSessionMessages(ctx, currentSession)
        result, _ := a.prune(ctx, msgs)
        if result.FreedTokens >= PruneMinimum {
            return false  // 不停止 Stream
        }
        return true  // 停止，走 Summarize
    },
},
```

**问题**：StopWhen 在 `Stream()` 的内部循环中执行。如果 prune 成功并返回 `false`（不停止），Stream 继续运行下一轮 API 调用。但 Stream 内部的消息状态（`options.Messages`）是基于之前的 `preparePrompt()` 构建的，不会从数据库重新读取。prune 修改了数据库中的消息，但 Stream 的内存消息缓存不会更新，下一轮 API 调用仍会发送未裁剪的内容。

#### 选项 2：在 Stream 结束后、Summarize 之前执行 prune（✅ 推荐）

```go
// line 582 改造
if shouldPruneOrSummarize {
    // 先尝试 prune
    // ...
    // prune 成功 → 重新排队，下一轮 Run 从数据库读取裁剪后的消息
    // prune 失败 → 走 Summarize
}
```

**优势**：
1. 不侵入 `Stream()` 内部循环
2. prune 修改数据库后，下一轮 `Run()` 调用 `preparePrompt()` → `getSessionMessages()` 从数据库重新读取，自然拿到裁剪后的版本
3. 逻辑清晰，与现有 Summarize 流程平级

### 4.3 推荐方案：选项 2 的完整流程

```
Run()
  ├─ preparePrompt() → history
  ├─ Stream() → 多轮 tool 调用
  │   └─ StopWhen 触发 → shouldPruneOrSummarize = true → Stream 返回
  ├─ 检查 shouldPruneOrSummarize                          [line 582 改造]
  │   ├─ prune(currentSession)
  │   │   ├─ 成功（FreedTokens >= 20K）
  │   │   │   ├─ 更新 session.PromptTokens 估算值
  │   │   │   └─ 重新排队继续对话（不走 Summarize）
  │   │   └─ 失败（释放不够或出错）
  │   │       └─ 走原有 Summarize 路径
  │   └─ Summarize()（原有逻辑不变）
  └─ 处理消息队列中的后续消息
```

### 4.4 具体代码改造

#### 4.4.1 StopWhen 改造（最小改动）

```go
// agent.go:251 附近
var shouldPruneOrSummarize bool  // 替代原来的 shouldSummarize

// agent.go:430-455 StopWhen（只改变量名和赋值）
StopWhen: []fantasy.StopCondition{
    func(_ []fantasy.StepResult) bool {
        cw := int64(largeModel.CatwalkCfg.ContextWindow)
        if cw == 0 {
            return false
        }
        tokens := currentSession.CompletionTokens + currentSession.PromptTokens
        remaining := cw - tokens
        var threshold int64
        if cw > largeContextWindowThreshold {
            threshold = largeContextWindowBuffer
        } else {
            threshold = int64(float64(cw) * smallContextWindowRatio)
        }
        if (remaining <= threshold) && !a.disableAutoSummarize {
            shouldPruneOrSummarize = true  // ★ 只改这一行
            return true
        }
        return false
    },
    // loop detection 不变
},
```

#### 4.4.2 Stream 结束后的处理逻辑（line 582 改造）

```go
// agent.go:582-597 替换为：

if shouldPruneOrSummarize {
    a.activeRequests.Del(call.SessionID)

    // ★ 先尝试 prune
    pruneMsgs, pruneErr := a.getSessionMessages(ctx, currentSession)
    if pruneErr == nil {
        pruneResult, pruneErr := a.prune(ctx, pruneMsgs)
        if pruneErr == nil && pruneResult.FreedTokens >= PruneMinimum {
            // prune 成功：更新 session token 估算，重新排队继续对话
            currentSession.PromptTokens -= int64(pruneResult.FreedTokens)
            if currentSession.PromptTokens < 0 {
                currentSession.PromptTokens = 0
            }
            if _, saveErr := a.sessions.Save(ctx, currentSession); saveErr != nil {
                return nil, saveErr
            }

            // 重新排队继续对话（复用现有 messageQueue 机制）
            existing, ok := a.messageQueue.Get(call.SessionID)
            if !ok {
                existing = []SessionAgentCall{}
            }
            call.Prompt = fmt.Sprintf(
                "Context was pruned to free space. The initial request was: `%s`",
                call.Prompt,
            )
            existing = append(existing, call)
            a.messageQueue.Set(call.SessionID, existing)

            // 跳过 Summarize，直接进入队列处理
            goto afterSummarize
        }
    }

    // prune 不够或失败，走原有 Summarize 路径
    if summarizeErr := a.Summarize(genCtx, call.SessionID, call.ProviderOptions); summarizeErr != nil {
        return nil, summarizeErr
    }
    if len(currentAssistant.ToolCalls()) > 0 {
        existing, ok := a.messageQueue.Get(call.SessionID)
        if !ok {
            existing = []SessionAgentCall{}
        }
        call.Prompt = fmt.Sprintf(
            "The previous session was interrupted because it got too long, the initial user request was: `%s`",
            call.Prompt,
        )
        existing = append(existing, call)
        a.messageQueue.Set(call.SessionID, existing)
    }
}
afterSummarize:
```

### 4.5 prune 后下一轮 Run 的行为分析

下一轮 `Run()` 的调用链：

```
Run()
  → getSessionMessages() → 从数据库读取消息 → 包含裁剪后的 "[tool output pruned]"
  → preparePrompt(msgs) → msgs.ToAIMessage() → LLM 收到裁剪后的内容
  → Stream() → StopWhen → 检查 remaining
```

**关键问题**：prune 成功后更新了 `currentSession.PromptTokens` 的估算值，但下一次 `Run()` 开头会重新从数据库获取 session：

```go
// agent.go:223-228
currentSession, err := a.sessions.Get(ctx, call.SessionID)
```

如果 `sessions.Save()` 已持久化了更新后的 PromptTokens，下一轮拿到的就是更新后的值，StopWhen 不会立即再次触发。

### 4.6 session token 更新的准确性

`currentSession.PromptTokens` 是在 `OnStepFinish` 中由 API 返回的精确值更新的（`agent.go:418-427`）。prune 后做的 `- FreedTokens` 只是一个估算。

**可能的偏差**：
- 实际 token 节省可能 > 估算（因为我们只算了 Content，没算 JSON 序列化开销）
- 实际 token 节省也可能 < 估算（如果估算偏大）

**影响**：
- 如果低估：下一轮 StopWhen 可能更快触发，再次 prune 或走 Summarize——不会死循环，只是多一轮
- 如果高估：下一轮 API 调用可能仍然超限——provider 会返回错误，Stream 处理错误后走 Summarize

两种情况都有兜底，不会出问题。

---

## 五、关键实现细节

### 5.1 "轮次" 的定义差异

```
OpenCode:  msg.info.role === "user" → turns++
Crush:     msg.Role == message.User → turns++
```

两者语义一致：user 消息标志着新一轮对话。但注意 Crush 中 summary 消息的角色会被改为 user：

```go
// agent.go:815
msgs[0].Role = message.User  // 摘要消息被改为 user 角色
```

这意味着如果存在摘要消息，它会算作一轮。在 prune 的逆向扫描中，遇到 `IsSummaryMessage == true` 时 `break loop`，所以不会有问题。

### 5.2 Token 估算

```go
func estimateTokens(text string) int {
    return len(text) / 4
}
```

**为什么不需要精确估算**：PruneMinimum = 20K 是一个很大的缓冲。即使估算偏差 50%（实际 10K 但估成 20K），最坏情况是偶尔不执行本可以执行的 prune，不会导致功能错误。反过来如果低估（实际 30K 但估成 15K），只是少释放了一些 token，Summarize 兜底。

### 5.3 已裁剪标记防重复

```
OpenCode:  part.state.time.compacted 存在 → break loop
Crush:     tr.PrunedAt > 0 → break loop
```

这个检查确保：
1. 不重复裁剪同一工具输出
2. 遇到之前 prune 的边界时停止，不往更旧的消息扫描

### 5.4 遇到摘要消息停止

```
OpenCode:  msg.info.role === "assistant" && msg.info.summary → break loop
Crush:     msg.Role == message.Assistant && msg.IsSummaryMessage → break loop
```

摘要消息之前的消息已经被 Summarize 处理过（替换为摘要），不需要再扫描。

### 5.5 消息持久化与去重

OpenCode 使用 `yield* session.updatePart(part)` 更新单个 part。Crush 的消息是整体持久化的（`a.messages.Update(ctx, msg)`）。

一条 tool-role 消息可能包含多个 ToolResult。如果一条消息中有 2 个 ToolResult 都被裁剪，只需要 Update 一次。代码中用 `updatedMsgs := make(map[int]bool)` 去重。

同理，如果 clearableToolUses 的 ToolCall 在同一条 assistant 消息中，也只 Update 一次。

### 5.6 MCP 工具的处理

Crush 的 MCP 工具名称格式为 `<server>__<tool>`（如 `mcp__filesystem__read_file`）。这些工具不在 `prunableTools` 和 `clearableToolUses` map 中，所以**默认不会被裁剪**，这与 Claude Code 的策略一致（MCP 工具行为不可预测，不应裁剪）。

如果未来想支持 MCP 工具的裁剪，可以按前缀匹配：

```go
// 未来扩展：按前缀匹配 MCP 工具
func isPrunable(toolName string) bool {
    if prunableTools[toolName] {
        return true
    }
    // 可选：特定 MCP server 的工具可裁剪
    // return strings.HasPrefix(toolName, "mcp__safe_server__")
    return false
}
```

---

## 六、与 OpenCode 的差异清单

| 项目 | OpenCode | Crush | 原因 |
|------|----------|-------|------|
| **工具分类** | 不区分（除 skill） | 三分类（可裁/只清调用/不可裁） | 吸取 Claude Code 的教训，避免裁掉重要工具输出 |
| **裁剪内容** | 清空为空字符串 | 替换为 `[tool output pruned]` | 让 LLM 知道有内容被清空，避免困惑 |
| **裁剪后持久化** | `session.updatePart(part)` | `a.messages.Update(ctx, msg)` 去重 | 数据模型差异，Crush 是整体消息持久化 |
| **清空调用参数** | 不做 | 对 edit/multiedit/write 清空 `ToolCall.Input` | 参考 Claude Code，编辑参数开销大但确认信息有价值 |
| **ToolCall 回溯** | 不需要（调用和结果在同一个对象） | 预构建 `toolCallID → msgIndex` 索引 | Crush 调用和结果分离存储 |
| **MCP 工具** | 可被裁剪 | 默认不可裁 | MCP 工具行为不可预测 |
| **prune 执行位置** | 溢出检测后、compaction 前 | Stream 结束后、Summarize 前 | Stream 内部消息缓存不会刷新，必须在 Stream 外执行 |
| **配置开关** | `cfg.compaction?.prune === false` | 可通过环境变量 `CRUSH_DISABLE_PRUNE` | 与 Crush 的配置风格一致 |

---

## 七、完整文件清单

实现需要修改/新增的文件：

```
internal/message/content.go     → ToolResult 新增 PrunedAt 字段
internal/agent/prune.go         → ★ 新文件：prune 核心逻辑
internal/agent/agent.go         → StopWhen 变量名改造 + Stream 后 prune/SUMMARIZE 分流
```

### 7.1 prune.go 新文件结构

```go
package agent

import (
    "context"
    "fmt"
    "time"

    "github.com/anthropics/crush/internal/message"
)

// 常量
const (
    PruneProtectTurns  = 2
    PruneProtectTokens = 40_000
    PruneMinimum       = 20_000
)

// 工具分类
var prunableTools = map[string]bool{ ... }
var clearableToolUses = map[string]bool{ ... }

// 结果类型
type pruneResult struct { ... }

// 核心函数
func (a *sessionAgent) prune(ctx context.Context, msgs []message.Message) (pruneResult, error)

// 辅助函数
func estimateTokens(text string) int
```

### 7.2 agent.go 修改点

```
line 251: shouldSummarize → shouldPruneOrSummarize（变量名）
line 447: shouldSummarize = true → shouldPruneOrSummarize = true
line 582-597: 替换为 prune → Summarize 分流逻辑
```

### 7.3 content.go 修改点

```
line 107-117: ToolResult 结构体新增 PrunedAt int64 `json:"pruned_at,omitempty"`
```

---

## 八、测试要点

### 8.1 单元测试

```go
func TestPrune_ProtectRecentTurns(t *testing.T) {
    // 构造 5 轮消息，最近 2 轮有大量工具输出
    // 验证最近 2 轮的工具输出不被裁剪
}

func TestPrune_ProtectRecentTokens(t *testing.T) {
    // 构造消息，从第 3 轮开始累计 < 40K token 的工具输出
    // 验证这些工具输出不被裁剪
}

func TestPrune_MinimumThreshold(t *testing.T) {
    // 构造消息，可裁剪的 token 总量为 15K（< PruneMinimum=20K）
    // 验证不执行裁剪
}

func TestPrune_ToolClassification(t *testing.T) {
    // 构造消息，包含 bash（可裁）、edit（只清调用）、agent（不可裁）工具
    // 验证：
    // - bash 的 ToolResult.Content 被替换为 "[tool output pruned]"
    // - edit 的 ToolResult 保留内容，对应 ToolCall.Input 被清空为 "{}"
    // - agent 的输出完全不动
}

func TestPrune_SummaryBoundary(t *testing.T) {
    // 构造消息，中间有一个 IsSummaryMessage=true 的 assistant 消息
    // 验证扫描在摘要消息处停止
}

func TestPrune_AlreadyPruned(t *testing.T) {
    // 构造消息，某个 ToolResult 已有 PrunedAt > 0
    // 验证扫描在该位置停止
}

func TestPrune_ToolCallIndexLookup(t *testing.T) {
    // 构造多条 assistant 消息和 tool 消息
    // 验证 toolCallIndex 正确映射 ToolCallID → assistant 消息
    // 验证 clearableToolUses 的 ToolCall.Input 被正确清空
}

func TestPrune_MessageUpdateDedup(t *testing.T) {
    // 构造一条 tool 消息包含 2 个 ToolResult，都被裁剪
    // 验证该消息只被 Update 一次（去重）
}
```

### 8.2 集成测试

```go
func TestPrune_Integration(t *testing.T) {
    // 构造长对话，触发 StopWhen
    // 验证：先执行 prune，prune 释放足够空间后跳过 Summarize
    // 验证：下一轮 Run 使用裁剪后的消息
}

func TestPrune_FallbackToSummarize(t *testing.T) {
    // 构造短对话，StopWhen 触发但 prune 释放空间不够
    // 验证：prune 后继续执行 Summarize
}

func TestPrune_SessionTokenUpdate(t *testing.T) {
    // 构造触发 prune 的场景
    // 验证：prune 后 session.PromptTokens 被正确减少
    // 验证：下一轮 Run 不会立即再次触发 StopWhen
}
```

---

## 九、风险缓解

### 9.1 Prompt Cache 失效

**问题**：直接修改消息内容会破坏 Anthropic API 的 prompt cache。

**缓解**：PruneMinimum = 20K。只有裁剪量 >= 20K token 时才执行。这意味着 cache miss 的代价（重传前缀）被 20K+ token 的节省抵消。

**进一步优化**（可选，不在初版实现）：如果检测到 cache 仍在 TTL 内（60 分钟），可以跳过 prune，等 cache 过期后再执行。

### 9.2 孤立 ToolResult

**问题**：裁剪 ToolResult 时，如果对应的 ToolCall 被清空（clearableToolUses），需要确保 API 不会因为空参数报错。

**缓解**：`ToolCall.Input = "{}"` 是合法的 JSON，API 会接受。LLM 看到空参数和标记过的 ToolResult 的组合，能理解这个工具之前被调用过但输出被清了。

### 9.3 LLM 重新调用被裁剪的工具

**问题**：LLM 看到 `[tool output pruned]` 后可能重新调用同一工具获取输出。

**缓解**：
1. 这实际上是合理行为——如果 LLM 判断需要那个输出，重新获取是对的
2. 可以在 system prompt 中加入提示："旧的工具输出可能被裁剪以节省上下文空间，如果需要可以重新调用工具"

### 9.4 并发安全

**问题**：prune 修改消息时，其他 goroutine 可能正在读取同一消息。

**缓解**：prune 在 `Stream()` 返回后、主流程中执行。此时 `activeRequests` 已清理（`a.activeRequests.Del(call.SessionID)`），不会有并发的 API 调用在操作同一 session。

### 9.5 prune 后立即再次触发 StopWhen

**问题**：如果 token 估算不准确，下一轮 Run 可能立即再次触发 StopWhen。

**缓解**：
1. prune 减少了 `session.PromptTokens`，下一轮 Run 拿到的是更新后的值
2. 即使再次触发，会再次 prune（释放更多空间）或走 Summarize（最终兜底）
3. 不会死循环：每轮 prune 至少释放 20K token，消息总量有限，几轮后必然不需要再 prune

---

## 十、Claude Code 可参考的优化（按优先级排序）

OpenCode 只有 prune 一层防护。Claude Code 在 prune 之前还有两层预截断机制，形成了三层防线：

```
Claude Code 三层防线：

第 1 层：单个工具输出超限 → 立即截断或持久化到磁盘（源头控制）
         时机：工具执行完毕后、写入 ToolResult 之前
         代码：toolResultStorage.ts → persistToolResult() + maybePersistLargeToolResult()

第 2 层：单轮消息内工具输出总量超限 → 最大优先替换为磁盘预览
         时机：每轮 API 调用前（preparePrompt 阶段）
         代码：toolResultStorage.ts → enforceToolResultBudget()

第 3 层：旧工具输出 prune → 释放空间
         时机：溢出触发 / 时间触发 / API 端自动
         代码：microCompact.ts
```

OpenCode 和当前 Crush 设计都只有第 3 层。以下是 Claude Code 各层优化的详细分析和适配建议。

---

### 10.1 优化 1：工具输出预截断（最高优先级）

#### 10.1.1 Claude Code 的做法

Claude Code 的工具输出处理流程：

```
工具执行完毕 → 输出内容
                ↓
         maybePersistLargeToolResult()
                ↓ 输出 > 阈值（默认 50K 字符）
         persistToolResult() → 写入磁盘文件
                ↓
         ToolResult.Content 替换为预览（前 N 行 + 文件路径提示）
                ↓
         写入消息
```

关键常量（`toolLimits.ts`）：

```typescript
DEFAULT_MAX_RESULT_SIZE_CHARS = 50_000        // 单个工具输出最大字符数
MAX_TOOL_RESULT_TOKENS = 100_000              // 单个工具输出最大 token 数（约 400KB）
BYTES_PER_TOKEN = 4                           // 保守的 byte/token 比率
MAX_TOOL_RESULTS_PER_MESSAGE_CHARS = 200_000  // 单轮所有工具输出总量上限
```

持久化到磁盘的核心逻辑（`toolResultStorage.ts:137-184`）：

```typescript
async function persistToolResult(toolName: string, content: string, ...) {
    const filepath = getResultFilePath(toolCallID)  // 生成唯一文件路径
    try {
        // flag: 'wx' = write-once，防止重复写入
        await writeFile(filepath, contentStr, { encoding: 'utf-8', flag: 'wx' })
    } catch (error) {
        if (getErrnoCode(error) !== 'EEXIST') {
            return { error: getFileSystemErrorMessage(toError(error)) }
        }
        // EEXIST: 之前已持久化过，跳过写入
    }
    // 生成预览（前 N 行 + 文件大小信息）
    const preview = generatePreview(content, filepath)
    return { replacement: preview, filepath }
}
```

预览生成（`toolResultStorage.ts:339-356`）：

```typescript
function generatePreview(content: string, filepath: string): string {
    const lines = content.split('\n')
    const previewLines = lines.slice(0, PREVIEW_LINE_COUNT)  // 默认保留前 N 行
    const truncated = lines.length > PREVIEW_LINE_COUNT
    let preview = previewLines.join('\n')
    if (truncated) {
        preview += `\n\n[Output truncated. ${lines.length} total lines. ` +
                   `Full output saved to: ${filepath}]`
    }
    return preview
}
```

**关键设计决策**：
- **写入一次**：使用 `flag: 'wx'` 确保同一工具调用只写入一次磁盘，后续轮次直接复用
- **保留预览**：不是完全清空，而是保留前几行让 LLM 知道输出大概是什么
- **文件路径提示**：告诉 LLM 完整内容在哪里，需要时可以用 `view` 工具重新读取

#### 10.1.2 Crush 现状分析

Crush 的工具输出流程（`agent.go:1074-1105`）：

```go
func (a *sessionAgent) convertToToolResult(result fantasy.ToolResultContent) message.ToolResult {
    baseResult := message.ToolResult{
        ToolCallID: result.ToolCallID,
        Name:       result.ToolName,
    }
    switch result.Result.GetType() {
    case fantasy.ToolResultContentTypeText:
        // ★ 直接赋值，没有任何大小限制
        baseResult.Content = r.Text
    case fantasy.ToolResultContentTypeMedia:
        baseResult.Content = content
        baseResult.Data = r.Data     // ★ 二进制数据也直接存储
        baseResult.MIMEType = r.MediaType
    }
    return baseResult
}
```

**问题**：`r.Text` 没有任何大小限制。一次 `bash -c "cat huge.log"` 可能产生数 MB 的输出，全部进入上下文。一次 `view` 读取大文件也是如此。

#### 10.1.3 对 Crush 的适配建议

**集成位置**：`convertToToolResult()` 函数（`agent.go:1074`），在 `baseResult.Content = r.Text` 之后加截断逻辑。

**方案 A：简单截断（初版推荐）**

```go
// internal/agent/prune.go 或 agent.go

const MaxResultSizeChars = 50_000  // 与 Claude Code 一致

func truncateToolOutput(toolName string, content string) string {
    if len(content) <= MaxResultSizeChars {
        return content
    }

    // 按行截断，保留前 N 行
    lines := strings.Split(content, "\n")
    maxLines := 200  // 预览行数上限
    if len(lines) <= maxLines {
        // 行数不多但总字符数超限（说明有超长行），直接按字符截断
        truncated := content[:MaxResultSizeChars]
        remaining := len(content) - MaxResultSizeChars
        return truncated + fmt.Sprintf(
            "\n\n[output truncated, %d characters (%.1f KB) remaining]",
            remaining, float64(remaining)/1024,
        )
    }

    preview := strings.Join(lines[:maxLines], "\n")
    return preview + fmt.Sprintf(
        "\n\n[output truncated, showed %d of %d lines, %d characters remaining]",
        maxLines, len(lines), len(content)-len(preview),
    )
}
```

集成到 `convertToToolResult`：

```go
case fantasy.ToolResultContentTypeText:
    if r, ok := fantasy.AsToolResultOutputType[fantasy.ToolResultOutputContentText](result.Result); ok {
        baseResult.Content = truncateToolOutput(result.ToolName, r.Text)
    }
```

**方案 B：磁盘持久化 + 预览（进阶版）**

```go
// internal/agent/tool_result_storage.go — 新文件

const (
    MaxResultSizeChars  = 50_000
    PreviewLineCount    = 200
    ToolResultDirPrefix = ".crush/tool-results/"
)

// PersistedResult 记录持久化结果
type PersistedResult struct {
    Preview  string // 替换后的预览内容
    FilePath string // 磁盘文件路径
    Size     int    // 原始大小
}

// maybePersistLargeToolResult 对超限输出进行持久化
func maybePersistLargeToolResult(toolCallID, toolName, content string) (string, *PersistedResult) {
    if len(content) <= MaxResultSizeChars {
        return content, nil  // 不超限，原样返回
    }

    // 生成文件路径
    dir := filepath.Join(os.TempDir(), "crush-tool-results")
    os.MkdirAll(dir, 0o755)
    filepath := filepath.Join(dir, fmt.Sprintf("%s.txt", toolCallID))

    // 写入磁盘（write-once）
    err := os.WriteFile(filepath, []byte(content), 0o644)
    if err != nil {
        // 持久化失败，降级为简单截断
        return truncateToolOutput(toolName, content), nil
    }

    // 生成预览
    lines := strings.Split(content, "\n")
    previewLines := lines
    if len(lines) > PreviewLineCount {
        previewLines = lines[:PreviewLineCount]
    }
    preview := strings.Join(previewLines, "\n")
    preview += fmt.Sprintf(
        "\n\n[output truncated: %d of %d lines shown. Full output (%.1f KB) saved to: %s]",
        len(previewLines), len(lines), float64(len(content))/1024, filepath,
    )

    return preview, &PersistedResult{
        Preview:  preview,
        FilePath: filepath,
        Size:     len(content),
    }
}
```

集成到 `convertToToolResult`：

```go
case fantasy.ToolResultContentTypeText:
    if r, ok := fantasy.AsToolResultOutputType[fantasy.ToolResultOutputContentText](result.Result); ok {
        preview, _ := maybePersistLargeToolResult(result.ToolCallID, result.ToolName, r.Text)
        baseResult.Content = preview
    }
```

**两种方案的取舍**：

| 维度 | 方案 A（简单截断） | 方案 B（磁盘持久化） |
|------|-------------------|---------------------|
| 实现复杂度 | 极低（一个函数） | 中等（新文件 + 磁盘管理） |
| 信息损失 | 截断后无法恢复 | LLM 可用 view 工具读取完整内容 |
| 磁盘管理 | 无需管理 | 需要清理临时文件 |
| 效果 | 从源头减少 ~50-90% 数据量 | 同方案 A，但 LLM 可以按需恢复 |

**建议**：初版用方案 A，验证效果后再升级到方案 B。

#### 10.1.4 对 prune 的影响

加了预截断后，进入上下文的工具输出被限制在 50K 字符以内。prune 扫描时遇到的都是已被截断的输出，单条不会超过 ~12K token。这意味着：

- 单条工具输出的 prune 收益变小（最多节省 ~12K token）
- prune 仍然有价值：多条旧工具输出累加起来仍可能 > 20K
- 但预截断减少了对 prune 的依赖——很多场景下预截断就够用了，根本不需要走到 prune

#### 10.1.5 工具级别的差异化阈值

Claude Code 为不同工具设置了不同的持久化阈值。Crush 可以参考：

```go
// 工具级别的输出上限（字符数）
var toolResultLimits = map[string]int{
    "bash":   100_000, // bash 输出可能很长（编译日志等），给更高上限
    "view":   50_000,  // 文件内容
    "grep":   50_000,  // 搜索结果
    "glob":   10_000,  // 文件列表通常不大
    "ls":     10_000,  // 目录列表通常不大
    "fetch":  50_000,  // Web 内容
    "sourcegraph": 50_000, // 代码搜索
}

func getToolResultLimit(toolName string) int {
    if limit, ok := toolResultLimits[toolName]; ok {
        return limit
    }
    return MaxResultSizeChars  // 默认 50K
}
```

---

### 10.2 优化 2：单轮预算限制（中优先级）

#### 10.2.1 Claude Code 的做法

Claude Code 在每轮 API 调用前执行 `enforceToolResultBudget()`，检查当前轮次（assistant 消息 + 对应的 tool 消息）内所有工具输出的总量：

```typescript
// toolResultStorage.ts: enforceToolResultBudget()
const perMessageLimit = getPerMessageBudgetLimit()  // 默认 200K 字符

// 收集候选（当前轮次内的所有 tool result）
const candidates = collectCandidatesByMessage(messages)

// 按大小排序，最大的优先替换
candidates.sort((a, b) => b.charCount - a.charCount)

let totalChars = candidates.reduce((sum, c) => sum + c.charCount, 0)
for (const candidate of candidates) {
    if (totalChars <= perMessageLimit) break
    // 持久化最大的 tool result 到磁盘
    const replacement = await persistToolResult(candidate)
    candidate.replace(replacement)
    totalChars -= candidate.charCount - replacement.length
}
```

**关键设计**：
- **最大优先替换**：优先替换输出最大的工具结果，用最少操作释放最多空间
- **状态冻结**：`ContentReplacementState` 确保一个工具结果的命运一旦决定（持久化或保留），后续轮次不会改变——这保持了 prompt cache 的稳定性
- **按 API 消息边界分组**：不是按本地消息分组，而是按发送给 API 的消息分组（一条 assistant 消息 + 其后的 tool 消息 = 一个 API round）

#### 10.2.2 Crush 的适用场景

当 agent 在一轮内并行调用多个工具时：

```
一轮 API 调用：
  assistant: [ToolCall bash, ToolCall grep, ToolCall view]
  tool:      [ToolResult bash 80K, ToolResult grep 60K, ToolResult view 100K]
  总计：240K 字符
```

如果没有单轮预算，240K 全部进入上下文。有了预算限制（200K），最大的 view (100K) 被持久化到磁盘，ToolResult 替换为 ~2K 的预览，总量降到 ~142K。

#### 10.2.3 对 Crush 的适配建议

**实现位置**：`preparePrompt()` 函数（`agent.go:761-797`），在消息转换为 AI 格式之前。

```go
// internal/agent/prune.go — 新增

const MaxToolResultsPerMessageChars = 200_000  // 与 Claude Code 一致

type budgetCandidate struct {
    msgIndex  int    // tool-role 消息索引
    partIndex int    // ToolResult 在 Parts 中的索引
    toolName  string
    content   string
    charCount int
}

// enforceToolResultBudget 对当前轮次内的工具输出执行预算限制
func (a *sessionAgent) enforceToolResultBudget(
    ctx context.Context,
    msgs []message.Message,
) error {
    // 找到最后一个 assistant 消息（当前轮次）
    lastAssistantIdx := -1
    for i := len(msgs) - 1; i >= 0; i-- {
        if msgs[i].Role == message.Assistant {
            lastAssistantIdx = i
            break
        }
    }
    if lastAssistantIdx < 0 {
        return nil
    }

    // 收集当前轮次的 tool result
    var candidates []budgetCandidate
    totalChars := 0
    for i := lastAssistantIdx + 1; i < len(msgs); i++ {
        if msgs[i].Role != message.Tool {
            continue
        }
        for j, part := range msgs[i].Parts {
            tr, ok := part.(message.ToolResult)
            if !ok || tr.IsError || tr.PrunedAt > 0 {
                continue
            }
            chars := len(tr.Content)
            totalChars += chars
            candidates = append(candidates, budgetCandidate{
                msgIndex:  i,
                partIndex: j,
                toolName:  tr.Name,
                content:   tr.Content,
                charCount: chars,
            })
        }
    }

    if totalChars <= MaxToolResultsPerMessageChars {
        return nil  // 在预算内
    }

    // 按大小降序排序，最大的优先替换
    sort.Slice(candidates, func(i, j int) bool {
        return candidates[i].charCount > candidates[j].charCount
    })

    // 替换最大的结果直到在预算内
    updatedMsgs := make(map[int]bool)
    for _, c := range candidates {
        if totalChars <= MaxToolResultsPerMessageChars {
            break
        }

        tr := msgs[c.msgIndex].Parts[c.partIndex].(message.ToolResult)

        // 截断或持久化
        preview := truncateToolOutput(c.toolName, c.content)
        savings := c.charCount - len(preview)

        tr.Content = preview
        msgs[c.msgIndex].Parts[c.partIndex] = tr
        totalChars -= savings
        updatedMsgs[c.msgIndex] = true
    }

    // 持久化修改
    for msgIdx := range updatedMsgs {
        if err := a.messages.Update(ctx, msgs[msgIdx]); err != nil {
            return err
        }
    }
    return nil
}
```

**集成到 Run() 中**：

```go
// agent.go Run() 中，preparePrompt 之前
msgs, err := a.getSessionMessages(ctx, currentSession)
if err != nil {
    return nil, err
}
a.enforceToolResultBudget(ctx, msgs)  // ★ 新增：单轮预算限制
history, files := a.preparePrompt(msgs, call.Attachments...)
```

#### 10.2.4 与 prune 的关系

```
优化 1（预截断）→ 每个工具输出 ≤ 50K 字符
优化 2（单轮预算）→ 每轮所有工具输出 ≤ 200K 字符
优化 3（prune）→ 清理旧的工具输出释放空间

三层协同：
  优化 1 限制单条 → 减少进入上下文的数据量
  优化 2 限制单轮 → 防止单轮暴增
  优化 3 清理旧数据 → 释放已积累的空间
```

**优先级判断**：依赖优化 1（预截断或持久化机制）。如果没有预截断，预算超限时的"替换"操作就只是简单截断，效果打折。建议在优化 1 之后实现。

---

### 10.3 优化 3：时间触发 prune（中优先级）

#### 10.3.1 Claude Code 的做法

Claude Code 在每轮 API 调用前检查时间间隔：

```typescript
// microCompact.ts: evaluateTimeBasedTrigger()
export function evaluateTimeBasedTrigger(messages: Message[], querySource: QuerySource) {
    const config = getTimeBasedMCConfig()
    if (!config.enabled) return null

    // 找到最后一个 assistant 消息的时间
    const lastAssistant = messages.findLast(m => m.type === 'assistant')
    if (!lastAssistant) return null

    const gapMinutes = (Date.now() - new Date(lastAssistant.timestamp).getTime()) / 60_000
    if (gapMinutes < config.gapThresholdMinutes) {  // 默认 60 分钟
        return null
    }

    return { gapMinutes, config }
}
```

**为什么是 60 分钟**：Anthropic API 的 prompt cache TTL 是 60 分钟。超过 60 分钟后缓存**一定**已经过期，下次 API 调用无论怎样都要重写完整前缀。此时 prune 是零额外代价的——反正 cache 已经没了，裁掉旧输出可以直接减少重写量。

**时间触发时的 prune 行为**：

```typescript
// microCompact.ts: maybeTimeBasedMicrocompact()
const compactableIds = collectCompactableToolIds(messages)
const keepRecent = Math.max(1, config.keepRecent)  // 默认 5
const keepSet = new Set(compactableIds.slice(-keepRecent))
const clearSet = new Set(compactableIds.filter(id => !keepSet.has(id)))

// 直接修改消息内容（cache 已过期，破坏无所谓）
for (const message of messages) {
    message.content = message.content.map(block => {
        if (block.type === 'tool_result' && clearSet.has(block.tool_use_id)) {
            return { ...block, content: '[Old tool result content cleared]' }
        }
        return block
    })
}
```

**与溢出触发的区别**：

| 维度 | 溢出触发（OpenCode/Crush 设计） | 时间触发（Claude Code） |
|------|-------------------------------|----------------------|
| 触发条件 | remaining <= threshold | gapMinutes >= 60 |
| PruneMinimum | 需要（破坏 cache 有代价） | **不需要**（cache 已过期，零代价） |
| 保护策略 | 2 轮 + 40K token | 保留最近 5 个可裁工具 |
| 执行时机 | 被动（快溢出了才裁） | 主动（cache 过期了就裁） |
| 效果 | 救急 | 预防 |

#### 10.3.2 对 Crush 的适用性分析

**Anthropic provider**：完全适用。Anthropic prompt cache TTL = 60min，时间触发的逻辑可以直接搬用。cache 过期后的 prune 不需要 PruneMinimum 限制，任何裁剪量都是净收益。

**OpenAI provider**：OpenAI 也有 prompt caching（automatic caching），但 TTL 较短（5-10 分钟），且缓存行为不如 Anthropic 透明。理论上可以用更短的间隔（如 10 分钟），但收益不明确。

**其他 provider**（Bedrock/Vertex/Gemini 等）：缓存行为各异，时间触发的阈值难以统一。

#### 10.3.3 对 Crush 的适配建议

**初版不实现**。原因：
1. 需要 provider 感知逻辑（不同 provider cache TTL 不同）
2. 需要记录最后 assistant 消息时间（Crush 的 Message 有 CreatedAt，可以获取）
3. 先验证溢出触发的 prune 稳定性，再加时间触发

**第二版实现参考**：

```go
// internal/agent/prune.go — 时间触发相关

// 各 provider 的 cache TTL（分钟），0 表示无 cache
var providerCacheTTL = map[string]int{
    "anthropic": 60,
    "openai":    10,  // OpenAI 自动缓存，TTL 较短
    // 其他 provider 默认 0（无 cache，不启用时间触发）
}

// maybeTimeBasedPrune 检查是否满足时间触发条件
func (a *sessionAgent) maybeTimeBasedPrune(
    ctx context.Context,
    session session.Session,
    provider string,
) (bool, error) {
    ttl, ok := providerCacheTTL[provider]
    if !ok || ttl == 0 {
        return false, nil  // 该 provider 无 cache，不启用
    }

    msgs, err := a.getSessionMessages(ctx, session)
    if err != nil {
        return false, err
    }

    // 找最后一个 assistant 消息的时间
    var lastAssistantTime int64
    for i := len(msgs) - 1; i >= 0; i-- {
        if msgs[i].Role == message.Assistant {
            lastAssistantTime = msgs[i].CreatedAt
            break
        }
    }
    if lastAssistantTime == 0 {
        return false, nil
    }

    gapMinutes := (time.Now().UnixMilli() - lastAssistantTime) / (60 * 1000)
    if gapMinutes < int64(ttl) {
        return false, nil  // cache 仍有效，不触发
    }

    // Cache 已过期，执行 prune（不需要 PruneMinimum 限制）
    result, err := a.pruneWithMinimum(ctx, msgs, 0)  // minimum = 0：任何裁剪量都值得
    if err != nil {
        return false, err
    }

    return result.PrunedCount > 0, nil
}

// pruneWithMinimum 与 prune 相同，但允许自定义 PruneMinimum
func (a *sessionAgent) pruneWithMinimum(
    ctx context.Context,
    msgs []message.Message,
    minimum int,
) (pruneResult, error) {
    // ... 与 prune 相同的扫描逻辑
    // 唯一区别：用传入的 minimum 替代 PruneMinimum 常量
}
```

**集成位置**：`Run()` 函数开头，`preparePrompt` 之前：

```go
// agent.go Run() 中
currentSession, _ := a.sessions.Get(ctx, call.SessionID)
msgs, _ := a.getSessionMessages(ctx, currentSession)

// ★ 时间触发检查（在 preparePrompt 之前）
if pruned, _ := a.maybeTimeBasedPrune(ctx, currentSession, largeModel.ModelCfg.Provider); pruned {
    // 重新获取裁剪后的消息
    msgs, _ = a.getSessionMessages(ctx, currentSession)
}

history, files := a.preparePrompt(msgs, call.Attachments...)
```

---

### 10.4 优化 4：cache 感知的 PruneMinimum 阈值（低优先级，可初版带）

#### 10.4.1 问题背景

PruneMinimum = 20K 存在的原因是：直接修改消息会破坏 prompt cache，cache miss 的代价需要被裁剪收益抵消。

但不同 provider 的 cache 行为不同：
- Anthropic：有显式 cache，miss 代价高，20K 阈值合理
- OpenAI：有自动 cache，但 miss 后会自动重建，代价较低
- 其他 provider：可能没有 cache，少量裁剪也是正收益

#### 10.4.2 适配建议

```go
// internal/agent/prune.go — 替换固定 PruneMinimum

// pruneThreshold 根据 provider 返回最小裁剪量阈值
func pruneThreshold(provider string) int {
    switch provider {
    case "anthropic":
        return 20_000  // Anthropic cache miss 代价高，保持高阈值
    case "openai":
        return 10_000  // OpenAI cache miss 代价较低
    default:
        return 5_000   // 其他 provider 可能无 cache，低阈值即可
    }
}
```

集成到 prune 函数中：

```go
// 替代原来的 if pruned < PruneMinimum
minimum := pruneThreshold(a.largeModel.Get().ModelCfg.Provider)
if pruned < minimum {
    return result, nil
}
```

**复杂度极低**（一个 switch + 改一行判断），可以在初版就带上。

---

### 10.5 优化总结与实现路线

#### 10.5.1 三层防线协同工作流

```
工具执行完毕
  │
  ├─ 优化 1：预截断（第 1 层）
  │   输出 > 50K → 截断/持久化
  │   输出 ≤ 50K → 原样保留
  │
  ▼
每轮 API 调用前（preparePrompt 阶段）
  │
  ├─ 优化 3：时间触发 prune（第 2 层，第二版）
  │   cache 过期？→ 清理旧输出（零成本）
  │   cache 有效？→ 不动
  │
  ├─ 优化 2：单轮预算（第 2 层，第二版）
  │   当轮总量 > 200K → 最大优先替换
  │   当轮总量 ≤ 200K → 原样保留
  │
  ▼
Stream 内部循环
  │
  ├─ 优化 4：cache 感知阈值（贯穿所有层）
  │   根据 provider 调整 PruneMinimum
  │
  ├─ prune（第 3 层，改动 2+3）
  │   溢出触发 → 逆向扫描 → 裁剪旧输出
  │
  └─ Summarize（兜底）
      prune 不够 → 全量摘要
```

#### 10.5.2 实现路线图

| 版本 | 内容 | 改动文件 | 预计工作量 |
|------|------|---------|-----------|
| **v1 初版** | prune 核心逻辑（改动 2+3）+ 预截断（优化 1 方案 A）+ cache 感知阈值（优化 4） | `prune.go` 新文件, `agent.go` 改, `content.go` 改 | 2-3 天 |
| **v2 进阶** | 磁盘持久化（优化 1 方案 B）+ 单轮预算（优化 2） | `tool_result_storage.go` 新文件, `agent.go` 改 | 1-2 天 |
| **v3 高级** | 时间触发 prune（优化 3） | `prune.go` 改, `agent.go` 改 | 0.5-1 天 |

#### 10.5.3 预期效果对比

| 场景 | 无优化（当前） | v1（初版） | v2（进阶） |
|------|-------------|-----------|-----------|
| `cat large.log`（500K 输出） | 500K 全部进入上下文 | 截断为 ~50K 预览 | 持久化到磁盘 + 2K 预览 |
| 一轮 3 个工具各 80K | 240K 全部进入上下文 | 各截断为 ~50K，共 150K | 最大优先持久化，共 ~102K |
| 长对话 20 轮后溢出 | 直接 Summarize | 先 prune 释放空间，可能不需要 Summarize | 同 v1 + 单轮更省空间 |
| 长对话暂停 1 小时后继续 | 旧输出占大量空间 | 下一轮自动裁剪旧输出 | 同 v1 + cache 已过期零成本裁剪 |

#### 10.5.4 各优化的投入产出比

| 优化 | 投入 | 产出 | 比值 |
|------|------|------|------|
| **1A. 简单预截断** | 20 行代码 | 从源头减少 50-90% 工具输出 | 极高 |
| **1B. 磁盘持久化** | 新文件 ~150 行 | 信息可恢复，LLM 可按需读取完整内容 | 高 |
| **2. 单轮预算** | ~80 行 | 防止单轮暴增 | 中高 |
| **3. 时间触发** | ~60 行 | cache 过期后零成本裁剪（仅 Anthropic） | 中 |
| **4. cache 感知阈值** | 15 行 | 避免其他 provider 的过度保守 | 高（性价比最高） |
