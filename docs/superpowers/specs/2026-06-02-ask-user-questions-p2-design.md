# Ask User Questions Tool — P2 Design Spec: Rich Interaction

## Overview

P2 adds rich interaction features to the `ask_user_questions` tool built in P1:
- **Preview panel**: Side-by-side option list + markdown preview when options have `preview` content
- **Annotations**: User notes attached to answers via `n` key
- **Tool prompt update**: Document preview and annotation features for the LLM

Built on top of P1's pub/sub service, data models, and TUI dialog infrastructure.

## Phasing within P2

### P2-a: Preview Panel (this spec)

**Goal:** When any option in a question has a `preview` field, the dialog switches to a side-by-side layout: a narrow option list on the left (30 chars) and a markdown-rendered preview box on the right.

**Scope:**
- Add `Preview string` to `Option` struct (was reserved in P1 design)
- Create `preview_box.go` component with box-drawing borders, markdown rendering, and line truncation
- Modify `questions.go` to detect preview mode and render side-by-side
- Modify `question_options.go` to track focused index for preview switching
- Fallback to P1 single-column layout when no options have preview content
- Fallback to single-column when terminal is too narrow (< 60 columns)

### P2-b: Annotations

**Goal:** Users can press `n` to add notes to their answer selection. Notes are returned with the answer.

**Scope:**
- Add `Annotation string` to `Answer` struct
- Add notes input state to `Questions` dialog
- Render notes area below preview / options
- Include annotations in tool response formatting

### P2-c: Tool Prompt Update

**Goal:** Update the embedded tool prompt to document preview and annotation features.

**Scope:**
- Update `ask_user_questions.md` with preview usage notes and annotation info

---

## P2-a: Preview Panel — Detailed Design

### Data Model Changes

```go
// Option — add Preview field
type Option struct {
    Label       string `json:"label"`
    Description string `json:"description"`
    Preview     string `json:"preview,omitempty"` // P2: markdown preview content
}
```

No validation changes — `Preview` is purely optional. Existing tests pass unchanged.

### Preview Detection

A question enters "preview mode" when **any** of its options has a non-empty `Preview` field:

```go
func (q Question) HasPreview() bool {
    for _, opt := range q.Options {
        if opt.Preview != "" {
            return true
        }
    }
    return false
}
```

This is checked in the dialog's `Draw()` and `HandleMsg()` to switch layout behavior.

### Layout: Side-by-Side

When in preview mode, the dialog splits into two columns:

```
┌──────────────────────────────────────────────────────────────────┐
│ ← [☑ Auth method] [☐ Library] →                                 │
│                                                                  │
│ Which auth method?                                               │
│                                                                  │
│  ◉ JWT (Recommended)    │ ┌────────────────────────────────────┐ │
│  ○ Session cookies      │ │ // jwt-middleware.go               │ │
│  ○ OAuth2               │ │ func JWTMiddleware(secret string)  │ │
│                         │ │   http.Handler {                   │ │
│                         │ │     return http.HandlerFunc(       │ │
│                         │ │       func(w http.ResponseWriter,  │ │
│                         │ │ ─── ✂ ─── 12 lines hidden ─────── │ │
│                         │ └────────────────────────────────────┘ │
│                                                                  │
│ up/down choose · enter select · n notes · esc close              │
└──────────────────────────────────────────────────────────────────┘
```

**Dimensions:**
- Left panel: **30 chars** fixed width
- Gap: **2 chars** (vertical separator `│` + space)
- Right panel: remaining width (total - left - gap - borders)
- Minimum terminal width for side-by-side: **60 columns** — below this, fall back to single-column

**Focused option drives preview content:**
- When user navigates up/down, the focused option's `Preview` is rendered in the right panel
- If focused option has no preview, show "No preview available" placeholder
- On initial display, focus the first option (or the previously selected one)

### PreviewBox Component

New file: `internal/ui/dialog/preview_box.go`

Renders a bordered box with markdown content:

```
┌──────────────────────────────┐
│ // rendered markdown content │
│ with syntax highlighting     │
│ and word wrapping            │
│                              │
│ ─── ✂ ─── 3 lines hidden ── │
└──────────────────────────────┘
```

**Box-drawing characters:**
```
topLeft:    ┌    topRight:    ┐
bottomLeft: └    bottomRight: ┘
horizontal: ─    vertical:    │
```

**Rendering pipeline:**
1. Receive raw markdown string + max width + max lines
2. Render markdown via `common.MarkdownRenderer(sty, innerWidth)`
3. Split rendered output into lines
4. Truncate lines exceeding max count, append truncation bar
5. Wrap each line in `│ content │` borders
6. Prepend top border `┌─...─┐`, append bottom border `└─...─┘`

**Line truncation:**
- `maxLines` computed from available dialog height minus overhead
- When content exceeds maxLines, show truncation bar: `├─── ✂ ─── N lines hidden ───┤`
- Truncation bar uses warning color style

