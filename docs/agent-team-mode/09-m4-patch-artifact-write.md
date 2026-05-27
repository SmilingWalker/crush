# 09 M4 Patch Artifact Write Mode

M4 允许 teammate 参与代码修改，但第一阶段不直接写主工作区，而是生成 patch artifact。

## 1. 为什么先 patch artifact

当前 file safety 主要依赖 read timestamp 和 mtime。多 agent 下风险太高：

- 同秒修改可能漏检。
- permission 等待后文件可能变化。
- shell 写绕过 edit/write。
- 多 teammate 后写覆盖先写。

patch artifact 的优势：

- teammate 不污染工作区。
- leader 可 review。
- apply 前可校验 base hash。
- conflict 可作为 artifact 保存。

## 2. Patch artifact 字段

```text
artifact_id
team_id
task_id
run_id
created_by_member_id
kind=patch
title
patch_text
touched_files
base_hashes
verification_logs
apply_status
created_at
```

每个 touched file：

```text
path
exists
base_hash
git_blob_oid
mode
size
```

## 3. 生成流程

```text
teammate runs in read-only or sandbox
  -> proposes changes
  -> creates patch artifact
  -> records touched files/base hashes
  -> optional verification logs
leader reviews
```

实现选择：

- 简单阶段：LLM 直接生成 unified diff。
- 更可靠阶段：临时 sandbox/worktree 生成 diff。

## 4. Apply 流程

```text
load artifact
validate touched file base hash
if all clean:
  apply patch
  record new hash
  mark artifact applied
else:
  create conflict artifact
  do not write
```

要求：

- apply 前必须重新读取文件。
- base mismatch 不覆盖。
- partial apply 需要明确 rollback 或记录 partial failure。

## 5. UI

展示：

- artifact list。
- touched files。
- diff preview。
- verification logs。
- apply/reject。
- conflict details。

## 6. 验收

- teammate 产 patch。
- leader 可 review。
- clean patch 可 apply。
- base mismatch 生成 conflict。
- apply 后记录 new hash。
- reject 后状态可追踪。

