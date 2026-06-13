# Preview Layout Redesign Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Replace broken side-by-side preview layout with vertical stack (options on top, preview below, full-width).

**Architecture:** Remove `joinSideBySide` and side-by-side constants. Rewrite `renderPreviewLayout` to render options list full-width above a full-width preview box. Fix the 1-char overflow bug in `renderPreviewBox`. Fix `focusedIdx` desync in `initList`.

**Tech Stack:** Go, lipgloss v2, Crush's internal `list` package

---

## File Structure

| File | Change |
|------|--------|
| `internal/ui/dialog/preview_box.go` | Fix extra space bug in content line (line 117) |
| `internal/ui/dialog/questions.go` | Rewrite `renderPreviewLayout`, fix `initList` focusedIdx, delete `joinSideBySide`, delete `previewLeftWidth`/`previewMinTotalWidth` constants |

---

### Task 1: Fix extra space bug in preview_box.go

**Files:**
- Modify: `internal/ui/dialog/preview_box.go:114-119`

The content line rendering has an extra `" "` between `line` and `padding`, making each content line 1 char wider than the box borders. This causes the box to overflow its intended width.

- [ ] **Step 1: Fix the content line assembly**

In `internal/ui/dialog/preview_box.go`, lines 114-119, change the content line from:

```go
			contentLines = append(contentLines,
				borderStyle.Render(boxVertical)+" "+
					line+
					" "+padding+" "+
					borderStyle.Render(boxVertical),
			)
```

to:

```go
			contentLines = append(contentLines,
				borderStyle.Render(boxVertical)+" "+
					line+padding+" "+
					borderStyle.Render(boxVertical),
			)
```

The change: remove `" "+` before `padding`. The correct layout is `"│ " + content + padding + " │"` where `content + padding = innerWidth`, giving total width = `innerWidth + 4 = boxWidth`.

- [ ] **Step 2: Verify with existing test**

Run: `go test ./internal/ui/dialog/ -count=1 -v -run TestRenderPreviewBox`
Expected: All tests pass.

- [ ] **Step 3: Commit**

```bash
git add internal/ui/dialog/preview_box.go
git commit -m "fix(ui): remove extra space in preview box content lines causing width overflow"
```

---

### Task 2: Fix focusedIdx desync in initList

**Files:**
- Modify: `internal/ui/dialog/questions.go:136-152`

`initList()` sets `focusedIdx` to the previously selected option, then calls `list.SelectFirst()` which moves the list selection to index 0. This causes the preview to show the wrong option's content.

- [ ] **Step 1: Sync focusedIdx after SelectFirst**

In `internal/ui/dialog/questions.go`, the `initList` function, add `q.focusedIdx = q.list.Selected()` as the last line of the function body, after `q.list.SelectFirst()`:

Current code (lines 150-152):
```go
	q.refreshList()
	q.list.SelectFirst()
}
```

Change to:
```go
	q.refreshList()
	q.list.SelectFirst()
	q.focusedIdx = q.list.Selected()
}
```

- [ ] **Step 2: Build**

Run: `go build ./internal/ui/dialog/`
Expected: Clean compile

- [ ] **Step 3: Commit**

```bash
git add internal/ui/dialog/questions.go
git commit -m "fix(ui): sync focusedIdx with list selection in initList"
```

---

### Task 3: Rewrite renderPreviewLayout to vertical stack

**Files:**
- Modify: `internal/ui/dialog/questions.go:481-543`

Replace the entire side-by-side layout with vertical stack. This is the core change.

- [ ] **Step 1: Replace constants and renderPreviewLayout**

Replace lines 481-515 (the constants `previewLeftWidth`/`previewMinTotalWidth` + the entire `renderPreviewLayout` function) with:

