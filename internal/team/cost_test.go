package team

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestUsageRecordFromRun locks acceptance #1: a TeamRun with all fields
// populated produces a flat UsageRecord with the same values. Nil pointers
// become zero.
func TestUsageRecordFromRun(t *testing.T) {
	prompt := int64(1200)
	completion := int64(300)
	cost := int64(4500)

	t.Run("all_fields_populated", func(t *testing.T) {
		run := TeamRun{
			ID: "run-1", TeamID: "team-1", MemberID: "member-1",
			PromptTokens: &prompt, CompletionTokens: &completion,
			CostMicros: &cost, UsageStatus: UsageStatusFinal,
		}
		rec := UsageRecordFromRun(run)
		assert.Equal(t, "team-1", rec.TeamID)
		assert.Equal(t, "member-1", rec.MemberID)
		assert.Equal(t, "run-1", rec.RunID)
		assert.Equal(t, prompt, rec.PromptTokens)
		assert.Equal(t, completion, rec.CompletionTokens)
		assert.Equal(t, cost, rec.CostMicros)
		assert.Equal(t, UsageStatusFinal, rec.UsageStatus)
	})

	t.Run("nil_pointers_become_zero", func(t *testing.T) {
		run := TeamRun{
			ID: "run-2", TeamID: "team-1", MemberID: "member-1",
			UsageStatus: UsageStatusUnknown,
		}
		rec := UsageRecordFromRun(run)
		assert.Equal(t, int64(0), rec.PromptTokens)
		assert.Equal(t, int64(0), rec.CompletionTokens)
		assert.Equal(t, int64(0), rec.CostMicros)
		assert.Equal(t, UsageStatusUnknown, rec.UsageStatus)
	})

	t.Run("partial_status", func(t *testing.T) {
		p := int64(500)
		run := TeamRun{
			ID: "run-3", TeamID: "team-1", MemberID: "member-2",
			PromptTokens: &p, UsageStatus: UsageStatusPartial,
		}
		rec := UsageRecordFromRun(run)
		assert.Equal(t, p, rec.PromptTokens)
		assert.Equal(t, int64(0), rec.CompletionTokens)
		assert.Equal(t, int64(0), rec.CostMicros)
		assert.Equal(t, UsageStatusPartial, rec.UsageStatus)
	})
}

// TestAggregateByMember locks acceptance #2: runs are grouped by member,
// costs are summed, and the output is ordered by first-appearance.
func TestAggregateByMember(t *testing.T) {
	prompt1 := int64(100)
	prompt2 := int64(200)
	cost1 := int64(300)
	cost2 := int64(500)

	t.Run("empty_input", func(t *testing.T) {
		result := AggregateByMember(nil)
		assert.Len(t, result, 0)
	})

	t.Run("single_member_single_run", func(t *testing.T) {
		runs := []TeamRun{
			{ID: "run-1", TeamID: "team-1", MemberID: "m-1",
				PromptTokens: &prompt1, CompletionTokens: &prompt2,
				CostMicros: &cost1, UsageStatus: UsageStatusFinal},
		}
		result := AggregateByMember(runs)
		require.Len(t, result, 1)
		assert.Equal(t, "m-1", result[0].MemberID)
		assert.Equal(t, prompt1, result[0].TotalPromptTokens)
		assert.Equal(t, prompt2, result[0].TotalCompletionTokens)
		assert.Equal(t, cost1, result[0].TotalCostMicros)
		assert.Equal(t, 0, result[0].UnknownRunCount)
		require.Len(t, result[0].Records, 1)
		assert.Equal(t, "run-1", result[0].Records[0].RunID)
	})

	t.Run("multiple_members_interleaved", func(t *testing.T) {
		runs := []TeamRun{
			{ID: "r1", TeamID: "t1", MemberID: "m-1",
				PromptTokens: &prompt1, CompletionTokens: &prompt1,
				CostMicros: &cost1, UsageStatus: UsageStatusFinal},
			{ID: "r2", TeamID: "t1", MemberID: "m-2",
				PromptTokens: &prompt2, CompletionTokens: &prompt2,
				CostMicros: &cost2, UsageStatus: UsageStatusPartial},
			{ID: "r3", TeamID: "t1", MemberID: "m-1",
				PromptTokens: &prompt2, CompletionTokens: &prompt1,
				CostMicros: &cost1, UsageStatus: UsageStatusFinal},
		}
		result := AggregateByMember(runs)
		require.Len(t, result, 2)
		// m-1 appeared first
		assert.Equal(t, "m-1", result[0].MemberID)
		assert.Equal(t, prompt1+prompt2, result[0].TotalPromptTokens)       // 100 + 200
		assert.Equal(t, prompt1+prompt1, result[0].TotalCompletionTokens)    // 100 + 100
		assert.Equal(t, cost1+cost1, result[0].TotalCostMicros)              // 300 + 300
		assert.Equal(t, 0, result[0].UnknownRunCount)
		require.Len(t, result[0].Records, 2)
		// m-2 appeared second
		assert.Equal(t, "m-2", result[1].MemberID)
		assert.Equal(t, prompt2, result[1].TotalPromptTokens)
		assert.Equal(t, 0, result[1].UnknownRunCount)
		require.Len(t, result[1].Records, 1)
	})

	t.Run("unknown_status_run_counted", func(t *testing.T) {
		runs := []TeamRun{
			{ID: "r1", TeamID: "t1", MemberID: "m-1",
				UsageStatus: UsageStatusUnknown},
			{ID: "r2", TeamID: "t1", MemberID: "m-1",
				PromptTokens: &prompt1, UsageStatus: UsageStatusFinal},
		}
		result := AggregateByMember(runs)
		require.Len(t, result, 1)
		assert.Equal(t, 1, result[0].UnknownRunCount)
		assert.Equal(t, prompt1, result[0].TotalPromptTokens) // unknown run contributed 0
	})
}

