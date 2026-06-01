# 10 M5 Advanced：Direct Write / Worktree / A2A Gateway

M5 是高级阶段，不应阻塞 M1-M4。

## 1. Direct write

只有在 patch artifact 模式稳定后，才允许 teammate direct write。

要求：

- file lease。
- path canonicalization。
- base hash validation。
- apply-time validation。
- post-write hash record。
- shell write 必须声明 write set，否则只能 worktree。

## 2. File lease

表：

```text
team_file_leases
  id
  team_id
  run_id
  member_id
  path
  path_kind
  mode
  base_hash
  lease_token
  lease_until
  released_at
```

规则：

- file lease 和 parent directory lease 互斥。
- directory lease 和 children 互斥。
- lease TTL。
- run finish/cancel 释放 lease。

## 3. Worktree runner

适用：

- 大规模修改。
- shell-heavy task。
- 需要完整 test run。
- 多 teammate 并行写。

成本：

- 依赖安装。
- LSP/MCP 配置。
- branch merge。
- Windows 文件锁。
- 磁盘占用。

## 4. A2A gateway

A2A 不作为内部协议。M5 可做 gateway：

```text
external A2A task -> team_task
team_message -> A2A message
team_artifact -> A2A artifact
teammate capability -> agent card
```

要求：

- gateway adapter 不绕过本地 permission。
- external agent 也必须映射 ActorContext。
- remote artifact 进入本地 artifact review。

## 5. 验收

- direct write 不覆盖其他 teammate 改动。
- lease conflict 可见。
- worktree task 可独立跑测试。
- worktree result 可生成 merge artifact。
- A2A 远程 agent 可接 task，但内部状态仍落本地 DB。

