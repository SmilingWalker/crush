package dialog

import (
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/crush/internal/questions"
	"github.com/charmbracelet/crush/internal/ui/list"
	"github.com/charmbracelet/crush/internal/ui/styles"
	"github.com/charmbracelet/x/ansi"
)

// questionOptionsList is a list of options for a question.
type questionOptionsList struct {
	*list.List
	t *styles.Styles
}

// newQuestionOptionsList creates a new list of options for a question.
func newQuestionOptionsList(sty *styles.Styles) *questionOptionsList {
	l := &questionOptionsList{
		List: list.NewList(),
		t:    sty,
	}
	l.RegisterRenderCallback(list.FocusedRenderCallback(l.List))
	return l
}

// SetQuestion sets the question's options in the list.
// otherText is the user's custom answer for "Other"; when non-empty the Other
// item shows the entered text as its label with a check indicator.
func (l *questionOptionsList) SetQuestion(q questions.Question, selOpts map[int]bool, otherText string) {
	var items []list.Item
	for i, opt := range q.Options {
		items = append(items, &questionOptionsListItem{
			Versioned:    list.NewVersioned(),
			parent:       l,
			opt:          opt,
			selected:     selOpts[i],
			index:        i,
			isMultiSelect: q.MultiSelect,
		})
	}
	// Add "Other..." option — if user already entered text, show it as label
	otherLabel := "Other..."
	otherDesc := "Provide a custom answer"
	otherSelected := selOpts[len(q.Options)]
	if otherText != "" {
		otherLabel = otherText
		otherDesc = ""
		otherSelected = true
	}
	items = append(items, &questionOptionsListItem{
		Versioned:    list.NewVersioned(),
		parent:       l,
		opt:          questions.Option{Label: otherLabel, Description: otherDesc},
		selected:     otherSelected,
		index:        len(q.Options),
		isOther:      true,
		isMultiSelect: q.MultiSelect,
	})
	l.SetItems(items...)
}

// questionOptionsListItem is a list item for a question's option.
type questionOptionsListItem struct {
	*list.Versioned
	parent       *questionOptionsList
	opt          questions.Option
	selected     bool
	focused      bool
	index        int
	isOther      bool
	isSubmit     bool
	isMultiSelect bool
}

var _ list.Item = &questionOptionsListItem{}
var _ list.Focusable = &questionOptionsListItem{}

// Finished implements list.Item. Option items are render-stable
// outside of explicit SetFocused / selection changes.
func (i *questionOptionsListItem) Finished() bool {
	return true
}

// SetFocused implements list.Focusable.
func (i *questionOptionsListItem) SetFocused(focused bool) {
	if i.focused == focused {
		return
	}
	i.focused = focused
	if i.Versioned != nil {
		i.Bump()
	}
}

// SetSelected updates the selected state and bumps the version.
func (i *questionOptionsListItem) SetSelected(selected bool) {
	if i.selected == selected {
		return
	}
	i.selected = selected
	if i.Versioned != nil {
		i.Bump()
	}
}

// Render implements list.Item.
// Layout: two lines — line 1 is indicator + label (highlighted when focused),
// line 2 is description (gray, indented). Follows Crush's commands_item pattern.
func (i *questionOptionsListItem) Render(width int) string {
	t := i.parent.t

	// Indicator: checkbox for multi-select, radio for single-select
	var indicator string
	if i.isMultiSelect {
		if i.selected {
			indicator = "☑ "
		} else {
			indicator = "☐ "
		}
	} else {
		if i.selected {
			indicator = t.Radio.On.Padding(0, 1, 0, 0).Render()
		} else {
			indicator = t.Radio.Off.Padding(0, 1, 0, 0).Render()
		}
	}

	// Style based on focus
	style := t.Dialog.NormalItem
	if i.focused {
		style = t.Dialog.SelectedItem
	}

	// Line 1: indicator + label — truncate label to fit width
	label := i.opt.Label
	availWidth := width - lipgloss.Width(indicator)
	if availWidth < 4 {
		availWidth = 4
	}
	label = ansi.Truncate(label, availWidth, "…")
	labelPart := indicator + label

	// Pad line 1 to full width so highlight background covers entire row
	labelPartWidth := lipgloss.Width(labelPart)
	gap := strings.Repeat(" ", max(0, width-labelPartWidth))
	line1 := style.Render(labelPart + gap)

	// Line 2: description in gray, indented by indicator width
	if len(i.opt.Description) > 0 {
		descStyle := t.Dialog.SecondaryText
		if i.focused {
			descStyle = t.Dialog.SelectedItem
		}
		indent := strings.Repeat(" ", lipgloss.Width(indicator))
		descContent := ansi.Truncate(i.opt.Description, max(0, width-lipgloss.Width(indent)), "…")
		descWidth := lipgloss.Width(descContent)
		descGap := strings.Repeat(" ", max(0, width-lipgloss.Width(indent)-descWidth))
		line2 := descStyle.Render(indent + descContent + descGap)
		return lipgloss.JoinVertical(lipgloss.Left, line1, line2)
	}

	return line1
}
