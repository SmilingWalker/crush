package dialog

import (
	"fmt"
	"log/slog"
	"strings"

	"charm.land/bubbles/v2/help"
	"charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/crush/internal/questions"
	"github.com/charmbracelet/crush/internal/ui/common"
	"github.com/charmbracelet/x/ansi"
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
	currQuestion  int
	selectedOpts  map[int]map[int]bool // map[questionIdx]map[optionIdx]bool
	otherTexts    map[int]string       // map[questionIdx]otherText
	isInTextInput bool
	textInput     string
	focusedIdx    int // P2: index of the option being previewed

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
	Select   key.Binding
	Submit   key.Binding
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
			key.WithHelp("left", "prev question"),
		),
		Next: key.NewBinding(
			key.WithKeys("right", "tab"),
			key.WithHelp("right", "next question"),
		),
		Select: key.NewBinding(
			key.WithKeys(" "),
			key.WithHelp("space", "toggle"),
		),
		Submit: key.NewBinding(
			key.WithKeys("enter"),
			key.WithHelp("enter", "confirm"),
		),
		Close: CloseKey,
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

func (q *Questions) refreshList() {
	q.list.SetQuestion(
		q.req.Questions[q.currQuestion],
		q.selectedOpts[q.currQuestion],
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

		// Normal option navigation
		switch {
		case key.Matches(msg, q.keyMap.Up):
			q.list.Focus()
			if q.list.IsSelectedFirst() {
				q.list.SelectLast()
			} else {
				q.list.SelectPrev()
			}
			q.list.ScrollToSelected()
		q.focusedIdx = q.list.Selected()
		case key.Matches(msg, q.keyMap.Down):
			q.list.Focus()
			if q.list.IsSelectedLast() {
				q.list.SelectFirst()
			} else {
				q.list.SelectNext()
			}
			q.list.ScrollToSelected()
		q.focusedIdx = q.list.Selected()
		case key.Matches(msg, q.keyMap.Previous):
			if q.currQuestion > 0 {
				q.currQuestion--
				q.initList()
			}
		case key.Matches(msg, q.keyMap.Next):
			if q.currQuestion < len(q.req.Questions)-1 {
				q.currQuestion++
				q.initList()
			}
		case key.Matches(msg, q.keyMap.Select):
			return q.handleToggle()
		case key.Matches(msg, q.keyMap.Submit):
			return q.handleSubmit()
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
		q.refreshList()
		return nil
	case msg.String() == "enter":
		// Submit the text as the Other answer
		text := strings.TrimSpace(q.textInput)
		if text != "" {
			currQ := q.req.Questions[q.currQuestion]
			otherIdx := len(currQ.Options) // Other is the last item
			q.selectedOpts[q.currQuestion] = map[int]bool{otherIdx: true}
			q.otherTexts[q.currQuestion] = text
		}
		q.isInTextInput = false
		// Auto-advance if single select
		if !q.req.Questions[q.currQuestion].MultiSelect {
			if q.currQuestion < len(q.req.Questions)-1 {
				q.currQuestion++
				q.initList()
			} else {
				return q.buildSubmitAction()
			}
		}
		return nil
	case msg.String() == "backspace":
		if len(q.textInput) > 0 {
			q.textInput = q.textInput[:len(q.textInput)-1]
		}
		return nil
	default:
		// Append printable character to text input
		ch := msg.String()
		if len(ch) == 1 && ch >= " " {
			q.textInput += ch
		}
		return nil
	}
}

// handleToggle processes space key: toggle option in multi-select, or enter Other input.
func (q *Questions) handleToggle() Action {
	currQ := q.req.Questions[q.currQuestion]
	idx := q.list.Selected()
	if idx < 0 {
		return nil
	}

	// Check if "Other" was selected
	if idx == len(currQ.Options) {
		q.isInTextInput = true
		q.textInput = ""
		return nil
	}

	if currQ.MultiSelect {
		// Multi select: toggle the option
		q.selectedOpts[q.currQuestion][idx] = !q.selectedOpts[q.currQuestion][idx]
		q.refreshList()
	}
	// Single select: space does nothing (use Enter to confirm)
	return nil
}

// handleSubmit processes enter key: confirm selection and advance or submit.
func (q *Questions) handleSubmit() Action {
	currQ := q.req.Questions[q.currQuestion]
	idx := q.list.Selected()

	if !currQ.MultiSelect {
		// Single select: select the focused option and advance
		if idx >= 0 && idx < len(currQ.Options) {
			q.selectedOpts[q.currQuestion] = map[int]bool{idx: true}
			delete(q.otherTexts, q.currQuestion)
			q.refreshList()
		}
		// Advance to next question or submit
		if q.currQuestion < len(q.req.Questions)-1 {
			q.currQuestion++
			q.initList()
		} else {
			return q.buildSubmitAction()
		}
	} else {
		// Multi select: advance to next question or submit all
		if q.currQuestion < len(q.req.Questions)-1 {
			q.currQuestion++
			q.initList()
		} else {
			return q.buildSubmitAction()
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

		res.Answers[questIdx] = answer
	}
	return ActionQuestionsResponse{Response: res}
}

// Draw implements Dialog.
func (q *Questions) Draw(scr uv.Screen, area uv.Rectangle) *tea.Cursor {
	if len(q.req.Questions) == 0 {
		return nil
	}

	currQ := q.req.Questions[q.currQuestion]
	t := q.com.Styles

	width := max(0, min(defaultDialogMaxWidth, area.Dx()-t.Dialog.View.GetHorizontalBorderSize()))
	height := max(0, min(defaultDialogHeight, area.Dy()-t.Dialog.View.GetVerticalBorderSize()))
	innerWidth := width - t.Dialog.View.GetHorizontalFrameSize()

	q.help.SetWidth(innerWidth)

	rc := NewRenderContext(t, width)

	// Title
	rc.Title = "Question"

	// Question navigation bar (tabs)
	navBar := q.renderNavigationBar()
	rc.AddPart(navBar)

	// Question text
	questionText := t.Dialog.TitleAccent.Italic(true).Padding(1, 2).Render(currQ.Question)
	rc.AddPart(questionText)

	// Content area
	if q.isInTextInput {
		// Text input mode
		prompt := t.Dialog.InputPrompt.Render("Your answer: ")
		input := t.Dialog.SelectedItem.Render(q.textInput + "|")
		rc.AddPart(prompt + input)
	} else if currQ.HasPreview() && area.Dx() >= previewMinTotalWidth {
		// P2: Side-by-side preview mode
		q.renderPreviewLayout(rc, currQ, innerWidth, height)
	} else {
		// Standard single-column layout
		q.list.SetSize(innerWidth, max(1, height-10))
		listView := t.Dialog.List.Height(q.list.Height()).Render(q.list.Render())
		rc.AddPart(listView)
	}

	// Help
	rc.Help = q.help.View(q)

	view := rc.Render()
	DrawCenterCursor(scr, area, view, nil)

	return nil
}

const (
	previewLeftWidth    = 30
	previewMinTotalWidth = 60
)

// renderPreviewLayout renders the side-by-side options + preview layout.
func (q *Questions) renderPreviewLayout(rc *RenderContext, currQ questions.Question, innerWidth, height int) {
	t := q.com.Styles

	rightWidth := innerWidth - previewLeftWidth - 2 // 2 for gap
	if rightWidth < 20 {
		// Not enough space, fall back to single column
		q.list.SetSize(innerWidth, max(1, height-10))
		listView := t.Dialog.List.Height(q.list.Height()).Render(q.list.Render())
		rc.AddPart(listView)
		return
	}

	// Render left panel: option list at constrained width
	q.list.SetSize(previewLeftWidth, max(1, height-10))
	leftView := q.list.Render()

	// Get preview content for focused option
	previewContent := ""
	if q.focusedIdx >= 0 && q.focusedIdx < len(currQ.Options) {
		previewContent = currQ.Options[q.focusedIdx].Preview
	}

	// Render right panel: preview box
	maxLines := max(1, height-6) // account for nav bar, question text, borders
	rightView := renderPreviewBox(previewBoxConfig{
		content:  previewContent,
		width:    rightWidth,
		maxLines: maxLines,
		minWidth: 20,
		styles:   t,
	})

	// Join side-by-side
	rc.AddPart(joinSideBySide(leftView, rightView, previewLeftWidth, innerWidth))
}

// joinSideBySide renders two views side by side with a gap.
func joinSideBySide(left, right string, leftWidth, totalWidth int) string {
	leftLines := strings.Split(left, "\n")
	rightLines := strings.Split(right, "\n")

	maxLineCount := max(len(leftLines), len(rightLines))
	// Pad shorter side
	for len(leftLines) < maxLineCount {
		leftLines = append(leftLines, "")
	}
	for len(rightLines) < maxLineCount {
		rightLines = append(rightLines, "")
	}

	gap := "  "
	var result []string
	for i := 0; i < maxLineCount; i++ {
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

// renderNavigationBar renders the question tabs.
func (q *Questions) renderNavigationBar() string {
	t := q.com.Styles
	var tabs []string
	for i, quest := range q.req.Questions {
		header := quest.Header
		if header == "" {
			header = fmt.Sprintf("Q%d", i+1)
		}
		_, answered := q.selectedOpts[i]
		checkbox := "[ ]"
		if answered {
			checkbox = "[x]"
		}
		if i == q.currQuestion {
			tabs = append(tabs, t.Dialog.SelectedItem.Render(fmt.Sprintf(" %s %s ", checkbox, header)))
		} else {
			tabs = append(tabs, fmt.Sprintf(" %s %s ", checkbox, header))
		}
	}
	return strings.Join(tabs, " ")
}

// ShortHelp returns the short help view.
func (q *Questions) ShortHelp() []key.Binding {
	h := []key.Binding{
		q.keyMap.Up,
		q.keyMap.Down,
		q.keyMap.Submit,
	}
	if len(q.req.Questions) > 1 {
		h = append(h, q.keyMap.Previous, q.keyMap.Next)
	}
	h = append(h, q.keyMap.Close)
	return h
}

// FullHelp returns the full help view.
func (q *Questions) FullHelp() [][]key.Binding {
	return [][]key.Binding{q.ShortHelp()}
}

// Compile-time assertion.
var _ Dialog = (*Questions)(nil)
