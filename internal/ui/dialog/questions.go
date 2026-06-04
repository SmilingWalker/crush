package dialog

import (
	"fmt"
	"log/slog"
	"strings"

	"charm.land/bubbles/v2/help"
	"charm.land/bubbles/v2/key"
	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/crush/internal/questions"
	"github.com/charmbracelet/crush/internal/ui/common"
	"github.com/charmbracelet/crush/internal/ui/styles"
	uv "github.com/charmbracelet/ultraviolet"
)

const QuestionsID = "questions"

// ActionQuestionsResponse is sent when the user completes an ask_user_questions dialog.
type ActionQuestionsResponse struct {
	Response questions.QuestionsResponse
}

// Questions is a dialog that presents multiple-choice questions to the user.
type Questions struct {
	com *common.Common
	req questions.QuestionsRequest

	// State
	currQuestion  int                    // 0..len(questions)-1 = question tab; len(questions) = Submit tab
	selectedOpts  map[int]map[int]bool   // map[questionIdx]map[optionIdx]bool
	otherTexts    map[int]string         // map[questionIdx]otherText
	isInTextInput bool
	textInput     textinput.Model
	focusedIdx     int // index of the option being previewed
	isInNotesInput bool
	notesTexts     map[int]string // map[questionIdx]notesText
	notesInput     textinput.Model

	// Keyboard
	keyMap questionsKeyMap

	// UI
	list *questionOptionsList
	help help.Model
}

type questionsKeyMap struct {
	Up       key.Binding
	Down     key.Binding
	Next     key.Binding
	Previous key.Binding
	Submit   key.Binding
	Notes    key.Binding
	Close    key.Binding
}

// NewQuestionsDialog creates a new Questions dialog.
func NewQuestionsDialog(com *common.Common, req questions.QuestionsRequest) *Questions {
	d := &Questions{
		com:          com,
		req:          req,
		currQuestion: 0,
		selectedOpts: make(map[int]map[int]bool),
		otherTexts:   make(map[int]string),
		list:         newQuestionOptionsList(com.Styles),
		help:         help.New(),
		textInput:    newTextInput(com.Styles),
		notesTexts:  make(map[int]string),
		notesInput:  newNotesInput(com.Styles),
	}

	d.keyMap = questionsKeyMap{
		Up: key.NewBinding(
			key.WithKeys("up", "ctrl+p"),
			key.WithHelp("up", "up"),
		),
		Down: key.NewBinding(
			key.WithKeys("down", "ctrl+n"),
			key.WithHelp("down", "down"),
		),
		Previous: key.NewBinding(
			key.WithKeys("left", "shift+tab"),
			key.WithHelp("←", "prev"),
		),
		Next: key.NewBinding(
			key.WithKeys("right", "tab"),
			key.WithHelp("→", "next"),
		),
		Submit: key.NewBinding(
			key.WithKeys("enter"),
			key.WithHelp("enter", "confirm"),
		),
		Close: CloseKey,
		Notes: key.NewBinding(
			key.WithKeys("n"),
			key.WithHelp("n", "notes"),
		),
	}

	d.list.Focus()
	d.initList()
	d.list.SetSelected(0)

	d.help.Styles = com.Styles.DialogHelpStyles()

	return d
}

// ID implements Dialog.
func (q *Questions) ID() string {
	return QuestionsID
}

// isOnSubmitTab returns true if the Submit tab is active.
func (q *Questions) isOnSubmitTab() bool {
	return q.currQuestion >= len(q.req.Questions)
}

// isQuestionAnswered returns true if the question at the given index has a real answer.
func (q *Questions) isQuestionAnswered(idx int) bool {
	if text, ok := q.otherTexts[idx]; ok && text != "" {
		return true
	}
	for _, sel := range q.selectedOpts[idx] {
		if sel {
			return true
		}
	}
	return false
}

func (q *Questions) initList() {
	if q.isOnSubmitTab() || len(q.req.Questions) == 0 {
		return
	}
	if q.selectedOpts[q.currQuestion] == nil {
		q.selectedOpts[q.currQuestion] = make(map[int]bool)
	}
	q.focusedIdx = 0
	for optIdx, sel := range q.selectedOpts[q.currQuestion] {
		if sel {
			q.focusedIdx = optIdx
			break
		}
	}
	q.refreshList()
	q.list.SelectFirst()
	q.focusedIdx = q.list.Selected()
}

func (q *Questions) refreshList() {
	if q.isOnSubmitTab() {
		return
	}
	q.list.SetQuestion(
		q.req.Questions[q.currQuestion],
		q.selectedOpts[q.currQuestion],
		q.otherTexts[q.currQuestion],
	)
}

