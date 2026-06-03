# P2-b: Annotations Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add per-question notes via `n` key, returned as `Answer.Annotation` to the LLM.

**Architecture:** Reuse the existing `textinput.Model` (already used for "Other" input). Add a `isInNotesInput` state alongside `isInTextInput`. Notes text stored in `notesTexts map[int]string`. A small notes area rendered below the option list shows existing notes or a "press n to add notes" hint.

**Tech Stack:** Go, bubbles v2 textinput, lipgloss v2 styles

---

## File Structure

| File | Change |
|------|--------|
| `internal/questions/service.go` | Add `Annotation string` to `Answer` struct |
| `internal/ui/dialog/questions.go` | Add `n` key binding, `isInNotesInput` state, `notesTexts` map, `handleNotesInput()`, notes rendering |
| `internal/agent/tools/ask_user_questions.go` | Include annotation in `formatAnswersResponse` |
| `internal/agent/tools/ask_user_questions.md` | Add notes usage documentation |

---

### Task 1: Add Annotation field to Answer struct

**Files:**
- Modify: `internal/questions/service.go:39-43`

- [ ] **Step 1: Add the field**

In `internal/questions/service.go`, change the `Answer` struct to add `Annotation`:

```go
type Answer struct {
	QuestionText string `json:"question"`
	Selected     string `json:"selected"`
	IsOther      bool   `json:"is_other"`
	Annotation   string `json:"annotation,omitempty"`
}
```

- [ ] **Step 2: Build and test**

Run: `go build ./internal/questions/ && go test ./internal/questions/ -count=1 -v`
Expected: All existing tests pass (field is additive, backward compatible)

- [ ] **Step 3: Commit**

```bash
git add internal/questions/service.go
git commit -m "feat(questions): add Annotation field to Answer struct"
```

---

### Task 2: Add notes state and key binding to Questions dialog

**Files:**
- Modify: `internal/ui/dialog/questions.go:28-55` (struct + keyMap)
- Modify: `internal/ui/dialog/questions.go:57-101` (constructor)

- [ ] **Step 1: Add state fields and Notes key binding**

Add to `Questions` struct (after `focusedIdx` on line 38):

```go
	isInNotesInput bool
	notesTexts     map[int]string // map[questionIdx]notesText
	notesInput     textinput.Model
```

Add `Notes` to `questionsKeyMap` struct (after `Submit` on line 53):

```go
		Notes    key.Binding
```

- [ ] **Step 2: Initialize new fields in constructor**

In `NewQuestionsDialog`, add to the struct literal (after `textInput` on line 67):

```go
			notesTexts: make(map[int]string),
			notesInput: newNotesInput(com.Styles),
```

Add to `d.keyMap` initialization (after `Submit` on line 91):

```go
			Notes: key.NewBinding(
				key.WithKeys("n"),
				key.WithHelp("n", "notes"),
			),
```

- [ ] **Step 3: Add the newNotesInput constructor**

After `newTextInput` function (after line 407), add:

```go
// newNotesInput creates a textinput.Model configured for notes input.
func newNotesInput(sty *styles.Styles) textinput.Model {
	ti := textinput.New()
	ti.SetStyles(sty.TextInput)
	ti.Prompt = "Notes: "
	ti.CharLimit = 500
	ti.SetVirtualCursor(false)
	return ti
}
```

- [ ] **Step 4: Build**

