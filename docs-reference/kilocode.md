# KiloCode — Tool Prune 分析

## 结论

**KiloCode 完全继承 OpenCode 的 Tool Prune 机制，没有创新。** 仅增加了 `KILO_DISABLE_PRUNE` flag（但未被引用）。

## 与 OpenCode 的关系

KiloCode 是 OpenCode 的 fork，`packages/opencode/src/session/compaction.ts` 中的 prune 逻辑与 OpenCode 完全相同：

- `PRUNE_MINIMUM = 20_000`
- `PRUNE_PROTECT = 40_000`
- `PRUNE_PROTECTED_TOOLS = ["skill"]`
- 逆向扫描 + 最近 2 轮保护 + 40K token 保护带
- 最小 20K token 裁剪量

## 唯一差异

```typescript
// flag.ts
export const KILO_DISABLE_PRUNE = truthy("KILO_DISABLE_PRUNE")
```

这个 flag 存在于代码中但**未被任何地方引用**。它暗示：
- 团队曾考虑过允许用户禁用 prune
- 可能在某些场景下 prune 导致了问题（如裁剪掉了不该裁的工具输出）
- 但最终没有实现禁用功能

## 分析

KiloCode 在 Tool Prune 方面与 OpenCode 完全一致，具体分析见 [opencode.md](./opencode.md)。