```go
const (
	// previewContentLines is the fixed number of content lines shown in the preview box.
	previewContentLines = 5
)

// renderPreviewLayout renders a vertical stack: options list on top, preview below.
// Both use full innerWidth. Preview is fixed at previewContentLines content rows.
func (q *Questions) renderPreviewLayout(rc *RenderContext, currQ questions.Question, innerWidth, height int) {
	t := q.com.Styles

	// Height budget for non-list/non-preview parts:
	//   title(1) + nav(1) + questionText(3) + notes(1) + help(1) = 7
	// Preview box: previewContentLines + 2 borders + 1 truncation line = 8
	// Remaining space goes to the options list.
	listHeight := max(1, height-7-8)
	q.list.SetSize(innerWidth, listHeight)
	listView := t.Dialog.List.Height(q.list.Height()).Render(q.list.Render())
	rc.AddPart(listView)

	// Preview box — full width, fixed content lines
	previewContent := ""
	if q.focusedIdx >= 0 && q.focusedIdx < len(currQ.Options) {
		previewContent = currQ.Options[q.focusedIdx].Preview
	}
	previewView := renderPreviewBox(previewBoxConfig{
		content:  previewContent,
		width:    innerWidth,
		maxLines: previewContentLines,
		minWidth: 20,
		styles:   t,
	})
	rc.AddPart(previewView)
}
```

- [ ] **Step 2: Delete joinSideBySide function**

Delete the entire `joinSideBySide` function (lines 517-543 after the replacement in Step 1 — look for `func joinSideBySide`). The function body starts with `func joinSideBySide(left, right string, leftWidth, totalWidth int) string {` and ends with the closing `}`. Delete it completely.

- [ ] **Step 3: Update Draw method to remove side-by-side condition**

In `Draw` method, find the line:
```go
		} else if currQ.HasPreview() && area.Dx() >= previewMinTotalWidth {
```

Change it to:
```go
		} else if currQ.HasPreview() {
```

The `previewMinTotalWidth` constant no longer exists. Vertical stack works at any width — the preview box adapts to `innerWidth`.

- [ ] **Step 4: Remove unused imports**

Check if the `ansi` import is still used in `questions.go`. The `joinSideBySide` function used `ansi.Truncate`. After deletion, if `ansi` is only used there, remove it from the import block.

Run: `go build ./internal/ui/dialog/`
Expected: Clean compile. If compiler reports unused import, remove `"github.com/charmbracelet/x/ansi"` from the import block and rebuild.

- [ ] **Step 5: Run tests**

Run: `go test ./internal/ui/dialog/ -count=1 -v`
Expected: All tests pass

- [ ] **Step 6: Commit**

```bash
git add internal/ui/dialog/questions.go
git commit -m "refactor(ui): replace side-by-side preview with vertical stack layout"
```

---

### Task 4: Update preview box width test

**Files:**
- Modify: `internal/ui/dialog/preview_box_test.go:64-78`

The existing test allows lines up to 36 chars for a 30-wide box (it was written to tolerate the bug). Tighten the assertion now that the bug is fixed.

- [ ] **Step 1: Tighten the width assertion**

In `internal/ui/dialog/preview_box_test.go`, the "respects width constraint" test case, change:

```go
				require.LessOrEqual(t, visualWidth, 36,
					"line visual width %d exceeds reasonable bounds: %q", visualWidth, line)
```

to:

```go
				require.LessOrEqual(t, visualWidth, 30,
					"line visual width %d exceeds box width 30: %q", visualWidth, line)
```

- [ ] **Step 2: Run tests**

Run: `go test ./internal/ui/dialog/ -count=1 -v -run TestRenderPreviewBox`
Expected: All tests pass with the tighter assertion

- [ ] **Step 3: Commit**

```bash
git add internal/ui/dialog/preview_box_test.go
git commit -m "test(ui): tighten preview box width assertion after bug fix"
```

---

### Task 5: Build and verify

- [ ] **Step 1: Full build**

Run: `go build ./...`
Expected: Clean compile

- [ ] **Step 2: All tests**

Run: `go test ./internal/ui/dialog/ ./internal/questions/ ./internal/agent/tools/ -count=1`
Expected: All pass

- [ ] **Step 3: Final commit (if any test fixes needed)**

Only if additional fixes were needed during verification.
