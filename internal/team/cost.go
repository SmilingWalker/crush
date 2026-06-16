// cost.go defines the M4-12 Cost Accounting types and aggregation functions.
// UsageRecord is a flat (non-pointer) cost record extracted from a TeamRun.
// MemberCost and TeamCost aggregate across members and teams respectively.
// FormatCost renders human-readable cost with "---" for unknown usage_status.
//
// UsageStatus constants (UsageStatusFinal / UsageStatusPartial / UsageStatusUnknown)
// are defined in recovery.go (M4-08). M4-12 consumes them for display decisions.

package team

import "fmt"

// UsageRecord is a flat cost record derived from a TeamRun. Unlike TeamRun
// (which uses *int64 for nullable DB columns), UsageRecord uses plain int64
// — callers supply zero for nil pointer fields. UsageStatus is always set
// to one of the recovery.go UsageStatus consts (final/partial/unknown).
type UsageRecord struct {
	TeamID           string `json:"team_id"`
	MemberID         string `json:"member_id"`
	RunID            string `json:"run_id"`
	PromptTokens     int64  `json:"prompt_tokens"`
	CompletionTokens int64  `json:"completion_tokens"`
	CostMicros       int64  `json:"cost_micros"`
	UsageStatus      string `json:"usage_status"`
}

// MemberCost aggregates cost for one member across all their runs.
type MemberCost struct {
	MemberID              string        `json:"member_id"`
	TotalPromptTokens     int64         `json:"total_prompt_tokens"`
	TotalCompletionTokens int64         `json:"total_completion_tokens"`
	TotalCostMicros       int64         `json:"total_cost_micros"`
	UnknownRunCount       int           `json:"unknown_run_count"`
	Records               []UsageRecord `json:"records,omitempty"`
}

// TeamCost aggregates cost for a team across all its members.
type TeamCost struct {
	TeamID                string       `json:"team_id"`
	TotalPromptTokens     int64        `json:"total_prompt_tokens"`
	TotalCompletionTokens int64        `json:"total_completion_tokens"`
	TotalCostMicros       int64        `json:"total_cost_micros"`
	UnknownRunCount       int          `json:"unknown_run_count"`
	Members               []MemberCost `json:"members,omitempty"`
}

// UsageRecordFromRun extracts a flat UsageRecord from a TeamRun.
// Nil pointer fields in the run are treated as zero.
func UsageRecordFromRun(r TeamRun) UsageRecord {
	rec := UsageRecord{
		TeamID:      r.TeamID,
		MemberID:    r.MemberID,
		RunID:       r.ID,
		UsageStatus: r.UsageStatus,
	}
	if r.PromptTokens != nil {
		rec.PromptTokens = *r.PromptTokens
	}
	if r.CompletionTokens != nil {
		rec.CompletionTokens = *r.CompletionTokens
	}
	if r.CostMicros != nil {
		rec.CostMicros = *r.CostMicros
	}
	return rec
}

// AggregateByMember groups a slice of TeamRuns by MemberID and returns a
// MemberCost per member. Runs are sorted by member order of first appearance.
// Each run is converted to a UsageRecord and summed into the member total.
func AggregateByMember(runs []TeamRun) []MemberCost {
	type accum struct {
		cost    MemberCost
		order   int
	}
	byID := make(map[string]*accum, len(runs))
	order := 0

	for _, r := range runs {
		rec := UsageRecordFromRun(r)
		entry, ok := byID[r.MemberID]
		if !ok {
			entry = &accum{
				cost: MemberCost{
					MemberID: r.MemberID,
					Records:  make([]UsageRecord, 0, 1),
				},
				order: order,
			}
			byID[r.MemberID] = entry
			order++
		}
		entry.cost.TotalPromptTokens += rec.PromptTokens
		entry.cost.TotalCompletionTokens += rec.CompletionTokens
		entry.cost.TotalCostMicros += rec.CostMicros
		if rec.UsageStatus == UsageStatusUnknown {
			entry.cost.UnknownRunCount++
		}
		entry.cost.Records = append(entry.cost.Records, rec)
	}

	out := make([]MemberCost, order)
	for _, v := range byID {
		out[v.order] = v.cost
	}
	return out
}

// AggregateTeamCost rolls up a slice of MemberCost values into a single
// TeamCost. The TeamID is taken from the first member, or left empty if
// the input is empty.
func AggregateTeamCost(teamID string, members []MemberCost) TeamCost {
	tc := TeamCost{
		TeamID:  teamID,
		Members: members,
	}
	for _, m := range members {
		tc.TotalPromptTokens += m.TotalPromptTokens
		tc.TotalCompletionTokens += m.TotalCompletionTokens
		tc.TotalCostMicros += m.TotalCostMicros
		tc.UnknownRunCount += m.UnknownRunCount
	}
	return tc
}

// CostKnown reports whether the usageStatus indicates any cost data was
// recorded. Returns true for final and partial; false for unknown or empty.
func CostKnown(usageStatus string) bool {
	return usageStatus == UsageStatusFinal || usageStatus == UsageStatusPartial
}

// FormatCost renders cost in micros to a human-readable token string.
// When usageStatus is "unknown" or empty, returns "---" because the
// cost number is meaningless (no usage data was recorded).
// Otherwise scales: tokens, K, M, B, T for thousands/millions/billions/trillions.
func FormatCost(micros int64, usageStatus string) string {
	if !CostKnown(usageStatus) {
		return "---"
	}
	if micros >= 1_000_000_000_000 {
		return fmt.Sprintf("%.1fT tokens", float64(micros)/1_000_000_000_000)
	}
	if micros >= 1_000_000_000 {
		return fmt.Sprintf("%.1fB tokens", float64(micros)/1_000_000_000)
	}
	if micros >= 1_000_000 {
		return fmt.Sprintf("%.1fM tokens", float64(micros)/1_000_000)
	}
	if micros >= 1_000 {
		return fmt.Sprintf("%.1fK tokens", float64(micros)/1_000)
	}
	return fmt.Sprintf("%d tokens", micros)
}
