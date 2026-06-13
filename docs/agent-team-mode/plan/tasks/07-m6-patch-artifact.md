# M6: 安全补丁 — 开发任务拆分

> 里程碑：M6 | 任务数：10 | 总工时：14.5 人天 | 建议周期：3-4 周
> 依赖：M5 (PermissionBridge) + M4 (MemberRunner)

---

## M6-01: Patch Artifact Schema

**工时**: 1.5 天

### PatchArtifact 结构体

```go
type ChangeKind string
const (
    ChangeAdd    ChangeKind = "add"
    ChangeModify ChangeKind = "modify"
    ChangeDelete ChangeKind = "delete"
    ChangeRename ChangeKind = "rename"
    ChangeBinary ChangeKind = "binary"
)

type ApplyStatus string
const (
    ApplyPending  ApplyStatus = "pending"
    ApplyApplied  ApplyStatus = "applied"
    ApplyRejected ApplyStatus = "rejected"
    ApplyConflict ApplyStatus = "conflict"
    ApplyFailed   ApplyStatus = "failed"
)

type TouchedFile struct {
    Path       string     `json:"path"`
    OldHash    string     `json:"old_hash,omitempty"`
    NewHash    string     `json:"new_hash,omitempty"`
    ChangeKind ChangeKind `json:"change_kind"`
}

type PatchArtifact struct {
    ID                    string        `json:"id"`
    TeamID                string        `json:"team_id"`
    WorkspaceID           string        `json:"workspace_id"`
    BaseRef               string        `json:"base_ref"`       // commit hash
    BaseHash              string        `json:"base_hash"`      // touched files hash
    TouchedFiles          []TouchedFile `json:"touched_files"`
    PatchContentRef       string        `json:"patch_content_ref"`  // opaque
    PatchContentHash      string        `json:"patch_content_hash"`
    GeneratedByRunID      string        `json:"generated_by_run_id"`
    GeneratedByMemberID   string        `json:"generated_by_member_id"`
    ApplyStatus           ApplyStatus   `json:"apply_status"`
    ApplyResult           string        `json:"apply_result,omitempty"`
    VerificationLogArtifactID string    `json:"verification_log_artifact_id,omitempty"`
    CreatedAt             time.Time     `json:"created_at"`
}
```

### 约束

- Patch content 不在 DB（只有 content_ref + hash）
- Binary change 只生成 artifact，不 allow apply
- Content ref 是 opaque SHA256[:16]，不接受路径型 ref

---

## M6-02: ContentStore

**工时**: 1 天

```go
type ContentStore interface {
    Put(ctx context.Context, workspaceID, artifactID string, content []byte) (contentRef string, contentHash string, err error)
    Get(ctx context.Context, workspaceID, artifactID string, contentRef string) ([]byte, error)
    Verify(ctx context.Context, workspaceID, artifactID, contentRef string, expectedHash string) (bool, error)
    Delete(ctx context.Context, workspaceID, artifactID, contentRef string) error
}

var (
    ErrArtifactIntegrityFailed = errors.New("artifact integrity check failed: content hash mismatch")
    ErrContentRefInvalid       = errors.New("content ref is invalid: must be opaque, no path separators")
)
```

- contentRef = SHA256(workspaceID + artifactID + random_nonce)[:16]
- contentHash = SHA256(content)
- Get() 重新计算 hash 验证
- Verify() 比较 expectedHash
- contentRef 拒绝 "/"  "\\"  ".."  "file://"

---

## M6-03: Filesystem Blob Store

**工时**: 1 天

```go
type FilesystemBlobStore struct {
    root string // {DataDirectory}/blobs/
    mu   sync.RWMutex
}
```

- root 在 app data 下，不在 workspace path
- backing path 不出现在 API/DB/UI
- 实现 ContentStore 接口

---

## M6-04: Apply Service

**工时**: 2.5 天 | **⚠️ 高风险**

### 6 步 apply 流程

