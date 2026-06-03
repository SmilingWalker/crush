# Ask User Questions P2-a — Preview Panel Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add side-by-side preview panel to the questions dialog when options have `preview` markdown content.

**Architecture:** When any option has a non-empty `Preview` field, the dialog detects "preview mode" and renders a left option list (30 chars) + right preview box. Uses `common.MarkdownRenderer` (glamour v2) for markdown rendering with box-drawing borders and line truncation.

**Tech Stack:** Go, Bubble Tea v2, lipgloss v2, glamour v2

**Design Spec:** `docs/superpowers/specs/2026-06-02-ask-user-questions-p2-design.md`

---

## File Structure

| File | Responsibility |
|------|---------------|
| `internal/questions/service.go` | Add `Preview` field to `Option`, add `HasPreview()` to `Question` |
| `internal/questions/service_test.go` | Test `HasPreview()` |
| `internal/ui/dialog/preview_box.go` | PreviewBox component: borders, markdown, truncation |
| `internal/ui/dialog/preview_box_test.go` | PreviewBox unit tests |
| `internal/ui/dialog/questions.go` | Preview mode detection, side-by-side layout, focusedIdx tracking |
| `internal/ui/dialog/question_options.go` | Constrained-width rendering, focused index helper |

---

### Task 1: Add Preview Field to Data Model

**Files:**
- Modify: `internal/questions/service.go`

- [ ] **Step 1: Add `Preview` field to `Option` and `HasPreview()` to `Question`**

In `internal/questions/service.go`, update the `Option` struct to add the `Preview` field:

```go
// Option represents a single answer option for a question.
type Option struct {
	Label       string `json:"label"`
	Description string `json:"description"`
	Preview     string `json:"preview,omitempty"`
}
```

Then add the `HasPreview()` method to `Question`:

```go
// HasPreview returns true if any option in this question has preview content.
func (q Question) HasPreview() bool {
	for _, opt := range q.Options {
		if opt.Preview != "" {
			return true
		}
	}
	return false
}
```

- [ ] **Step 2: Verify existing tests still pass**

```bash
cd G:/ai-project/remote-github/crush && go test ./internal/questions/ -v
```

Expected: All 17 existing tests still PASS (Preview field is optional, no validation changes).

- [ ] **Step 3: Commit**

```bash
git add internal/questions/service.go
git commit -m "feat(questions): add Preview field to Option and HasPreview method"
```

---

### Task 2: Test HasPreview

**Files:**
- Modify: `internal/questions/service_test.go`

- [ ] **Step 1: Add HasPreview tests**

Add these tests to `internal/questions/service_test.go`:

```go
func TestQuestion_HasPreview(t *testing.T) {
	tests := []struct {
		name     string
		question Question
		want     bool
	}{
		{
			name: "no preview",
			question: Question{
				Question: "Q?", Header: "H",
				Options: []Option{{Label: "A"}, {Label: "B"}},
			},
			want: false,
		},
		{
			name: "one option has preview",
			question: Question{
				Question: "Q?", Header: "H",
				Options: []Option{
					{Label: "A", Preview: "some code"},
					{Label: "B"},
				},
			},
			want: true,
		},
		{
			name: "all options have preview",
			question: Question{
				Question: "Q?", Header: "H",
				Options: []Option{
					{Label: "A", Preview: "code a"},
					{Label: "B", Preview: "code b"},
				},
			},
			want: true,
		},
		{
			name: "empty preview string is ignored",
			question: Question{
				Question: "Q?", Header: "H",
				Options: []Option{
					{Label: "A", Preview: ""},
					{Label: "B"},
				},
			},
			want: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.Equal(t, tt.want, tt.question.HasPreview())
		})
	}
}
```

- [ ] **Step 2: Run tests**

```bash
go test ./internal/questions/ -run TestQuestion_HasPreview -v
```

Expected: All PASS.

- [ ] **Step 3: Commit**

```bash
git add internal/questions/service_test.go
git commit -m "test(questions): add HasPreview tests"
```

---

### Task 3: Create PreviewBox Component

**Files:**
- Create: `internal/ui/dialog/preview_box.go`

This component renders a bordered box with markdown content, following the pattern from Claude Code's `PreviewBox.tsx` but adapted to Go + Bubble Tea.

- [ ] **Step 1: Create the PreviewBox**