// HandleMsg implements Dialog.
func (q *Questions) HandleMsg(msg tea.Msg) Action {
	switch msg := msg.(type) {
	case tea.KeyPressMsg:
		// If in text input mode (Other selected)
		if q.isInTextInput {
			return q.handleTextInput(msg)
		}

		if q.isInNotesInput {
			return q.handleNotesInput(msg)
		}

		// Normal option navigation
		switch {
		case key.Matches(msg, q.keyMap.Up):
			if !q.isOnSubmitTab() {
				q.list.Focus()
				if q.list.IsSelectedFirst() {
					q.list.SelectLast()
				} else {
					q.list.SelectPrev()
				}
				q.list.ScrollToSelected()
				q.focusedIdx = q.list.Selected()
			}
		case key.Matches(msg, q.keyMap.Down):
			if !q.isOnSubmitTab() {
				q.list.Focus()
				if q.list.IsSelectedLast() {
					q.list.SelectFirst()
				} else {
					q.list.SelectNext()
				}
				q.list.ScrollToSelected()
				q.focusedIdx = q.list.Selected()
			}
		case key.Matches(msg, q.keyMap.Previous):
			if q.currQuestion > 0 {
				q.currQuestion--
				q.initList()
			}
		case key.Matches(msg, q.keyMap.Next):
			if q.currQuestion < len(q.req.Questions) {
				q.currQuestion++
				if !q.isOnSubmitTab() {
					q.initList()
				}
			}
		case key.Matches(msg, q.keyMap.Submit):
			return q.handleSubmit()
		case key.Matches(msg, q.keyMap.Notes):
			if !q.isOnSubmitTab() && !q.isInTextInput {
				q.isInNotesInput = true
				existing := q.notesTexts[q.currQuestion]
				q.notesInput.SetValue(existing)
				q.notesInput.Focus()
			}
		case key.Matches(msg, q.keyMap.Close):
			slog.Info("QuestionsDialog rejected by user")
			return ActionQuestionsResponse{
				Response: questions.QuestionsResponse{
					RequestID: q.req.ID,
					Rejected:  true,
				},
			}
		}
	}
	return nil
}

// handleTextInput processes key events while the user is typing an "Other" answer.
func (q *Questions) handleTextInput(msg tea.KeyPressMsg) Action {
	switch {
	case key.Matches(msg, q.keyMap.Close):
		// Escape exits text input
		q.isInTextInput = false
		q.textInput.SetValue("")
		q.textInput.Blur()
		q.refreshList()
		return nil
	case msg.String() == "enter":
		// Submit the text as the Other answer
		text := strings.TrimSpace(q.textInput.Value())
		if text != "" {
			currQ := q.req.Questions[q.currQuestion]
			otherIdx := len(currQ.Options)
			q.selectedOpts[q.currQuestion] = map[int]bool{otherIdx: true}
			q.otherTexts[q.currQuestion] = text
		}
		q.isInTextInput = false
		q.textInput.SetValue("")
		q.textInput.Blur()
		// Auto-advance if single select
		if !q.req.Questions[q.currQuestion].MultiSelect {
			q.currQuestion++
			if !q.isOnSubmitTab() {
				q.initList()
			}
		}
		return nil
	default:
		// Delegate all other keys to the textinput component.
		// CRITICAL: textinput.Update returns a new model (value type).
		// We must capture the return value, otherwise key events are lost.
		var cmd tea.Cmd
		q.textInput, cmd = q.textInput.Update(msg)
		if cmd != nil {
			return ActionCmd{Cmd: cmd}
		}
		return nil
	}
}

