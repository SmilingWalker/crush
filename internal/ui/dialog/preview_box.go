package dialog

import (
	"fmt"
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/crush/internal/ui/common"
	"github.com/charmbracelet/crush/internal/ui/styles"
	"github.com/charmbracelet/x/ansi"
)

// previewBoxConfig holds configuration for rendering a preview box.
type previewBoxConfig struct {
	content  string
	width    int
	maxLines int
	minWidth int
	styles   *styles.Styles
}

// renderPreviewBox renders markdown content with a left border indicator.
// No top/bottom borders to maximize vertical content space.
// Truncates with ✂ indicator if content exceeds maxLines.
func renderPreviewBox(cfg previewBoxConfig) string {
	if cfg.minWidth <= 0 {
		cfg.minWidth = 20
	}
	if cfg.maxLines <= 0 {
		cfg.maxLines = 20
	}

	// Content width — full cfg.width minus 2 for "│ " prefix
	innerWidth := cfg.width - 2
	if innerWidth < cfg.minWidth-2 {
		innerWidth = cfg.minWidth - 2
	}

	// Render markdown content
	var rendered string
	if strings.TrimSpace(cfg.content) == "" {
		rendered = cfg.styles.Dialog.SecondaryText.Italic(true).
			Render("No preview available")
	} else {
		renderer := common.MarkdownRenderer(cfg.styles, innerWidth)
		if renderer != nil {
			var err error
			rendered, err = renderer.Render(cfg.content)
			if err != nil {
				rendered = cfg.content
			}
		} else {
			rendered = cfg.content
		}
	}

	// Split into lines
	rendered = strings.TrimRight(rendered, "\n")
	lines := strings.Split(rendered, "\n")

	// Truncate if needed
	totalLines := len(lines)
	isTruncated := totalLines > cfg.maxLines
	if isTruncated {
		lines = lines[:cfg.maxLines]
	}

	// Foreground-only style for the left border indicator
	borderFg := lipgloss.NewStyle().Foreground(cfg.styles.Dialog.SecondaryText.GetForeground())

	// Build content lines with left border indicator
	var contentLines []string
	for _, line := range lines {
		lineWidth := lipgloss.Width(line)
		if lineWidth > innerWidth {
			line = ansi.Truncate(line, innerWidth, "")
		}
		padding := strings.Repeat(" ", max(0, innerWidth-lipgloss.Width(line)))
		contentLines = append(contentLines, borderFg.Render("│ ")+line+padding)
	}

	// Truncation indicator
	if isTruncated {
		hiddenCount := totalLines - cfg.maxLines
		contentLines = append(contentLines, borderFg.Render(
			fmt.Sprintf("│ ✂ %d more lines hidden", hiddenCount)))
	}

	return strings.Join(contentLines, "\n")
}