```go
// internal/ui/dialog/preview_box.go
package dialog

import (
	"fmt"
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/crush/internal/ui/common"
	"github.com/charmbracelet/crush/internal/ui/styles"
	"github.com/charmbracelet/x/ansi"
)

// Box-drawing characters for preview borders.
const (
	boxTopLeft    = "┌"
	boxTopRight   = "┐"
	boxBottomLeft = "└"
	boxBottomRight = "┘"
	boxHorizontal = "─"
	boxVertical   = "│"
	boxTeeLeft    = "├"
	boxTeeRight   = "┤"
	scissors      = "✂"
)

// previewBoxConfig holds configuration for rendering a preview box.
type previewBoxConfig struct {
	content   string
	width     int
	maxLines  int
	minWidth  int
	styles    *styles.Styles
}

// renderPreviewBox renders markdown content inside a bordered box.
// Returns the rendered string and the actual height used.
func renderPreviewBox(cfg previewBoxConfig) string {
	if cfg.minWidth <= 0 {
		cfg.minWidth = 20
	}
	if cfg.maxLines <= 0 {
		cfg.maxLines = 20
	}

	// Calculate box dimensions
	innerWidth := cfg.width - 4 // borders + padding
	if innerWidth < cfg.minWidth-4 {
		innerWidth = cfg.minWidth - 4
	}
	boxWidth := innerWidth + 4

	// Render markdown content
	var rendered string
	if strings.TrimSpace(cfg.content) == "" {
		rendered = lipgloss.NewStyle().Italic(true).Faint(true).Render("No preview available")
	} else {
		renderer := common.MarkdownRenderer(cfg.styles, innerWidth)
		rendered = renderer.Render(cfg.content)
	}

	// Split into lines
	rendered = strings.TrimRight(rendered, "\n")
	lines := strings.Split(rendered, "\n")

	// Truncate if needed
	isTruncated := len(lines) > cfg.maxLines
	if isTruncated {
		lines = lines[:cfg.maxLines]
	}

	// Build borders
	hBorder := strings.Repeat(boxHorizontal, boxWidth-2)
	topBorder := boxTopLeft + hBorder + boxTopRight
	bottomBorder := boxBottomLeft + hBorder + boxBottomRight

	// Build truncation bar if needed
	truncationBar := ""
	if isTruncated {
		hiddenCount := len(strings.Split(rendered, "\n")) - cfg.maxLines
		label := fmt.Sprintf("%s %s %s %d lines hidden ",
			strings.Repeat(boxHorizontal, 3), scissors, strings.Repeat(boxHorizontal, 3), hiddenCount)
		labelWidth := lipgloss.Width(label)
		fillWidth := max(0, boxWidth-2-labelWidth)
		truncationBar = boxTeeLeft + label + strings.Repeat(boxHorizontal, fillWidth) + boxTeeRight
	}

	// Build content lines
	var contentLines []string
	for _, line := range lines {
		lineWidth := lipgloss.Width(line)
		if lineWidth > innerWidth {
			line = ansi.Truncate(line, innerWidth, "")
		}
		padding := strings.Repeat(" ", max(0, innerWidth-lipgloss.Width(line)))
		contentLines = append(contentLines,
			lipgloss.NewStyle().Faint(true).Render(boxVertical)+" "+
				line+
				" "+padding+" "+
				lipgloss.NewStyle().Faint(true).Render(boxVertical),
		)
	}

	// Assemble box
	var parts []string
	parts = append(parts, lipgloss.NewStyle().Faint(true).Render(topBorder))
	parts = append(parts, contentLines...)
	if truncationBar != "" {
		parts = append(parts, lipgloss.NewStyle().Foreground(lipgloss.Color("11")).Render(truncationBar))
	}
	parts = append(parts, lipgloss.NewStyle().Faint(true).Render(bottomBorder))

	return strings.Join(parts, "\n")
}
```

- [ ] **Step 2: Verify compilation**

```bash
go build ./internal/ui/dialog/
```

- [ ] **Step 3: Commit**

```bash
git add internal/ui/dialog/preview_box.go
git commit -m "feat(ui): add PreviewBox component for markdown preview rendering"
```

---

### Task 4: PreviewBox Tests

**Files:**
- Create: `internal/ui/dialog/preview_box_test.go`

- [ ] **Step 1: Write PreviewBox tests**

