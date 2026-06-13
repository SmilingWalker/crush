# Preview Layout Redesign

## Problem

The current side-by-side preview layout in `ask_user_questions` dialog has fundamental issues:

1. **Width overflow**: Preview box content lines are 1 char wider than borders (bug: extra space in `" "+padding+" "+`). Combined with side-by-side layout, total width overflows by 2 chars.
2. **Insufficient space**: 30 chars for markdown rendering is too narrow. Content wraps badly or gets aggressively truncated.
3. **Uncontrolled height**: Hardcoded offsets (`height-10`, `height-6`) don't account for all dialog parts. Preview box can render 14+ content lines, causing total dialog to overflow the screen.
4. **focusedIdx desync**: `initList()` sets `focusedIdx` from selected options but then calls `SelectFirst()`, leaving the preview showing the wrong option's content.

## Design Decision

**Switch from side-by-side to vertical stack layout.**

Options list renders full-width at the top. Preview box renders full-width below it. Fixed 5 content lines for preview (7 total with borders).

## Architecture

### Layout (vertical stack)

```
┌─ Question ╱╱╱╱╱╱╱╱╱╱╱╱╱╱╱╱╱╱╱╱╱╱╱╱╱╱╱╱╱╱╱╱┐
│ ← ☐ Q1 ☐ Q2 … Submit →                      │
│                                               │
│ Which auth method?                            │
│                                               │
│ ◉ RESTful API                                 │
│   Simple and widely adopted                   │
│ ○ GraphQL                                     │
│   Flexible queries                            │
│ ○ RPC                                         │
│   High performance                            │
│ ○ Other...                                    │
│   Provide a custom answer                     │
│                                               │
│ ┌───────────────────────────────────────────┐ │
│ │ // GET /api/v1/users                      │ │
│ │ Content-Type: application/json            │ │
│ │                                           │ │
│ │ { "users": [...] }                        │ │
│ │ ...more lines...                          │ │
│ └───────────────────────────────────────────┘ │
│ press n to add notes                          │
│ esc exit · ↑↓ up/down · ←→ prev/next · n notes│
└────────────────────────────────────────────────┘
```

### Height budget

| Part | Lines |
|------|-------|
| Title | 1 |
| Nav bar | 1 |
| Question text (Padding(1,2)) | 3 |
| Options list (scrollable) | variable, max ~8 |
| Preview box (5 content + 2 borders) | 7 |
| Notes hint | 1 |
| Help bar | 1 |
| **Total** | **~22** |

Options list is scrollable via the existing `list.SetSize(w, h)` — if more items than space allows, the list viewport scrolls. Preview is fixed at 5 content lines.

### Changes

#### 1. Fix `preview_box.go` line 117

Remove extra space between content and padding:

```
// Before (bug): │ " " + line + " " + padding + " " + │  → innerWidth + 5
// After (fix):  │ " " + line + padding + " " + │        → innerWidth + 4 = boxWidth
```

#### 2. Rewrite `renderPreviewLayout` in `questions.go`

Replace side-by-side logic with vertical stack:

- Calculate available height: `availableHeight = height - fixedParts`
- Split between options list and preview: options get remaining space, preview gets fixed 5 content lines
- Render options list full-width via `list.SetSize(innerWidth, listHeight)`
- Render preview box full-width via `renderPreviewBox` with `width: innerWidth`
- Add both as separate `rc.AddPart()` calls (stacked vertically)

#### 3. Fix `focusedIdx` sync in `initList()`

After `q.list.SelectFirst()`, add `q.focusedIdx = q.list.Selected()` to keep preview content in sync with focused option.

#### 4. Remove dead code

- Delete `joinSideBySide` function
- Delete `previewLeftWidth` and `previewMinTotalWidth` constants
- Remove side-by-side width check condition (`area.Dx() >= previewMinTotalWidth`)

### Files modified

| File | Change |
|------|--------|
| `internal/ui/dialog/preview_box.go` | Fix extra space bug in content line rendering |
| `internal/ui/dialog/questions.go` | Rewrite `renderPreviewLayout` to vertical stack, fix `initList` focusedIdx, remove `joinSideBySide` and constants |

### Preview box with full width

With `innerWidth ≈ 64` (dialog max 70 minus frame), the preview box content area becomes `64 - 4 = 60` chars wide. Markdown renders well at 60 chars — code blocks, lists, paragraphs all have room.

### Fallback behavior

Questions without preview (`!currQ.HasPreview()`) are unaffected — they continue using the full-width options list without any preview area. The `renderPreviewLayout` function is only called when `currQ.HasPreview()` is true.

## Verification

1. `go build ./...` — clean compile
2. `go test ./internal/ui/dialog/ -count=1` — all pass
3. Manual: navigate options with up/down → preview updates correctly
4. Manual: long preview content → truncated at 5 lines with ✂ indicator
5. Manual: no preview → options list uses full space, no empty area