```go
func (as *ApplyService) Apply(ctx context.Context, patchID string) (*ApplyResult, error) {
    // Step 1: Load patch artifact
    patch, _ := as.patchStore.GetPatchArtifact(ctx, patchID)

    // Step 2: Verify base_hash for ALL touched files
    for _, tf := range patch.TouchedFiles {
        currentHash, _ := hashFile(tf.Path)
        if currentHash != tf.BaseHash {
            // 任一 mismatch → 整体 no-write → conflict
            as.conflictStore.RecordConflict(ctx, patchID, tf.Path, tf.BaseHash, currentHash, "base_mismatch")
            return &ApplyResult{Status: "conflict", Reason: fmt.Sprintf("base mismatch on %s", tf.Path)}, nil
        }
    }

    // Step 3: Load patch content from ContentStore
    patchContent, _ := as.contentStore.Get(ctx, patch.WorkspaceID, patchID, patch.PatchContentRef)

    // Step 4: Per-file atomic apply
    applied := make(map[string]string) // map[path]backup_path
    for _, tf := range patch.TouchedFiles {
        // per-path lock
        mu := as.getPerPathMutex(tf.Path)
        mu.Lock()
        // pre-write recheck
        currentHash, _ := hashFile(tf.Path)
        if currentHash != tf.BaseHash {
            mu.Unlock()
            as.rollback(applied) // rollback all applied files
            as.conflictStore.RecordConflict(ctx, ...)
            return &ApplyResult{Status: "conflict", Reason: "pre-write mismatch"}, nil
        }
        // backup → temp write → fsync → close → target hash check → atomic rename
        backup, _ := backupFile(tf.Path)
        tmpFile, _ := os.CreateTemp(as.workspaceRoot, "crush-patch-*")
        tmpFile.Write(applyHunk(patchContent, tf))
        tmpFile.Sync()
        tmpFile.Close()
        os.Rename(tmpFile.Name(), tf.Path)
        applied[tf.Path] = backup
        mu.Unlock()
    }

    // Step 5: Success → update artifact status + audit
    as.patchStore.UpdateApplyStatus(ctx, patchID, ApplyApplied)

    // Step 6: Cleanup backups
    for _, backup := range applied { os.Remove(backup) }
    return &ApplyResult{Status: "applied", TouchedFiles: len(patch.TouchedFiles)}, nil
}
```

### rollback

```go
func (as *ApplyService) rollback(applied map[string]string) {
    for path, backup := range applied {
        os.Rename(backup, path) // restore original
    }
    // 失败时生成 partial_apply conflict + audit
}
```

### 验收标准

1. base_hash 全匹配 → apply 成功
2. 任一 mismatch → 整体 no-write → conflict artifact
3. Pre-write recheck mismatch → rollback
4. Rollback 失败 → partial_apply conflict + audit
5. Temp file + atomic rename 路径正确
6. Binary → ErrBinaryNotApplicable

### 高风险标注

多文件 apply 不承诺 filesystem-level atomic。rollback 可能中间失败导致 workspace 部分修改。缓解：M6 第一版 best-effort rollback + rollback_failed conflict artifact + audit。后续考虑 copy-on-write 或 worktree。

---

## M6-05 ~ M6-09: Conflict、UI、Verification、Binary、E2E

| 任务 | 工时 | 核心产出 |
|------|------|----------|
| M6-05 Apply Conflict | 1.0d | team_apply_conflicts 表 + conflict artifact |
| M6-06 Patch Review UI | 2.0d | unified diff viewer + file/hunk 导航 + 大diff summary + binary review-only |
| M6-07 Apply/Reject UI | 1.5d | 确认弹窗 + 结果/audit + conflict 详情 |
| M6-08 Verification Log | 1.5d | bash allow-list (go test/npm test等5种) + leader-triggered + output redaction |
| M6-09 Binary Handling | 0.5d | binary → ErrBinaryNotApplicable + UI [review only] |
| M6-10 M6 E2E | 2.0d | 7场景: generate/review/apply/conflict/reject/binary/verification/integrity |

---