```go
// internal/ui/dialog/preview_box_test.go
package dialog

import (
	"strings"
	"testing"

	"github.com/charmbracelet/crush/internal/ui/styles"
	"github.com/stretchr/testify/require"
)

func TestRenderPreviewBox(t *testing.T) {
	sty := styles.DefaultStyles()

	t.Run("empty content shows placeholder", func(t *testing.T) {
		result := renderPreviewBox(previewBoxConfig{
			content:  "",
			width:    40,
			maxLines: 10,
			styles:   sty,
		})
		require.Contains(t, result, "No preview available")
		require.Contains(t, result, boxTopLeft)
		require.Contains(t, result, boxBottomRight)
	})

	t.Run("renders markdown content", func(t *testing.T) {
		result := renderPreviewBox(previewBoxConfig{
			content:  "# Hello\n\nThis is **bold** text.",
			width:    50,
			maxLines: 10,
			styles:   sty,
		})
		require.Contains(t, result, boxTopLeft)
		require.Contains(t, result, boxBottomRight)
		require.Contains(t, result, boxVertical)
		// Should have at least 2 content lines (heading + paragraph)
		lines := strings.Split(result, "\n")
		require.GreaterOrEqual(t, len(lines), 4) // top + at least 2 content + bottom
	})

	t.Run("truncates long content", func(t *testing.T) {
		longContent := strings.Repeat("line\n", 30)
		result := renderPreviewBox(previewBoxConfig{
			content:  longContent,
			width:    40,
			maxLines: 5,
			styles:   sty,
		})
		require.Contains(t, result, scissors)
		require.Contains(t, result, "lines hidden")
	})

	t.Run("no truncation for short content", func(t *testing.T) {
		result := renderPreviewBox(previewBoxConfig{
			content:  "short",
			width:    40,
			maxLines: 20,
			styles:   sty,
		})
		require.NotContains(t, result, scissors)
	})

	t.Run("respects width constraint", func(t *testing.T) {
		result := renderPreviewBox(previewBoxConfig{
			content:  "some content",
			width:    30,
			maxLines: 10,
			minWidth: 20,
			styles:   sty,
		})
		for _, line := range strings.Split(result, "\n") {
			require.LessOrEqual(t, lipglossWidth(line), 30,
				"line exceeds width: %q", line)
		}
	})
}

// lipglossWidth helper to measure visual width.
func lipglossWidth(s string) int {
	return lipgloss.Width(s)
}
```

Note: The `lipglossWidth` function wraps `lipgloss.Width`. The import for `charm.land/lipgloss/v2` is needed.

- [ ] **Step 2: Run tests**

```bash
go test ./internal/ui/dialog/ -run TestRenderPreviewBox -v
```

Expected: All PASS.

- [ ] **Step 3: Commit**

```bash
git add internal/ui/dialog/preview_box_test.go
git commit -m "test(ui): add PreviewBox rendering tests"
```

---

### Task 5: Update Questions Dialog for Preview Mode

**Files:**
- Modify: `internal/ui/dialog/questions.go`

This is the core task — adding preview mode detection, side-by-side rendering, and focused index tracking.

- [ ] **Step 1: Add `focusedIdx` field to Questions struct**

Add `focusedIdx int` to the `Questions` struct:

```go
type Questions struct {
	com *common.Common
	req questions.QuestionsRequest

	// State
	currQuestion  int
	selectedOpts  map[int]map[int]bool
	otherTexts    map[int]string
	isInTextInput bool
	textInput     string
	focusedIdx    int // P2: index of the option being previewed

	// ... rest unchanged
}
```

- [ ] **Step 2: Update `initList()` to reset focusedIdx**

In `initList()`, after `q.selectedOpts` initialization, add focused index reset logic:

```go
func (q *Questions) initList() {
	if len(q.req.Questions) == 0 {
		return
	}
	if q.selectedOpts[q.currQuestion] == nil {
		q.selectedOpts[q.currQuestion] = make(map[int]bool)
	}
	// Reset focused index: prefer previously selected option, else 0
	q.focusedIdx = 0
	for optIdx, sel := range q.selectedOpts[q.currQuestion] {
		if sel {
			q.focusedIdx = optIdx
			break
		}
	}
	q.refreshList()
	q.list.SelectFirst()
}
```

- [ ] **Step 3: Update `HandleMsg()` to track focused index on navigation**