// TestAggregateTeamCost locks acceptance #3: member costs are summed into
// a team cost with correct totals and unknown-count accumulation.
func TestAggregateTeamCost(t *testing.T) {
	t.Run("empty_members", func(t *testing.T) {
		tc := AggregateTeamCost("t1", nil)
		assert.Equal(t, "t1", tc.TeamID)
		assert.Equal(t, int64(0), tc.TotalCostMicros)
		assert.Equal(t, 0, tc.UnknownRunCount)
		assert.Nil(t, tc.Members)
	})

	t.Run("single_member", func(t *testing.T) {
		members := []MemberCost{
			{
				MemberID: "m-1", TotalPromptTokens: 100,
				TotalCompletionTokens: 50, TotalCostMicros: 300,
				UnknownRunCount: 1,
			},
		}
		tc := AggregateTeamCost("t1", members)
		assert.Equal(t, "t1", tc.TeamID)
		assert.Equal(t, int64(100), tc.TotalPromptTokens)
		assert.Equal(t, int64(50), tc.TotalCompletionTokens)
		assert.Equal(t, int64(300), tc.TotalCostMicros)
		assert.Equal(t, 1, tc.UnknownRunCount)
		require.Len(t, tc.Members, 1)
	})

	t.Run("multiple_members", func(t *testing.T) {
		members := []MemberCost{
			{MemberID: "m-1", TotalPromptTokens: 100, TotalCompletionTokens: 50, TotalCostMicros: 300, UnknownRunCount: 0},
			{MemberID: "m-2", TotalPromptTokens: 200, TotalCompletionTokens: 100, TotalCostMicros: 500, UnknownRunCount: 2},
		}
		tc := AggregateTeamCost("t1", members)
		assert.Equal(t, int64(300), tc.TotalPromptTokens)
		assert.Equal(t, int64(150), tc.TotalCompletionTokens)
		assert.Equal(t, int64(800), tc.TotalCostMicros)
		assert.Equal(t, 2, tc.UnknownRunCount)
		require.Len(t, tc.Members, 2)
	})
}

// TestCostKnown locks acceptance #4: final and partial are known; unknown
// and empty string are not.
func TestCostKnown(t *testing.T) {
	assert.True(t, CostKnown(UsageStatusFinal))
	assert.True(t, CostKnown(UsageStatusPartial))
	assert.False(t, CostKnown(UsageStatusUnknown))
	assert.False(t, CostKnown(""))
	assert.False(t, CostKnown("bogus"))
}

// TestFormatCost locks acceptance #5: all scale tiers produce correct
// human-readable strings; unknown/empty status returns "---".
func TestFormatCost(t *testing.T) {
	t.Run("scale_tiers", func(t *testing.T) {
		tests := []struct {
			micros int64
			want   string
		}{
			{0, "0 tokens"},
			{1, "1 tokens"},
			{999, "999 tokens"},
			{1000, "1.0K tokens"},
			{1500, "1.5K tokens"},
			{1_000_000, "1.0M tokens"},
			{2_500_000, "2.5M tokens"},
			{1_000_000_000, "1.0B tokens"},
			{1_000_000_000_000, "1.0T tokens"},
		}
		for _, tt := range tests {
			got := FormatCost(tt.micros, UsageStatusFinal)
			assert.Equal(t, tt.want, got, "micros=%d", tt.micros)
		}
	})

	t.Run("unknown_status_returns_dash", func(t *testing.T) {
		assert.Equal(t, "---", FormatCost(5000, UsageStatusUnknown))
		assert.Equal(t, "---", FormatCost(0, UsageStatusUnknown))
		assert.Equal(t, "---", FormatCost(1_000_000, ""))
	})

	t.Run("partial_status_shows_cost", func(t *testing.T) {
		assert.Equal(t, "5.0K tokens", FormatCost(5000, UsageStatusPartial))
	})

	t.Run("final_status_shows_cost", func(t *testing.T) {
		assert.Equal(t, "1.0M tokens", FormatCost(1_000_000, UsageStatusFinal))
	})
}