Run: `go build ./internal/ui/dialog/`
Expected: Clean compile (handleNotesInput not used yet, that's OK)

- [ ] **Step 5: Commit**

```bash
git add internal/ui/dialog/questions.go
git commit -m "feat(ui): add notes state, key binding, and notesInput to Questions dialog"
```

---

### Task 3: Add notes input handler and wire into HandleMsg

**Files:**
- Modify: `internal/ui/dialog/questions.go` (HandleMsg + new handler)

- [ ] **Step 1: Wire Notes key into HandleMsg**

In `HandleMsg`, add a new case in the switch block (after the `q.keyMap.Submit` case, before `q.keyMap.Close`):

```go
			case key.Matches(msg, q.keyMap.Notes):
				if !q.isOnSubmitTab() && !q.isInTextInput {
					q.isInNotesInput = true
					// Pre-fill existing notes if any
					existing := q.notesTexts[q.currQuestion]
					q.notesInput.SetValue(existing)
					q.notesInput.Focus()
				}
```

Also update the top of `HandleMsg` to check `isInNotesInput` alongside `isInTextInput` (line 160):

```go
			if q.isInTextInput {
				return q.handleTextInput(msg)
			}
			if q.isInNotesInput {
				return q.handleNotesInput(msg)
			}
```

- [ ] **Step 2: Implement handleNotesInput**

Add after `handleTextInput` (after line 256):

```go
// handleNotesInput processes key events while the user is typing notes.
func (q *Questions) handleNotesInput(msg tea.KeyPressMsg) Action {
	switch {
	case key.Matches(msg, q.keyMap.Close):
		// Escape cancels notes input
		q.isInNotesInput = false
		q.notesInput.SetValue("")
		q.notesInput.Blur()
		return nil
	case msg.String() == "enter":
		// Save notes
		text := strings.TrimSpace(q.notesInput.Value())
		if text != "" {
			q.notesTexts[q.currQuestion] = text
		} else {
			delete(q.notesTexts, q.currQuestion)
		}
		q.isInNotesInput = false
		q.notesInput.SetValue("")
		q.notesInput.Blur()
		return nil
	default:
		var cmd tea.Cmd
		q.notesInput, cmd = q.notesInput.Update(msg)
		if cmd != nil {
			return ActionCmd{Cmd: cmd}
		}
		return nil
	}
}
```

- [ ] **Step 3: Build**

Run: `go build ./internal/ui/dialog/`
Expected: Clean compile

- [ ] **Step 4: Commit**

```bash
git add internal/ui/dialog/questions.go
git commit -m "feat(ui): add handleNotesInput and wire n key into HandleMsg"
```

---

### Task 4: Render notes area in Draw and include in buildSubmitAction

**Files:**
- Modify: `internal/ui/dialog/questions.go` (Draw, buildSubmitAction, ShortHelp)

- [ ] **Step 1: Add notes area to Draw**

In `Draw`, inside the `else` branch (after the option list / preview rendering, before the closing `}`), add notes area rendering:

```go
			// Notes area
			if q.isInNotesInput {
				q.notesInput.Focus()
				rc.AddPart(q.notesInput.View())
			} else if note := q.notesTexts[q.currQuestion]; note != "" {
				rc.AddPart(t.Dialog.SecondaryText.Padding(0, 2).Render("📝 " + note))
			} else if !q.isOnSubmitTab() {
				rc.AddPart(t.Dialog.SecondaryText.Padding(0, 2).Render("press n to add notes"))
			}
```

- [ ] **Step 2: Include annotation in buildSubmitAction**

In `buildSubmitAction`, after setting `res.Answers[questIdx] = answer` (line 339), add:

```go
			answer.Annotation = q.notesTexts[questIdx]
```

Wait — this needs to be BEFORE the assignment. Move it: in the loop body, just before `res.Answers[questIdx] = answer`, add:

```go
			answer.Annotation = q.notesTexts[questIdx]
```

- [ ] **Step 3: Add Notes to ShortHelp**

In `ShortHelp`, add `q.keyMap.Notes` after the Submit binding:

```go
		h = append(h, q.keyMap.Notes)
```

- [ ] **Step 4: Build and test**

Run: `go build ./... && go test ./internal/ui/dialog/ ./internal/questions/ -count=1`
Expected: All pass

- [ ] **Step 5: Commit**

```bash
git add internal/ui/dialog/questions.go
git commit -m "feat(ui): render notes area in Draw and include Annotation in buildSubmitAction"
```

---

### Task 5: Include annotation in tool response formatting

**Files:**
- Modify: `internal/agent/tools/ask_user_questions.go:54-66`

- [ ] **Step 1: Update formatAnswersResponse**

Change the function to include annotation:

```go
func formatAnswersResponse(resp questions.QuestionsResponse) fantasy.ToolResponse {
	parts := make([]string, 0, len(resp.Answers))
	for _, ans := range resp.Answers {
		selected := ans.Selected
		if ans.IsOther && selected != "" {
			selected = fmt.Sprintf("%s (user input)", selected)
		}
		part := fmt.Sprintf("%q=%q", ans.QuestionText, selected)
		if ans.Annotation != "" {
			part += fmt.Sprintf(" (notes: %q)", ans.Annotation)
		}
		parts = append(parts, part)
	}
	msg := fmt.Sprintf("User answered your questions: %s. You can continue with the user's answers in mind.",
		strings.Join(parts, ", "))
	return fantasy.NewTextResponse(msg)
}
```

- [ ] **Step 2: Build and test**

Run: `go build ./... && go test ./internal/agent/tools/ -count=1 -run TestAskUser -timeout 30s`
Expected: All pass (existing tests don't check annotation, backward compatible)

- [ ] **Step 3: Commit**

```bash
git add internal/agent/tools/ask_user_questions.go
git commit -m "feat(tools): include annotation in formatAnswersResponse"
```

---

### Task 6: Update tool prompt documentation

**Files:**
- Modify: `internal/agent/tools/ask_user_questions.md`

- [ ] **Step 1: Add notes documentation**

Append to the end of the file:

```markdown
Annotations:
- Users can press "n" to add notes to any question
- Notes are returned with the answer and can provide additional context
- Example: user selects "OAuth" and adds note "use RS256 signing"
```

- [ ] **Step 2: Build**

Run: `go build ./...`
Expected: Clean compile (embedded file update)

- [ ] **Step 3: Commit**

```bash
git add internal/agent/tools/ask_user_questions.md
git commit -m "docs(tools): add annotation documentation to ask_user_questions prompt"
```

---

## Verification

After all tasks complete:

1. `go build ./...` — clean compile
2. `go test ./internal/ui/dialog/ ./internal/questions/ -count=1` — all pass
3. Manual test scenarios:
   - Press `n` → notes input appears → type text → Enter saves → notes shown below options
   - Press `n` → type text → Esc cancels → no notes saved
   - Press `n` → type text → Enter → press `n` again → existing text pre-filled
   - Submit with notes → response includes `(notes: "...")` in tool output
   - Navigate between questions → each question has independent notes