In the Up/Down navigation cases within `HandleMsg()`, update `focusedIdx`:

After the existing up/down list navigation, add:
```go
q.focusedIdx = q.list.Selected()
```

Also update after `handleSelect()`:
```go
case key.Matches(msg, q.keyMap.Select):
    return q.handleSelect()
```

In `handleSelect()`, after the option index is determined, set `q.focusedIdx = idx` at the start of the method.

- [ ] **Step 4: Update `Draw()` for preview mode**

Replace the current `Draw()` method's options rendering section with preview-aware logic:

```go
func (q *Questions) Draw(scr uv.Screen, area uv.Rectangle) *tea.Cursor {
	if len(q.req.Questions) == 0 {
		return nil
	}

	currQ := q.req.Questions[q.currQuestion]
	t := q.com.Styles

	width := max(0, min(defaultDialogMaxWidth, area.Dx()-t.Dialog.View.GetHorizontalBorderSize()))
	height := max(0, min(defaultDialogHeight, area.Dy()-t.Dialog.View.GetVerticalBorderSize()))
	innerWidth := width - t.Dialog.View.GetHorizontalFrameSize()

	rc := NewRenderContext(t, width)
	rc.Title = "Question"

	// Navigation bar
	navBar := q.renderNavigationBar()
	rc.AddPart(navBar)

	// Question text
	questionText := t.Dialog.TitleAccent.Italic(true).Padding(1, 2).Render(currQ.Question)
	rc.AddPart(questionText)

	if q.isInTextInput {
		// Text input mode (unchanged from P1)
		prompt := t.Dialog.InputPrompt.Render("Your answer: ")
		input := t.Dialog.SelectedItem.Render(q.textInput + "|")
		rc.AddPart(prompt + input)
	} else if currQ.HasPreview() && area.Dx() >= 60 {
		// P2: Side-by-side preview mode
		q.renderPreviewLayout(rc, currQ, innerWidth, height)
	} else {
		// P1: Standard single-column layout
		q.list.SetSize(innerWidth, height-10)
		q.help.SetWidth(innerWidth)
		listView := t.Dialog.List.Height(q.list.Height()).Render(q.list.Render())
		rc.AddPart(listView)
		rc.Help = q.help.View(q)
	}

	view := rc.Render()
	DrawCenterCursor(scr, area, view, nil)
	return nil
}
```

- [ ] **Step 5: Add `renderPreviewLayout()` method**

Add this new method to `Questions`:

```go
const (
	previewLeftWidth = 30
	previewMinTotalWidth = 60
)

// renderPreviewLayout renders the side-by-side options + preview layout.
func (q *Questions) renderPreviewLayout(rc *RenderContext, currQ questions.Question, innerWidth, height int) {
	t := q.com.Styles

	rightWidth := innerWidth - previewLeftWidth - 2 // 2 for gap
	if rightWidth < 20 {
		// Not enough space, fall back to single column
		q.list.SetSize(innerWidth, height-10)
		listView := t.Dialog.List.Height(q.list.Height()).Render(q.list.Render())
		rc.AddPart(listView)
		return
	}

	// Render left panel: option list at constrained width
	q.list.SetSize(previewLeftWidth, height-10)
	leftView := q.list.Render()

	// Get preview content for focused option
	previewContent := ""
	if q.focusedIdx >= 0 && q.focusedIdx < len(currQ.Options) {
		previewContent = currQ.Options[q.focusedIdx].Preview
	}

	// Render right panel: preview box
	maxLines := height - 6 // account for nav bar, question text, borders
	rightView := renderPreviewBox(previewBoxConfig{
		content:  previewContent,
		width:    rightWidth,
		maxLines: maxLines,
		minWidth: 20,
		styles:   t,
	})

	// Join side-by-side
	rc.AddPart(joinSideBySide(leftView, rightView, previewLeftWidth, innerWidth))

	q.help.SetWidth(innerWidth)
	rc.Help = q.help.View(q)
}
```

- [ ] **Step 6: Add `joinSideBySide()` helper**