// handleNotesInput processes key events while the user is typing notes/annotations.
func (q *Questions) handleNotesInput(msg tea.KeyPressMsg) Action {
	switch {
	case key.Matches(msg, q.keyMap.Close):
		q.isInNotesInput = false
		q.notesInput.SetValue("")
		q.notesInput.Blur()
		return nil
	case msg.String() == "enter":
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

// handleSubmit processes enter key: the universal action.
//   - Submit tab → submit all answers
//   - Other item → enter text input mode
//   - Single-select: select focused option + auto-advance/submit
//   - Multi-select: toggle focused option
func (q *Questions) handleSubmit() Action {
	// Submit tab: submit all answers
	if q.isOnSubmitTab() {
		return q.buildSubmitAction()
	}

	currQ := q.req.Questions[q.currQuestion]

	// Identify the focused item via the list
	item := q.list.SelectedItem()
	if item == nil {
		return nil
	}
	optItem, ok := item.(*questionOptionsListItem)
	if !ok {
		return nil
	}

	// Other item: enter text input mode
	if optItem.isOther {
		q.isInTextInput = true
		q.textInput.SetValue("")
		q.textInput.Focus()
		return nil
	}

	if currQ.MultiSelect {
		// Multi-select: toggle the option checkbox
		q.selectedOpts[q.currQuestion][optItem.index] = !q.selectedOpts[q.currQuestion][optItem.index]
		q.refreshList()
	} else {
		// Single-select: select the focused option and advance
		q.selectedOpts[q.currQuestion] = map[int]bool{optItem.index: true}
		delete(q.otherTexts, q.currQuestion)
		q.refreshList()
		// Advance to next question or Submit tab (never auto-submit)
		q.currQuestion++
		if !q.isOnSubmitTab() {
			q.initList()
		}
	}
	return nil
}

// buildSubmitAction assembles the final response from all selected options.
func (q *Questions) buildSubmitAction() Action {
	slog.Info("QuestionsDialog submitting answers")
	res := questions.NewQuestionsResponse(&q.req)
	for questIdx, quest := range q.req.Questions {
		answer := questions.Answer{
			QuestionText: quest.Question,
		}

		// Check if Other was selected for this question
		if otherText, ok := q.otherTexts[questIdx]; ok && otherText != "" {
			answer.Selected = otherText
			answer.IsOther = true
		} else if quest.MultiSelect {
			// Multi-select: comma-separated labels
			var selected []string
			for optIdx, sel := range q.selectedOpts[questIdx] {
				if sel && optIdx < len(quest.Options) {
					selected = append(selected, quest.Options[optIdx].Label)
				}
			}
			answer.Selected = strings.Join(selected, ",")
		} else {
			// Single select: find the selected option
			for optIdx, sel := range q.selectedOpts[questIdx] {
				if sel && optIdx < len(quest.Options) {
					answer.Selected = quest.Options[optIdx].Label
					break
				}
			}
		}

		answer.Annotation = q.notesTexts[questIdx]

		res.Answers[questIdx] = answer
	}
	return ActionQuestionsResponse{Response: res}
}

// Draw implements Dialog.
func (q *Questions) Draw(scr uv.Screen, area uv.Rectangle) *tea.Cursor {
	if len(q.req.Questions) == 0 {
		return nil
	}

	t := q.com.Styles

	width := max(0, min(defaultDialogMaxWidth, area.Dx()-t.Dialog.View.GetHorizontalBorderSize()))
	height := max(0, min(defaultDialogHeight, area.Dy()-t.Dialog.View.GetVerticalBorderSize()))
	innerWidth := width - t.Dialog.View.GetHorizontalFrameSize()

	q.help.SetWidth(innerWidth)

	rc := NewRenderContext(t, width)

	// Title
	rc.Title = "Question"

	// Navigation bar with question tabs + Submit tab
	navBar := q.renderNavigationBar(innerWidth)
	rc.AddPart(navBar)

	if q.isOnSubmitTab() {
		// Submit tab: show answer summary
		rc.AddPart(q.renderSubmitView(innerWidth))
	} else {
		currQ := q.req.Questions[q.currQuestion]

		// Question text
		questionText := t.Dialog.TitleAccent.Padding(1, 2).Render(currQ.Question)
		rc.AddPart(questionText)

		// Content area
		if q.isInTextInput {
			q.textInput.Focus()
			rc.AddPart(q.textInput.View())
		} else if currQ.HasPreview() {
			q.renderPreviewLayout(rc, currQ, innerWidth, height)
		} else {
			q.list.SetSize(innerWidth, max(1, height-10))
			listView := t.Dialog.List.Height(q.list.Height()).Render(q.list.Render())
			rc.AddPart(listView)
		}

		// Notes area
		if q.isInNotesInput {
			q.notesInput.Focus()
			rc.AddPart(q.notesInput.View())
		} else if note := q.notesTexts[q.currQuestion]; note != "" {
			rc.AddPart(t.Dialog.SecondaryText.Padding(0, 2).Render("📝 " + note))
		} else {
			rc.AddPart(t.Dialog.SecondaryText.Padding(0, 2).Render("press n to add notes"))
		}
	}

	// Help
	rc.Help = q.help.View(q)

	view := rc.Render()
	DrawCenterCursor(scr, area, view, nil)

	return nil
}

// newTextInput creates a textinput.Model configured for the "Other" answer input.
func newTextInput(sty *styles.Styles) textinput.Model {
	ti := textinput.New()
	ti.SetStyles(sty.TextInput)
	ti.Prompt = "Your answer: "
	ti.CharLimit = 500
	ti.SetVirtualCursor(false)
	return ti
}

// newNotesInput creates a textinput.Model configured for the notes annotation input.
func newNotesInput(sty *styles.Styles) textinput.Model {
	ti := textinput.New()
	ti.SetStyles(sty.TextInput)
	ti.Prompt = "Notes: "
	ti.CharLimit = 500
	ti.SetVirtualCursor(false)
	return ti
}

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
	// Preview box: up to previewContentLines + 2 borders + 1 truncation line = 8 max
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

// renderNavigationBar renders the question tabs + Submit tab.
// Layout: ← [☐] Q1 [☑] Q2 [✓ Submit] →
// Modelled after Claude Code's QuestionNavigationBar.
func (q *Questions) renderNavigationBar(innerWidth int) string {
	t := q.com.Styles
	var parts []string

	// Left arrow
	if q.currQuestion <= 0 {
		parts = append(parts, t.Dialog.SecondaryText.Render("← "))
	} else {
		parts = append(parts, "← ")
	}

	// Question tabs
	for i, quest := range q.req.Questions {
		header := quest.Header
		if header == "" {
			header = fmt.Sprintf("Q%d", i+1)
		}
		answered := q.isQuestionAnswered(i)
		checkbox := "☐"
		if answered {
			checkbox = "☑"
		}
		tabText := fmt.Sprintf(" %s %s ", checkbox, header)
		if i == q.currQuestion {
			parts = append(parts, t.Dialog.SelectedItem.Render(tabText))
		} else {
			parts = append(parts, t.Dialog.NormalItem.Render(tabText))
		}
	}

	// Submit tab — visual state depends on whether all questions are answered
	allAnswered := true
	for i := range q.req.Questions {
		if !q.isQuestionAnswered(i) {
			allAnswered = false
			break
		}
	}
	submitIndicator := "…"
	if allAnswered {
		submitIndicator = "✓"
	}
	submitText := fmt.Sprintf(" %s Submit ", submitIndicator)
	if q.isOnSubmitTab() {
		parts = append(parts, t.Dialog.SelectedItem.Bold(true).Render(submitText))
	} else if allAnswered {
		parts = append(parts, t.Dialog.SelectedItem.Render(submitText))
	} else {
		parts = append(parts, t.Dialog.NormalItem.Render(submitText))
	}

	// Right arrow
	if q.isOnSubmitTab() {
		parts = append(parts, t.Dialog.SecondaryText.Render(" →"))
	} else {
		parts = append(parts, " →")
	}

	return strings.Join(parts, "")
}

// renderSubmitView renders the content when the Submit tab is active.
// Shows each question with its selected answer.
func (q *Questions) renderSubmitView(innerWidth int) string {
	t := q.com.Styles
	var lines []string

	for i, quest := range q.req.Questions {
		answered := q.isQuestionAnswered(i)
		header := quest.Header
		if header == "" {
			header = fmt.Sprintf("Q%d", i+1)
		}
		status := "☐"
		if answered {
			status = "☑"
		}

		// Resolve the actual answer text
		answerText := ""
		if otherText, ok := q.otherTexts[i]; ok && otherText != "" {
			answerText = otherText
		} else if quest.MultiSelect {
			var selected []string
			for optIdx, sel := range q.selectedOpts[i] {
				if sel && optIdx < len(quest.Options) {
					selected = append(selected, quest.Options[optIdx].Label)
				}
			}
			answerText = strings.Join(selected, ", ")
		} else {
			for optIdx, sel := range q.selectedOpts[i] {
				if sel && optIdx < len(quest.Options) {
					answerText = quest.Options[optIdx].Label
					break
				}
			}
		}

		line := fmt.Sprintf(" %s %s", status, header)
		if answerText != "" {
			line = fmt.Sprintf(" %s %s → %s", status, header, answerText)
		}
		if answered {
			lines = append(lines, t.Dialog.NormalItem.Render(line))
		} else {
			lines = append(lines, t.Dialog.SecondaryText.Render(line))
		}
	}

	// Submit prompt — subtle, not a giant button
	lines = append(lines, "")
	allAnswered := true
	for i := range q.req.Questions {
		if !q.isQuestionAnswered(i) {
			allAnswered = false
			break
		}
	}
	if allAnswered {
		lines = append(lines, t.Dialog.SelectedItem.Render(" Press Enter to submit "))
	} else {
		lines = append(lines, t.Dialog.SecondaryText.Render(" Press Enter to submit "))
	}

	return strings.Join(lines, "\n")
}

// ShortHelp returns the short help view.
func (q *Questions) ShortHelp() []key.Binding {
	h := []key.Binding{
		q.keyMap.Up,
		q.keyMap.Down,
		q.keyMap.Submit,
	}
	if len(q.req.Questions) > 1 || true { // always show nav for Submit tab
		h = append(h, q.keyMap.Previous, q.keyMap.Next)
	}
	h = append(h, q.keyMap.Notes)
	h = append(h, q.keyMap.Close)
	return h
}

// FullHelp returns the full help view.
func (q *Questions) FullHelp() [][]key.Binding {
	return [][]key.Binding{q.ShortHelp()}
}

// Compile-time assertion.
var _ Dialog = (*Questions)(nil)
