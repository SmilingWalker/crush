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
func (l *questionOptionsList) SetQuestion(q questions.Question, selOpts map[int]bool) {
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
	// Add "Other..." option
	items = append(items, &questionOptionsListItem{
		Versioned:    list.NewVersioned(),
		parent:       l,
		opt:          questions.Option{Label: "Other...", Description: "Provide a custom answer"},
		selected:     selOpts[len(q.Options)], // Other is at index len(q.Options)
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

	// Label style based on focus
	labelStyle := t.Dialog.NormalItem
	if i.focused {
		labelStyle = t.Dialog.SelectedItem
	}

	labelRender := labelStyle.Render(i.opt.Label)

	// Description on the same line, right-aligned with gap
	descRender := ""
	if len(i.opt.Description) > 0 {
		descStyle := t.Dialog.SecondaryText
		descAvailWidth := width - lipgloss.Width(indicator) - lipgloss.Width(labelRender) - 2
		optDesc := ansi.Truncate(i.opt.Description, max(0, descAvailWidth), "…")
		descRender = descStyle.Render(optDesc)
	}

	gapWidth := max(0, width-lipgloss.Width(indicator)-lipgloss.Width(labelRender)-lipgloss.Width(descRender))
	gapRender := labelStyle.Render(strings.Repeat(" ", gapWidth))

	return labelStyle.Render(indicator + labelRender + gapRender + descRender)
}
