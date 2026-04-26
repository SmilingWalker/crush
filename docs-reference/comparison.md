# Tool Prune 对比分析

## 一、谁有 Tool Prune

| Agent | 有 Tool Prune | 实际做法 |
|-------|:---:|---------|
| **Claude Code** | ✓ | 三种路径：时间触发 + 缓存编辑 + API 端管理，精细区分工具类型 |
| **OpenCode** | ✓ | 逆向扫描 + 位置保护，不区分工具类型 |
| **KiloCode** | ✓ | 继承 OpenCode，无创新 |
| **Cline** | ✗ | 做的是 Message Prune（删消息对），不是 Tool Prune |
| **Crush** | ✗ | 没有 prune，直接跳到摘要 |

---

## 二、核心差异对比

### 2.1 裁剪粒度

```
Claude Code：  精细区分三类工具（可裁结果/只裁调用/不可裁）
OpenCode：     不区分（除 skill 全部可裁）
KiloCode：     同 OpenCode
Cline：        不裁工具输出，直接删消息对
Crush：        不裁
```

### 2.2 触发时机

| Agent | 何时触发 | 为什么 |
|-------|---------|--------|
| **Claude Code** | 时间触发（>60min）+ 工具数量阈值 + API 端自动 | 时间触发利用 cache 过期主动清理，阈值是兜底 |
| **OpenCode** | 上下文溢出时被动触发 | 只在危险时才清理 |
| **KiloCode** | 同 OpenCode | 同 OpenCode |
| **Cline** | token 接近阈值时 | 但做的是删消息对不是 prune |
| **Crush** | 无 | 无 prune |

### 2.3 Cache 感知

| Agent | 是否保护 Prompt Cache | 怎么做 |
|-------|:---:|--------|
| **Claude Code** | ✓ | 缓存编辑 Prune 通过 API 层删除，不修改本地消息 |
| **OpenCode** | ✗ | 直接修改消息内容，每次 prune 都 cache miss |
| **KiloCode** | ✗ | 同 OpenCode |
| **Cline** | ✗ | N/A |
| **Crush** | ✗ | N/A |

### 2.4 保护策略

| Agent | 保护什么 |
|-------|---------|
| **Claude Code** | 最近 N 个可裁剪工具（默认 5 个） |
| **OpenCode** | 最近 2 轮 + 最近 40K token + skill 工具 |
| **KiloCode** | 同 OpenCode |
| **Cline** | 无保护（删消息对） |
| **Crush** | 无保护（直接全量摘要） |

### 2.5 裁剪后内容

| Agent | 工具输出变成什么 |
|-------|----------------|
| **Claude Code（时间触发）** | `'[Old tool result content cleared]'` |
| **Claude Code（缓存编辑）** | 本地不变，API 层删除 |
| **OpenCode** | 清空 output 字段 + 标记时间戳 |
| **KiloCode** | 同 OpenCode |
| **Cline** | 整个消息对删除 |
| **Crush** | 整个对话替换为摘要 |

---

## 三、工具分类对比

### Claude Code 的工具分类（最精细）

| 类别 | 工具 | 裁剪策略 | 为什么 |
|------|------|---------|--------|
| **可裁结果** | Read, Bash, Grep, Glob, WebSearch, WebFetch | 客户端+API 都可清 tool_use + tool_result | 输出大、一次性消费 |
| **只裁调用** | Edit, Write, NotebookEdit | API 只清 tool_use，保留 tool_result | 结果包含编辑确认，有参考价值 |
| **不可裁** | Agent, Task, Skill, MCP, ToolSearch, Memory, Cron | 不裁剪 | 影响执行流或不可预测 |

### OpenCode 的工具分类（最简单）

| 类别 | 工具 | 裁剪策略 | 为什么 |
|------|------|---------|--------|
| **受保护** | skill | 永不裁剪 | 未说明原因 |
| **可裁剪** | 其他所有工具 | 超出保护范围就裁 | 不区分类型 |

---

## 四、设计哲学对比

### Claude Code：Cache-Aware Multi-Path

```
Cache 过期了？→ 时间触发粗暴清（反正 cache 没了）
Cache 还有效？→ 缓存编辑精细删（不破坏 cache）
还不够？      → API 端自动管理（服务端处理）
```

核心思想：**根据 cache 状态选择策略**。每种路径都是在特定条件下的最优解。

### OpenCode：Position-Based Protection

```
溢出了？→ 从最新到最旧扫描
         → 最近 2 轮保护
         → 最近 40K token 保护
         → 超出部分全部清空
         → 少于 20K 不清（不值得 cache miss）
```

核心思想：**位置决定保护级别**。越新的越重要，越旧的越可裁。

### Cline：Message-Level Aggressive

```
快满了？→ 直接删消息对
         → half/quarter/lastTwo/none
```

核心思想：**简单粗暴**。不区分工具输出和对话内容，一刀切。

### Crush：Binary Switch

```
满了？→ 全部替换为 LLM 摘要
没满？→ 全部保留
```

核心思想：**非此即彼**。没有中间状态。

---

## 五、排名

| 维度 | 排名 |
|------|------|
| **完善度** | Claude Code >> OpenCode = KiloCode > Cline > Crush |
| **Cache 效率** | Claude Code >> OpenCode = KiloCode > Cline > Crush |
| **工具分类精细度** | Claude Code >> OpenCode = KiloCode > Cline = Crush |
| **信息保留** | Claude Code > OpenCode = KiloCode > Cline > Crush |
| **实现简洁度** | Crush > Cline > KiloCode = OpenCode > Claude Code |

---

## 六、关键洞察

### 1. Cache 感知是核心差异

Claude Code 的缓存编辑 Prune 是唯一不破坏 prompt cache 的方案。其他 agent 每次 prune 都导致 cache miss，实际 token 节省可能为负。

### 2. 工具分类的必要性

Read/Bash/Grep 的输出是"一次性消费"（读完就过了），Agent/Task 的输出是"持续性消费"（后续决策依赖它）。不区分会导致裁掉不该裁的。

### 3. 时间触发的价值

OpenCode 只在溢出时触发 prune——但此时可能已经浪费了大量 token 在传输旧工具输出上。Claude Code 在 cache 过期前主动清理，是在问题发生前预防而非发生后补救。

### 4. 最小裁剪量的权衡

OpenCode 的 20K 最小裁剪量是对 cache miss 的妥协——少量裁剪不值 cache miss 的代价。但 Claude Code 的缓存编辑没有这个代价，所以不需要最小量限制。**这说明了 cache 感知如何影响 prune 策略的每一个设计决策。**
