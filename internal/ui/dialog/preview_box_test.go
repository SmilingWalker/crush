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
		require.Contains(t, result, boxTopLeft)
		require.Contains(t, result, boxBottomRight)
	})

	t.Run("renders markdown content with borders", func(t *testing.T) {
		result := renderPreviewBox(previewBoxConfig{
			content:  "# Hello\n\nThis is **bold** text.",
			width:    50,
			maxLines: 10,
			styles:   &sty,
		})
		require.Contains(t, result, boxTopLeft)
		require.Contains(t, result, boxBottomRight)
		require.Contains(t, result, boxVertical)
		// Should have at least 4 lines (top border + content + bottom border)
		lines := strings.Split(result, "\n")
		require.GreaterOrEqual(t, len(lines), 4)
	})

	t.Run("truncates long content", func(t *testing.T) {
		longContent := strings.Repeat("line content\n", 30)
		result := renderPreviewBox(previewBoxConfig{
			content:  longContent,
			width:    40,
			maxLines: 5,
			styles:   &sty,
		})
		require.Contains(t, result, scissors)
		require.Contains(t, result, "lines hidden")
	})

	t.Run("no truncation for short content", func(t *testing.T) {
		result := renderPreviewBox(previewBoxConfig{
			content:  "short content",
			width:    40,
			maxLines: 20,
			styles:   &sty,
		})
		require.NotContains(t, result, scissors)
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
			// lipgloss.Width measures visual width (strips ANSI codes)
			visualWidth := lipgloss.Width(line)
			require.LessOrEqual(t, visualWidth, 36,
				"line visual width %d exceeds reasonable bounds: %q", visualWidth, line)
		}
	})

	t.Run("handles single line content", func(t *testing.T) {
		result := renderPreviewBox(previewBoxConfig{
			content:  "just one line",
			width:    40,
			maxLines: 10,
			styles:   &sty,
		})
		require.Contains(t, result, boxTopLeft)
		require.Contains(t, result, boxBottomRight)
	})
}