```go
// joinSideBySide renders two views side by side with a gap.
func joinSideBySide(left, right string, leftWidth, totalWidth int) string {
	leftLines := strings.Split(left, "\n")
	rightLines := strings.Split(right, "\n")

	maxLines := max(len(leftLines), len(rightLines))
	// Pad shorter side
	for len(leftLines) < maxLines {
		leftLines = append(leftLines, "")
	}
	for len(rightLines) < maxLines {
		rightLines = append(rightLines, "")
	}

	gap := "  "
	var result []string
	for i := 0; i < maxLines; i++ {
		// Left line padded to leftWidth
		leftPart := leftLines[i]
		leftPartWidth := lipgloss.Width(leftPart)
		if leftPartWidth < leftWidth {
			leftPart += strings.Repeat(" ", leftWidth-leftPartWidth)
		} else if leftPartWidth > leftWidth {
			leftPart = ansi.Truncate(leftPart, leftWidth, "")
		}
		result = append(result, leftPart+gap+rightLines[i])
	}
	return strings.Join(result, "\n")
}
```

- [ ] **Step 7: Verify compilation and run tests**

```bash
go build ./internal/ui/dialog/ && go test ./internal/ui/dialog/ -v
```

- [ ] **Step 8: Commit**

```bash
git add internal/ui/dialog/questions.go
git commit -m "feat(ui): add preview mode with side-by-side layout to questions dialog"
```

---

### Task 6: Update Tool Prompt for Preview (P2-c)

**Files:**
- Modify: `internal/agent/tools/ask_user_questions.md`

- [ ] **Step 1: Add preview documentation to tool prompt**

Update `internal/agent/tools/ask_user_questions.md` to:

```markdown
Use this tool when you need to ask the user questions during execution.

Usage notes:
- Users will always be able to select "Other" to provide custom text input
- Use multi_select: true to allow multiple answers
- If you recommend a specific option, make it the first option and append "(Recommended)" to the label
- Add a "preview" field to options with markdown content to show a side-by-side preview panel
- Preview is useful for code snippets, file contents, or detailed descriptions
- Keep preview content concise (excessive lines will be truncated)

Constraints:
- Ask 1-4 questions at a time
- Each question must have 2-4 options
- Question texts must be unique
- Option labels must be unique within each question
- Header must be 1-12 characters

When to use:
- Gather user preferences or requirements
- Clarify ambiguous instructions
- Get decisions on implementation choices
- Offer choices about direction to take

Plan mode:
- Use this tool to clarify requirements BEFORE finalizing your plan
- Do NOT ask "Is my plan ready?" or "Should I proceed?"

Preview examples:
- Show code snippets for different implementation approaches
- Display file content previews for configuration options
- Present API response examples for endpoint selection
```

- [ ] **Step 2: Verify build**

```bash
go build ./internal/agent/tools/
```

- [ ] **Step 3: Commit**

```bash
git add internal/agent/tools/ask_user_questions.md
git commit -m "docs(tools): update ask_user_questions prompt with preview feature notes"
```

---

### Task 7: Build & Smoke Test

- [ ] **Step 1: Full build**

```bash
go build ./...
```

Expected: No errors.

- [ ] **Step 2: Run all related tests**

```bash
go test ./internal/questions/ ./internal/ui/dialog/ ./internal/agent/tools/ -v
```

Expected: All PASS, no regressions.

- [ ] **Step 3: Run full test suite**

```bash
go test ./internal/... 2>&1 | tail -20
```

Expected: No new failures.

- [ ] **Step 4: Final commit if fixes needed**

```bash
git add -A && git commit -m "fix: resolve P2-a integration issues"
```

---

## Self-Review

**1. Spec coverage:**
- ✅ `Option.Preview` field — Task 1
- ✅ `Question.HasPreview()` — Task 1 + tested in Task 2
- ✅ PreviewBox component — Task 3 + tested in Task 4
- ✅ Side-by-side layout in dialog — Task 5
- ✅ Focused index tracking — Task 5
- ✅ Markdown rendering via glamour — Task 3
- ✅ Line truncation with scissors indicator — Task 3
- ✅ Narrow terminal fallback — Task 5
- ✅ Tool prompt update — Task 6

**2. Placeholder scan:** No TBD/TODO. All code is concrete.

**3. Type consistency:**
- `previewBoxConfig` used in Task 3 and referenced in Task 5
- `renderPreviewBox()` returns string, used in `renderPreviewLayout()`
- `joinSideBySide()` uses `lipgloss.Width()` and `ansi.Truncate()` consistently
- `focusedIdx int` used in HandleMsg and Draw