// TestUsageRecord_JSONRoundTrip locks acceptance #6: UsageRecord must survive
// a JSON marshal/unmarshal round-trip with all fields preserved.
func TestUsageRecord_JSONRoundTrip(t *testing.T) {
	t.Run("all_fields_populated", func(t *testing.T) {
		in := UsageRecord{
			TeamID: "team-1", MemberID: "member-1", RunID: "run-1",
			PromptTokens: 1200, CompletionTokens: 300,
			CostMicros: 4500, UsageStatus: UsageStatusFinal,
		}
		var out UsageRecord
		b, err := json.Marshal(in)
		require.NoError(t, err)
		require.NoError(t, json.Unmarshal(b, &out))
		assert.Equal(t, in, out)
	})

	t.Run("partial_record", func(t *testing.T) {
		in := UsageRecord{
			TeamID: "team-1", MemberID: "member-2", RunID: "run-2",
			PromptTokens: 500, UsageStatus: UsageStatusPartial,
		}
		var out UsageRecord
		b, err := json.Marshal(in)
		require.NoError(t, err)
		require.NoError(t, json.Unmarshal(b, &out))
		assert.Equal(t, in, out)
	})

	t.Run("unknown_record_zero_cost", func(t *testing.T) {
		in := UsageRecord{
			TeamID: "team-1", MemberID: "member-3", RunID: "run-3",
			UsageStatus: UsageStatusUnknown,
		}
		var out UsageRecord
		b, err := json.Marshal(in)
		require.NoError(t, err)
		require.NoError(t, json.Unmarshal(b, &out))
		assert.Equal(t, in, out)
	})
}

// TestMemberCost_JSONRoundTrip locks acceptance #7: MemberCost round-trips.
func TestMemberCost_JSONRoundTrip(t *testing.T) {
	in := MemberCost{
		MemberID:              "m-1",
		TotalPromptTokens:     500,
		TotalCompletionTokens: 200,
		TotalCostMicros:       1500,
		UnknownRunCount:       1,
		Records: []UsageRecord{
			{TeamID: "t1", MemberID: "m-1", RunID: "r1", PromptTokens: 500,
				CompletionTokens: 200, CostMicros: 1500, UsageStatus: UsageStatusFinal},
		},
	}
	var out MemberCost
	b, err := json.Marshal(in)
	require.NoError(t, err)
	require.NoError(t, json.Unmarshal(b, &out))
	assert.Equal(t, in.MemberID, out.MemberID)
	assert.Equal(t, in.TotalPromptTokens, out.TotalPromptTokens)
	assert.Equal(t, in.TotalCostMicros, out.TotalCostMicros)
	assert.Equal(t, in.UnknownRunCount, out.UnknownRunCount)
	require.Len(t, out.Records, 1)
	assert.Equal(t, in.Records[0], out.Records[0])
}

// TestTeamCost_JSONRoundTrip locks acceptance #8: TeamCost round-trips.
func TestTeamCost_JSONRoundTrip(t *testing.T) {
	in := TeamCost{
		TeamID:                "t1",
		TotalPromptTokens:     1000,
		TotalCompletionTokens: 400,
		TotalCostMicros:       3000,
		UnknownRunCount:       2,
		Members: []MemberCost{
			{MemberID: "m-1", TotalPromptTokens: 600, TotalCompletionTokens: 250,
				TotalCostMicros: 1800, UnknownRunCount: 1},
			{MemberID: "m-2", TotalPromptTokens: 400, TotalCompletionTokens: 150,
				TotalCostMicros: 1200, UnknownRunCount: 1},
		},
	}
	var out TeamCost
	b, err := json.Marshal(in)
	require.NoError(t, err)
	require.NoError(t, json.Unmarshal(b, &out))
	assert.Equal(t, in.TeamID, out.TeamID)
	assert.Equal(t, in.TotalPromptTokens, out.TotalPromptTokens)
	assert.Equal(t, in.TotalCostMicros, out.TotalCostMicros)
	assert.Equal(t, in.UnknownRunCount, out.UnknownRunCount)
	require.Len(t, out.Members, 2)
	assert.Equal(t, "m-1", out.Members[0].MemberID)
	assert.Equal(t, "m-2", out.Members[1].MemberID)
}
