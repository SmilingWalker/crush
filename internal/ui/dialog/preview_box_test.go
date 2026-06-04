package dialog

import (
	"strings"
	"testing"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/crush/internal/ui/styles"
	"github.com/stretchr/testify/require"
)

func TestRenderPreviewBox(t *testing.T) {
	sty := styles.CharmtonePantera()

	t.Run("empty content shows placeholder", func(t *testing.T) {
		result := renderPreviewBox(previewBoxConfig{
			content:  "",
			width:    40,
			maxLines: 10,
			styles:   &sty,
		})
		require.Contains(t, result, "No preview available")
		require.Contains(t, result, "│")
	})

	t.Run("renders markdown content with left border", func(t *testing.T) {
		result := renderPreviewBox(previewBoxConfig{
			content:  "# Hello\n\nThis is **bold** text.",
			width:    50,
			maxLines: 10,
			styles:   &sty,
		})
		require.Contains(t, result, "│")
		lines := strings.Split(result, "\n")
		require.GreaterOrEqual(t, len(lines), 2)
	})

	t.Run("truncates long content", func(t *testing.T) {
		longContent := strings.Repeat("line content\n", 30)
		result := renderPreviewBox(previewBoxConfig{
			content:  longContent,
			width:    40,
			maxLines: 5,
			styles:   &sty,
		})
		require.Contains(t, result, "✂")
		require.Contains(t, result, "more lines hidden")
	})

	t.Run("no truncation for short content", func(t *testing.T) {
		result := renderPreviewBox(previewBoxConfig{
			content:  "short content",
			width:    40,
			maxLines: 20,
			styles:   &sty,
		})
		require.NotContains(t, result, "✂")
	})

	t.Run("respects width constraint", func(t *testing.T) {
		result := renderPreviewBox(previewBoxConfig{
			content:  "some content here",
			width:    30,
			maxLines: 10,
			minWidth: 20,
			styles:   &sty,
		})
		for _, line := range strings.Split(result, "\n") {
			visualWidth := lipgloss.Width(line)
			require.LessOrEqual(t, visualWidth, 30,
				"line visual width %d exceeds width 30: %q", visualWidth, line)
		}
	})

	t.Run("handles single line content", func(t *testing.T) {
		result := renderPreviewBox(previewBoxConfig{
			content:  "just one line",
			width:    40,
			maxLines: 10,
			styles:   &sty,
		})
		require.Contains(t, result, "│")
		lines := strings.Split(result, "\n")
		require.Equal(t, 1, len(lines))
	})
}
