package team

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// TestStatus_Valid_AllConsts locks acceptance #1: every declared const of each
// Status type must report Valid()==true, and any string not in the const set
// must report false. The const-set slices (allTeamStatuses etc.) are the single
// source of truth — if a const is added without a case in Valid(), the
// allConsts loop here catches it.
func TestStatus_Valid_AllConsts(t *testing.T) {
	t.Run("TeamStatus", func(t *testing.T) {
		for _, s := range allTeamStatuses {
			assert.Truef(t, s.Valid(), "const %q should be Valid", s)
		}
		// Exhaustive set check: 8 values per master doc :285-295.
		assert.Len(t, allTeamStatuses, 8, "TeamStatus must have exactly 8 consts")
		// Bogus values must be invalid.
		for _, bad := range []TeamStatus{"", "CREATED", "paused ", "unknown", "archived "} {
			assert.Falsef(t, bad.Valid(), "bogus %q must be invalid", bad)
		}
	})

	t.Run("MemberStatus", func(t *testing.T) {
		for _, s := range allMemberStatuses {
			assert.Truef(t, s.Valid(), "const %q should be Valid", s)
		}
		assert.Len(t, allMemberStatuses, 11, "MemberStatus must have exactly 11 consts")
		for _, bad := range []MemberStatus{"", "RUNNING", "waiting-permission", "stopped_at"} {
			assert.Falsef(t, bad.Valid(), "bogus %q must be invalid", bad)
		}
	})

	t.Run("TaskStatus", func(t *testing.T) {
		for _, s := range allTaskStatuses {
			assert.Truef(t, s.Valid(), "const %q should be Valid", s)
		}
		assert.Len(t, allTaskStatuses, 7, "TaskStatus must have exactly 7 consts")
		for _, bad := range []TaskStatus{"", "IN_PROGRESS", "in-progress", "done"} {
			assert.Falsef(t, bad.Valid(), "bogus %q must be invalid", bad)
		}
	})

	t.Run("RunStatus", func(t *testing.T) {
		for _, s := range allRunStatuses {
			assert.Truef(t, s.Valid(), "const %q should be Valid", s)
		}
		assert.Len(t, allRunStatuses, 7, "RunStatus must have exactly 7 consts")
		for _, bad := range []RunStatus{"", "RUNNING", "interrupted ", "success"} {
			assert.Falsef(t, bad.Valid(), "bogus %q must be invalid", bad)
		}
	})
}
