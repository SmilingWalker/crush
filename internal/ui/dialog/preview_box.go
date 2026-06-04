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
	boxTopLeft     = "┌"
	boxTopRight    = "┐"
	boxBottomLeft  = "└"
	boxBottomRight = "┘"
	boxHorizontal  = "─"
	boxVertical    = "│"
	boxTeeLeft     = "├"
	boxTeeRight    = "┤"
	scissors       = "✂"
)

// previewBoxConfig holds configuration for rendering a preview box.
type previewBoxConfig struct {
	content  string
	width    int
	maxLines int
	minWidth int
	styles   *styles.Styles
}

// renderPreviewBox renders markdown content inside a bordered box.
// Returns the rendered string with box-drawing borders, markdown formatting,
// and line truncation if content exceeds maxLines.
func renderPreviewBox(cfg previewBoxConfig) string {
	if cfg.minWidth <= 0 {
		cfg.minWidth = 20
	}
	if cfg.maxLines <= 0 {
		cfg.maxLines = 20
	}

	// Calculate box dimensions
	innerWidth := cfg.width - 4 // 2 border chars + 2 padding per line
	if innerWidth < cfg.minWidth-4 {
		innerWidth = cfg.minWidth - 4
	}
	boxWidth := innerWidth + 4

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
				rendered = cfg.content // fallback to raw text
			}
		} else {
			rendered = cfg.content // fallback if renderer is nil
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

	// Build borders
	hBorder := strings.Repeat(boxHorizontal, boxWidth-2)
	topBorder := boxTopLeft + hBorder + boxTopRight
	bottomBorder := boxBottomLeft + hBorder + boxBottomRight

	// Build truncation bar if needed
	truncationBar := ""
	if isTruncated {
		hiddenCount := totalLines - cfg.maxLines
		label := fmt.Sprintf("%s %s %s %s %d lines hidden ",
			strings.Repeat(boxHorizontal, 3),
			scissors,
			strings.Repeat(boxHorizontal, 3),
			strings.Repeat(boxHorizontal, 3),
			hiddenCount,
		)
		labelWidth := lipgloss.Width(label)
		fillWidth := max(0, boxWidth-2-labelWidth)
		truncationBar = boxTeeLeft + label + strings.Repeat(boxHorizontal, fillWidth) + boxTeeRight
	}

	// Dim style for borders
	borderStyle := cfg.styles.Dialog.SecondaryText

	// Build content lines
	var contentLines []string
	for _, line := range lines {
		lineWidth := lipgloss.Width(line)
		if lineWidth > innerWidth {
			line = ansi.Truncate(line, innerWidth, "")
		}
		padding := strings.Repeat(" ", max(0, innerWidth-lipgloss.Width(line)))
		contentLines = append(contentLines,
			borderStyle.Render(boxVertical)+" "+
				line+
				padding+" "+
				borderStyle.Render(boxVertical),
		)
	}

	// Assemble box
	var parts []string
	parts = append(parts, borderStyle.Render(topBorder))
	parts = append(parts, contentLines...)
	if truncationBar != "" {
		warningStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("11"))
		parts = append(parts, warningStyle.Render(truncationBar))
	}
	parts = append(parts, borderStyle.Render(bottomBorder))

	return strings.Join(parts, "\n")
}
