package team

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestStatusIconMapping(t *testing.T) {
	tests := []struct {
		status string
		want   string
	}{
		// Active/running states
		{"running", "●"},
		{"in_progress", "●"},

		// Completed/done states
		{"completed", "✓"},
		{"done", "✓"},

		// Failed/error states
		{"failed", "✗"},
		{"error", "✗"},

		// Paused
		{"paused", "⏸"},

		// Blocked/waiting
		{"blocked", "⏳"},
		{"waiting_permission", "⏳"},

		// Queued/pending (not yet started)
		{"queued", "○"},
		{"assigned", "○"},
		{"created", "○"},
		{"starting", "○"},

		// Stopped/terminal
		{"stopped", "■"},
		{"canceled", "■"},
		{"shutting_down", "■"},
		{"canceling_turn", "■"},

		// Idle
		{"idle", "◇"},

		// Unknown / empty
		{"unknown", " "},
		{"", " "},
	}

	for _, tt := range tests {
		t.Run(tt.status, func(t *testing.T) {
			got := StatusIcon(tt.status)
			assert.Equal(t, tt.want, got, "StatusIcon(%q) = %q, want %q", tt.status, got, tt.want)
		})
	}
}