**Width handling:**
- `minWidth` = 20 (default)
- `maxWidth` = parent container's available width minus borders
- `innerWidth` = `boxWidth - 4` (2 border chars + 2 padding chars per line)
- Lines wider than innerWidth are truncated with ansi-aware truncation

### Questions Dialog Changes

**State additions:**
```go
type Questions struct {
    // ... existing P1 fields ...
    focusedIdx int  // index of the option being previewed (not selected, just focused/hovered)
}
```

**HandleMsg changes:**
- Up/down navigation updates `focusedIdx` in addition to list selection
- When navigating to a different question, reset `focusedIdx` to 0 (or previously selected option)
- Quick-select by number (1-9) also updates `focusedIdx` and shows preview

**Draw changes:**
- Check if current question `HasPreview()`
- If yes AND terminal width >= 60: render side-by-side layout
  - Left column: option list rendered at fixed 30-char width
  - Right column: PreviewBox with focused option's preview content
- If no OR terminal too narrow: render P1 single-column layout

**Side-by-side rendering in Draw():**
```go
// Pseudo-code
if currQ.HasPreview() && innerWidth >= 60 {
    leftWidth := 30
    rightWidth := innerWidth - leftWidth - 2 // gap

    // Render option list at leftWidth
    leftView := renderOptionsList(leftWidth)

    // Render preview box at rightWidth
    previewContent := currQ.Options[focusedIdx].Preview
    rightView := renderPreviewBox(previewContent, rightWidth, maxLines)

    // Combine side by side
    rc.AddPart(joinSideBySide(leftView, rightView))
} else {
    // P1 layout
    rc.AddPart(listView)
}
```

### question_options.go Changes

- Add `FocusedIdx() int` method to `questionOptionsList` to expose the focused/selected index
- When option list selection changes, the parent dialog reads the focused index to update preview
- The list is rendered at a constrained width (30 chars) when in preview mode

### Tool Prompt Changes (P2-c, after P2-a)

The tool prompt will be updated to include:
```markdown
Preview:
- Add a "preview" field to options with markdown content
- Preview is displayed side-by-side with the option list
- Use for code snippets, file contents, or detailed descriptions
- Preview content should be concise (excessive lines will be truncated)
```

---

## File Manifest for P2-a

### New Files

| File | Purpose |
|------|---------|
| `internal/ui/dialog/preview_box.go` | PreviewBox component with markdown rendering, borders, truncation |

### Modified Files

| File | Change |
|------|--------|
| `internal/questions/service.go` | Add `Preview` field to `Option` struct, add `HasPreview()` method to `Question` |
| `internal/ui/dialog/questions.go` | Preview mode detection, side-by-side layout in Draw(), focusedIdx tracking in HandleMsg() |
| `internal/ui/dialog/question_options.go` | Constrained-width rendering for preview mode, focused index exposure |

### Test Files

| File | Change |
|------|--------|
| `internal/questions/service_test.go` | Add test for `HasPreview()` |
| `internal/ui/dialog/preview_box_test.go` | New: unit tests for preview box rendering |

---

## P2-b: Annotations — Detailed Design (Brief)

### Data Model Changes

```go
// Answer — add Annotation field
type Answer struct {
    QuestionText string `json:"question"`
    Selected     string `json:"selected"`
    IsOther      bool   `json:"is_other"`
    Annotation   string `json:"annotation,omitempty"` // P2: user notes
}
```

### Dialog Changes

- New key binding: `n` to enter notes input mode
- New state: `isInNotesInput bool`, `notesTexts map[int]string` (per question)
- In notes mode: show TextInput below preview/options
- When not in notes mode: show "press n to add notes" placeholder
- Notes are included in `buildSubmitAction()` as `Answer.Annotation`

### Response Format

Annotations included in the tool response:
```
User answered: "Which auth?"="JWT" (notes: "make sure to use RS256").
```

---

## Key Reference: Crush Markdown Infrastructure

From `internal/ui/common/markdown.go`:
- `MarkdownRenderer(sty *styles.Styles, width int) *glamour.TermRenderer` — memoized per width
- NOT concurrent-safe (TUI is single-threaded, ok for dialog rendering)
- Uses `sty.Markdown` for style config
- Uses custom Chroma formatter registered as "crush"
- Word wrapping via `glamour.WithWordWrap(width)`

For PreviewBox, use `MarkdownRenderer` with the preview's inner width, render the content, split into lines, then wrap with box-drawing borders.

---

## Open Questions for P2-a Implementation

1. **Preview max content length**: Claude Code defaults to 20 lines. For Crush, should we match this or use a different default? → **Match 20 lines default**
2. **Minimum width for side-by-side**: 60 columns seems reasonable. Below that, fall back to single-column. → **60 columns confirmed**
3. **Focused index initialization**: When navigating to a question that already has a selection, should preview show the selected option or the first? → **Show selected option if exists, otherwise first**
