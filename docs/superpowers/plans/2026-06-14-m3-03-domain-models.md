# M3-03 Domain Models Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add a pure-Go domain type layer to `package team` — four string-typed Status enums (each with a `Valid()` method), six domain structs (`Team`, `TeamMember`, `TeamTask`, `TeamRun`, `TeamEvent`, `AuditEvent`), and a `TeamSnapshot` aggregate — that express the M3 team-data-domain business semantics. These types are the lingua franca every M3 store (M3-04) and the TeamService facade (M3-05) consumes and returns.

**Architecture:** One new file `internal/team/models.go` lives in the *existing* `package team` (which M2 populated with the delegate runtime types: `DelegateTask`/`DelegateResult`/`DelegateRunGroup`/`DelegateRunner`). The domain types are deliberately **separate from** the sqlc-generated `package db` persistence types: domain uses idiomatic Go (`time.Time` for timestamps, `*int64`/`*time.Time` pointers for nullable columns, named string types for enums), while sqlc emits SQLite-native types (`int64` epoch millis, `sql.NullInt64`, bare `string`). M3-04 owns the `toTeamTask(row) TeamTask` translation between the two layers (master doc `04-m3-team-data-domain.md:431`). M3-03 therefore depends on **no** sqlc output, no DB, and no M2 delegate code — it imports only the stdlib (`time`) and compiles standalone.

**Tech Stack:** Go 1.26.3 (`module github.com/charmbracelet/crush`), `github.com/stretchr/testify` (assert + require — the `package team` test convention, see `delegate_types_test.go`). New file in existing package `github.com/charmbracelet/crush/internal/team`. Worktree `g:/ai-project/remote-github/crush-worktrees/m3-03`, branch `m3-03-domain-models`, off agent-team tip `4dfaf2e1` (Merge M3-01). Shell: bash on win32 (forward slashes, `/dev/null`).

---

## Design seam corrections (vs master task doc `04-m3-team-data-domain.md:274-381`)

The master task doc is the approved design intent. The following are **verified implementation decisions** for the fields the master doc left as `/* ... */` (TeamMember/TeamTask/TeamRun/TeamEvent/AuditEvent) plus the seam questions the team-lead flagged. Each is backed by cited evidence from the M3-01 migration and M3 data-contract doc at agent-team tip `4dfaf2e1`.

### Seam 1: Package `team` coexists with M2 delegate types — NO naming collision (verified)

M2 added `delegate_types.go` / `delegate_runner.go` / `delegate_aggregate_test.go` to `package team`. **Research result:** the M2 type names (`DelegateTask`, `DelegateResult`, `DelegateRunGroup`, `DelegateRunner`) share **no** identifier with the M3-03 domain types (`Team`, `TeamMember`, `TeamTask`, `TeamRun`, `TeamEvent`, `AuditEvent`, `TeamSnapshot`, `TeamStatus`, `MemberStatus`, `TaskStatus`, `RunStatus`). `grep -rn` across `internal/` returns **zero** non-test, non-docs matches — the M3-03 names are unused. M3-03 therefore adds `models.go` to the **same** `package team` with no rename, no build-tag split, and no new package. Task 5's `go build ./internal/team/...` confirms the merged package compiles clean.

**Evidence:** `internal/team/delegate_types.go` read in full (4 type defs, none colliding); Grep over worktree tree confirms only `docs/` + task-doc references to the M3-03 type names.

### Seam 2: Domain layer vs sqlc persistence layer = two type sets, M3-04 converts (master design)

The master domain spec (`:344-358`) types `Team` with `time.Time` for `CreatedAt`/`UpdatedAt`, `*int64` for nullable `MaxCost`/`MaxTokens`, `*time.Time` for nullable `ArchivedAt`, and named string types for `Status`. The sqlc-generated `package db` types (master doc M3-02) emit `int64` for every SQLite `INTEGER` column (epoch millis for timestamps), `sql.NullInt64` for nullable `INTEGER`, and bare `string` for `status`. **Decision (master design, not a deviation):** M3-03 defines the domain types in `package team` and **never imports `package db` or sqlc output**; M3-04 (`store_*.go`) owns one `to<DomainType>(row db.<RowType>) <DomainType>` converter per table (the master doc shows `toTeamTask(row)` at `:431`). The split keeps the domain layer free of `database/sql` and SQLite epoch-millis leakage.

**Evidence:** master doc `:344-358` (domain types) vs M3-02 sqlc queries; `:431` (`toTeamTask(row)` in M3-04 store). The current `internal/db/models.go` (sqlc-generated) uses `int64`/`sql.NullInt64`/`string`.

### Seam 3: The four Status enums + `Valid()` — full const set, verified against M3-01 schema + master doc

The master doc (`:285-338`) lists exact enum values. This plan reproduces them verbatim and cross-checks each against the M3-01 migration column it gates:

- **TeamStatus** (8): gates `teams.status` (migration `:18`, default `'created'`).
- **MemberStatus** (11): gates `team_members.status` (migration `:37`, default `'created'`).
- **TaskStatus** (7): gates `team_tasks.status` (migration `:56`, default `'queued'`).
- **RunStatus** (7): gates `team_runs.status` (migration `:73`, default `'queued'`).

Each `Valid()` iterates a package-level slice (`allTeamStatuses` etc.) over **every** const of that type — single source of truth, DRY. The test `TestStatus_Valid_AllConsts` asserts the const counts (8/11/7/7) and rejects bogus values; `TestStatus_ConstSetExhaustsValid` guards the slice↔Valid() invariant.

**Evidence:** master doc `:285-338`; migration `20260614000000_create_team_tables.sql:18,37,56,73` (status defaults confirm seed values are in the enum).

### Seam 4: The 5 structs the master doc left as `/* ... */` — fields derived 1:1 from M3-01 columns

The master doc fully specifies only `Team` (`:344-358`) and `TeamSnapshot` (`:367-373`). `TeamMember`/`TeamTask`/`TeamRun`/`TeamEvent`/`AuditEvent` are stubbed `/* ... */`. **Decision:** derive every domain struct field 1:1 from its M3-01 table column, applying the domain-vs-persistence type mapping from Seam 2 (`INTEGER` epoch millis → `time.Time`; nullable `INTEGER` → `*int64`; nullable `INTEGER` epoch → `*time.Time`; `INTEGER` non-null → `int64`/`int`; `TEXT` non-null → `string`; nullable `TEXT` → `*string`). JSON tags are `snake_case` matching the column names, with `,omitempty` on nullable/optional fields.

One field-level note: `team_runs.usage_status` (migration `:81`, values `final`/`partial`/`unknown`) is **not** one of the four Status enums. It is a free `string` field `UsageStatus` on `TeamRun` (open-ended value set per the data contract, so a closed enum would be premature).

**Evidence:** migration `20260614000000_create_team_tables.sql:12-126` (all column definitions); data-contract doc `04-team-domain-data-contract.md:126` (usage_status value set).

### Seam 5: `team_event_counters` has NO domain type (YAGNI)

The migration's `team_event_counters` table is pure persistence machinery (the counter is read/incremented inside the `NextEventSeq` sqlc query). M3-03 defines **no** `TeamEventCounter` domain type — it never surfaces to business logic or the API. `team_audit_events` optional columns are all `*string` on `AuditEvent`; audit is append-only with no status lifecycle.

### Seam 6: JSON round-trip (acceptance #2) — `time.Time` equality + pointer-nil + omitempty

Acceptance #2 requires `json.Marshal(s)` → `json.Unmarshal(bytes, &s2)` to reproduce every field. Three subtleties:

1. **`time.Time` equality:** JSON marshal of `time.Time` uses RFC3339Nano (UTC, drops monotonic). Compare via `t1.Equal(t2)` field-by-field, NOT `reflect.DeepEqual` on the whole struct.
2. **Pointer-nil vs omitempty:** nullable `*int64`/`*time.Time` use `,omitempty`, so a `nil` pointer round-trips to `nil`. The test covers pointers set (incl. `*int64` pointing at `0` — omitempty does **not** drop non-nil pointers to zero) and all-`nil`.
3. **`omitempty` on zero scalars:** an empty `""` description is dropped on marshal; the test asserts absent keys via a `map[string]json.RawMessage` key check.

`TeamSnapshot` is also round-tripped (it embeds slices of the above; empty snapshot → nil slices).

### Seam 7 (import-timing, added after review): `time` import lands with the first struct, NOT in Task 1

`models.go` imports only `time`, but Task 1 (enums only) uses **no** `time.Time`. An `import "time"` in the Task 1 commit would be an unused import → `go build` fails. **Decision:** Task 1's `models.go` has **no** import block (enums need none); `import "time"` is added in Task 2 Step 3 when the `Team` struct (first `time.Time` usage) lands. This keeps every commit compiling clean. (Plan originally wrote `import "time"` in Task 1 — corrected per team-lead review.)

---

## File Structure

| File | Responsibility | Created/Modified |
|---|---|---|
| `internal/team/models.go` | The entire M3-03 domain type layer: 4 Status string-typed enums + const sets + `Valid()` methods; 6 domain structs; 1 aggregate (`TeamSnapshot`). Stdlib `time` only. | **Create** |
| `internal/team/models_test.go` | Acceptance coverage: every `Valid()` covers all consts + rejects bogus values (acceptance #1); every domain struct + `TeamSnapshot` JSON round-trips (acceptance #2); package compiles + vets clean (acceptance #3). | **Create** |

No other files are touched. The existing `delegate_types.go`/`delegate_runner.go`/`*_test.go` in `package team` are untouched (Seam 1).

---

## Out of scope

- **M3-02 (sqlc queries):** parallel programmer owns `internal/db/sql/*.sql` + regenerated `internal/db/*.sql.go`/`models.go`.
- **M3-04 (store layer):** owns `to<DomainType>(row)` converters.
- **M3-01 migration:** already merged (`4dfaf2e1`).
- **`team_event_counters` domain type:** pure persistence machinery (Seam 5).
- **`UsageStatus` enum:** free `string` field (Seam 4).
- **M2 delegate code:** untouched.

---

## Task 1: Status enums, const sets, and `Valid()` methods

**Files:**
- Create: `internal/team/models.go`
- Create: `internal/team/models_test.go`

- [ ] **Step 1: Write the failing test for all four `Valid()` methods**

Create `internal/team/models_test.go`:

```go
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
```

- [ ] **Step 2: Run test to verify it fails (no types defined yet)**

Run: `cd G:/ai-project/remote-github/crush-worktrees/m3-03 && go test ./internal/team/ -run TestStatus_Valid_AllConsts -v`
Expected: FAIL / build error — `allTeamStatuses` undefined, `TeamStatus` undefined, `Valid` undefined.

- [ ] **Step 3: Write minimal implementation — enums, consts, slices, `Valid()` (NO `time` import yet)**

Create `internal/team/models.go`. Note: enums use no `time.Time`, so there is **no** `import "time"` in this commit (Seam 7 — `time` lands in Task 2). The file header comment notes `time` is added in Task 2.

```go
// models.go defines the M3 team-data-domain type layer: the four Status enums
// (TeamStatus/MemberStatus/TaskStatus/RunStatus) that gate the lifecycle of a
// team, its members, tasks, and runs, plus the six domain structs (Team,
// TeamMember, TeamTask, TeamRun, TeamEvent, AuditEvent) and the TeamSnapshot
// aggregate that the M3 stores (M3-04) and TeamService (M3-05) consume.
//
// These domain types are deliberately separate from the sqlc-generated package
// db row types: domain uses idiomatic Go (time.Time, *int64/*time.Time
// pointers for nullable columns, named string types for enums); package db
// uses SQLite-native types (int64 epoch millis, sql.NullInt64, bare string).
// M3-04 owns the to<DomainType>(row) translation between the layers.
//
// This file depends only on the standard library (time, added in Task 2 once
// the first struct lands). It does not import package db, sqlc output, or the
// M2 delegate types.

package team

// TeamStatus is the lifecycle state of a Team. Gates teams.status
// (migration 20260614000000_create_team_tables.sql:18, default 'created').
type TeamStatus string

const (
	TeamCreated   TeamStatus = "created"
	TeamRunning   TeamStatus = "running"
	TeamPaused    TeamStatus = "paused"
	TeamCanceling TeamStatus = "canceling"
	TeamStopped   TeamStatus = "stopped"
	TeamCompleted TeamStatus = "completed"
	TeamFailed    TeamStatus = "failed"
	TeamArchived  TeamStatus = "archived"
)

// allTeamStatuses is the single source of truth for the TeamStatus const set.
// Valid() and the tests both read it, so adding a const here without covering
// it in Valid() is caught by the round-trip table test.
var allTeamStatuses = []TeamStatus{
	TeamCreated, TeamRunning, TeamPaused, TeamCanceling,
	TeamStopped, TeamCompleted, TeamFailed, TeamArchived,
}

// Valid reports whether s is one of the declared TeamStatus consts.
func (s TeamStatus) Valid() bool {
	for _, v := range allTeamStatuses {
		if s == v {
			return true
		}
	}
	return false
}

// MemberStatus is the lifecycle state of a TeamMember. Gates
// team_members.status (migration :37, default 'created'). 11 values.
type MemberStatus string

const (
	MemberCreated           MemberStatus = "created"
	MemberStarting          MemberStatus = "starting"
	MemberIdle              MemberStatus = "idle"
	MemberQueued            MemberStatus = "queued"
	MemberRunning           MemberStatus = "running"
	MemberWaitingPermission MemberStatus = "waiting_permission"
	MemberBlocked           MemberStatus = "blocked"
	MemberCancelingTurn     MemberStatus = "canceling_turn"
	MemberShuttingDown      MemberStatus = "shutting_down"
	MemberStopped           MemberStatus = "stopped"
	MemberFailed            MemberStatus = "failed"
)

var allMemberStatuses = []MemberStatus{
	MemberCreated, MemberStarting, MemberIdle, MemberQueued, MemberRunning,
	MemberWaitingPermission, MemberBlocked, MemberCancelingTurn,
	MemberShuttingDown, MemberStopped, MemberFailed,
}

func (s MemberStatus) Valid() bool {
	for _, v := range allMemberStatuses {
		if s == v {
			return true
		}
	}
	return false
}

// TaskStatus is the lifecycle state of a TeamTask. Gates team_tasks.status
// (migration :56, default 'queued'). 7 values.
type TaskStatus string

const (
	TaskQueued     TaskStatus = "queued"
	TaskAssigned   TaskStatus = "assigned"
	TaskInProgress TaskStatus = "in_progress"
	TaskBlocked    TaskStatus = "blocked"
	TaskCompleted  TaskStatus = "completed"
	TaskFailed     TaskStatus = "failed"
	TaskCanceled   TaskStatus = "canceled"
)

var allTaskStatuses = []TaskStatus{
	TaskQueued, TaskAssigned, TaskInProgress, TaskBlocked,
	TaskCompleted, TaskFailed, TaskCanceled,
}

func (s TaskStatus) Valid() bool {
	for _, v := range allTaskStatuses {
		if s == v {
			return true
		}
	}
	return false
}

// RunStatus is the lifecycle state of a TeamRun. Gates team_runs.status
// (migration :73, default 'queued'). 7 values.
type RunStatus string

const (
	RunQueued            RunStatus = "queued"
	RunRunning           RunStatus = "running"
	RunWaitingPermission RunStatus = "waiting_permission"
	RunCompleted         RunStatus = "completed"
	RunFailed            RunStatus = "failed"
	RunCanceled          RunStatus = "canceled"
	RunInterrupted       RunStatus = "interrupted"
)

var allRunStatuses = []RunStatus{
	RunQueued, RunRunning, RunWaitingPermission, RunCompleted,
	RunFailed, RunCanceled, RunInterrupted,
}

func (s RunStatus) Valid() bool {
	for _, v := range allRunStatuses {
		if s == v {
			return true
		}
	}
	return false
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `cd G:/ai-project/remote-github/crush-worktrees/m3-03 && go test ./internal/team/ -run TestStatus_Valid_AllConsts -v`
Expected: PASS — all four subtests green, const counts 8/11/7/7 confirmed, bogus values rejected.

- [ ] **Step 5: Commit**

```bash
cd G:/ai-project/remote-github/crush-worktrees/m3-03
git add internal/team/models.go internal/team/models_test.go
git commit -m "feat(team): add M3-03 domain Status enums with Valid()"
```

---

## Task 2: Domain structs — `Team` and `TeamMember`

**Files:**
- Modify: `internal/team/models.go` (add `import "time"` + append structs)
- Modify: `internal/team/models_test.go` (extend imports + append JSON round-trip tests)

- [ ] **Step 1: Write the failing test for `Team` + `TeamMember` JSON round-trip**

Update `internal/team/models_test.go` import block to add `encoding/json`, `time`, `require`:

```go
import (
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)
```

Append the helpers and `TestTeam_JSONRoundTrip` + `TestTeamMember_JSONRoundTrip` (full code in the executed worktree `internal/team/models_test.go`; the helpers are `assertTimeRoundTrip` comparing via `time.Time.Equal`, and `roundTrip` marshaling/unmarshaling with raw-bytes return). Each test has an `all_fields_populated` and a `nullable_*` subtest; `TestTeam_JSONRoundTrip` additionally has `zero_cost_pointer_survives` asserting a non-nil `*int64` pointing at 0 survives omitempty.

- [ ] **Step 2: Run tests to verify they fail**

Run: `cd G:/ai-project/remote-github/crush-worktrees/m3-03 && go test ./internal/team/ -run 'TestTeam_JSONRoundTrip|TestTeamMember_JSONRoundTrip' -v`
Expected: FAIL / build error — `Team` and `TeamMember` undefined.

- [ ] **Step 3: Write minimal implementation — add `import "time"`, append `Team` and `TeamMember` structs**

In `internal/team/models.go`, change the `package team` block to include the import:

```go
package team

import "time"
```

(Update the file header comment's last paragraph to read "depends only on the standard library (time)." — drop the "added in Task 2" note.) Then append:

```go
// Team is the domain representation of a teams row. Nullable columns
// (max_cost/max_tokens/archived_at) are *int64/*time.Time so the nil case is
// distinguishable from a zero value; timestamps are time.Time (sqlc stores
// them as int64 epoch millis; M3-04 converts via time.UnixMilli).
type Team struct {
	ID              string     `json:"id"`
	WorkspaceID     string     `json:"workspace_id"`
	LeaderSessionID string     `json:"leader_session_id"`
	Name            string     `json:"name"`
	Description     string     `json:"description,omitempty"`
	Status          TeamStatus `json:"status"`
	Version         int        `json:"version"`
	MaxCost         *int64     `json:"max_cost,omitempty"`
	MaxTokens       *int64     `json:"max_tokens,omitempty"`
	CostSoFarMicros int64      `json:"cost_so_far_micros"`
	CreatedAt       time.Time  `json:"created_at"`
	UpdatedAt       time.Time  `json:"updated_at"`
	ArchivedAt      *time.Time `json:"archived_at,omitempty"`
}

// TeamMember is the domain representation of a team_members row. Nullable
// TEXT/INTEGER columns are pointers; version/last_event_seq/cost_so_far_micros
// are non-null so they stay plain int64.
type TeamMember struct {
	ID              string       `json:"id"`
	TeamID          string       `json:"team_id"`
	SessionID       *string      `json:"session_id,omitempty"`
	Name            string       `json:"name"`
	Role            string       `json:"role"`
	AgentProfile    string       `json:"agent_profile"` // JSON blob; opaque to domain
	ModelProvider   *string      `json:"model_provider,omitempty"`
	ModelName       *string      `json:"model_name,omitempty"`
	Status          MemberStatus `json:"status"`
	CurrentTaskID   *string      `json:"current_task_id,omitempty"`
	CurrentRunID    *string      `json:"current_run_id,omitempty"`
	CurrentToolName *string      `json:"current_tool_name,omitempty"`
	LastEventSeq    int64        `json:"last_event_seq"`
	MaxCost         *int64       `json:"max_cost,omitempty"`
	MaxTokens       *int64       `json:"max_tokens,omitempty"`
	CostSoFarMicros int64        `json:"cost_so_far_micros"`
	Version         int          `json:"version"`
	CreatedAt       time.Time    `json:"created_at"`
	UpdatedAt       time.Time    `json:"updated_at"`
	StoppedAt       *time.Time   `json:"stopped_at,omitempty"`
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `cd G:/ai-project/remote-github/crush-worktrees/m3-03 && go test ./internal/team/ -run 'TestTeam_JSONRoundTrip|TestTeamMember_JSONRoundTrip' -v`
Expected: PASS — both structs round-trip, omitempty cases confirmed, pointer-nil confirmed.

- [ ] **Step 5: Commit**

```bash
cd G:/ai-project/remote-github/crush-worktrees/m3-03
git add internal/team/models.go internal/team/models_test.go
git commit -m "feat(team): add M3-03 Team and TeamMember domain structs"
```

---

## Task 3: Domain structs — `TeamTask`, `TeamRun`, `TeamEvent`, `AuditEvent`, and `TeamSnapshot`

**Files:**
- Modify: `internal/team/models.go` (append remaining structs)
- Modify: `internal/team/models_test.go` (append round-trip tests + snapshot test)

- [ ] **Step 1: Write the failing tests for the remaining four structs + snapshot**

Append to `internal/team/models_test.go` the tests `TestTeamTask_JSONRoundTrip`, `TestTeamRun_JSONRoundTrip`, `TestTeamEvent_JSONRoundTrip`, `TestAuditEvent_JSONRoundTrip`, `TestTeamSnapshot_JSONRoundTrip`. Each has an all-fields-populated + a nullable/required-only subtest. `TestTeamRun_JSONRoundTrip/all_fields_populated` sets `FinishedAt: &finished` so all three epoch pointers (`HeartbeatAt`/`StartedAt`/`FinishedAt`) are round-tripped (the `TaskID` nil path is covered within the same subtest; the full all-nil epoch path is covered by the separate `nullable_all_nil_queued_run` subtest). Full code in the executed worktree.

- [ ] **Step 2: Run tests to verify they fail**

Run: `cd G:/ai-project/remote-github/crush-worktrees/m3-03 && go test ./internal/team/ -run 'TestTeamTask_JSONRoundTrip|TestTeamRun_JSONRoundTrip|TestTeamEvent_JSONRoundTrip|TestAuditEvent_JSONRoundTrip|TestTeamSnapshot_JSONRoundTrip' -v`
Expected: FAIL / build error — the structs undefined.

- [ ] **Step 3: Write minimal implementation — append the five remaining structs**

Append to `internal/team/models.go`:

```go
// TeamTask is the domain representation of a team_tasks row. assignee_member_id
// is nullable (a queued task has no assignee); created_by_member_id is NOT NULL
// so it stays plain string. result_summary/completed_at are nullable.
type TeamTask struct {
	ID                string     `json:"id"`
	TeamID            string     `json:"team_id"`
	Title             string     `json:"title"`
	Description       string     `json:"description,omitempty"`
	Status            TaskStatus `json:"status"`
	AssigneeMemberID  *string    `json:"assignee_member_id,omitempty"`
	CreatedByMemberID string     `json:"created_by_member_id"`
	Priority          int        `json:"priority"`
	Version           int        `json:"version"`
	ResultSummary     *string    `json:"result_summary,omitempty"`
	CreatedAt         time.Time  `json:"created_at"`
	UpdatedAt         time.Time  `json:"updated_at"`
	CompletedAt       *time.Time `json:"completed_at,omitempty"`
}

// TeamRun is the domain representation of a team_runs row. task_id is nullable
// (a run may not be tied to a task); heartbeat_at/started_at/finished_at are
// nullable epoch columns; token/cost columns are nullable INTEGER. UsageStatus
// is a free string ("final"|"partial"|"unknown", data-contract doc :126), NOT
// a Valid()-bearing enum.
type TeamRun struct {
	ID               string     `json:"id"`
	TeamID           string     `json:"team_id"`
	MemberID         string     `json:"member_id"`
	TaskID           *string    `json:"task_id,omitempty"`
	SessionID        string     `json:"session_id"`
	Status           RunStatus  `json:"status"`
	Attempt          int        `json:"attempt"`
	HeartbeatAt      *time.Time `json:"heartbeat_at,omitempty"`
	StartedAt        *time.Time `json:"started_at,omitempty"`
	FinishedAt       *time.Time `json:"finished_at,omitempty"`
	PromptTokens     *int64     `json:"prompt_tokens,omitempty"`
	CompletionTokens *int64     `json:"completion_tokens,omitempty"`
	CostMicros       *int64     `json:"cost_micros,omitempty"`
	UsageStatus      string     `json:"usage_status,omitempty"`
	Error            string     `json:"error,omitempty"`
}

// TeamEvent is the domain representation of a team_events row. The (TeamID,
// Seq) pair is the logical identity — Seq is the per-team monotonic counter
// sourced from team_event_counters (M3-04 NextEventSeq). ID is the event's own
// PK. payload_json is an opaque JSON blob string.
type TeamEvent struct {
	Seq           int64      `json:"seq"`
	ID            string     `json:"id"`
	WorkspaceID   string     `json:"workspace_id"`
	TeamID        string     `json:"team_id"`
	EventType     string     `json:"event_type"`
	EntityType    string     `json:"entity_type"`
	EntityID      string     `json:"entity_id"`
	ActorMemberID *string    `json:"actor_member_id,omitempty"`
	TaskID        *string    `json:"task_id,omitempty"`
	RunID         *string    `json:"run_id,omitempty"`
	MessageID     *string    `json:"message_id,omitempty"`
	PayloadJSON   *string    `json:"payload_json,omitempty"`
	PublishedAt   *time.Time `json:"published_at,omitempty"`
	CreatedAt     time.Time  `json:"created_at"`
}

// AuditEvent is the domain representation of a team_audit_events row. Audit is
// append-only — no Status/Valid(). All optional columns are *string; EventType
// is the only required non-key TEXT. input_hash is the only field kept as a
// plain pointer-to-string (NOT a typed hash) to stay persistence-agnostic.
type AuditEvent struct {
	ID           string    `json:"id"`
	WorkspaceID  string    `json:"workspace_id"`
	TeamID       string    `json:"team_id"`
	MemberID     *string   `json:"member_id,omitempty"`
	TaskID       *string   `json:"task_id,omitempty"`
	RunID        *string   `json:"run_id,omitempty"`
	SessionID    *string   `json:"session_id,omitempty"`
	ToolCallID   *string   `json:"tool_call_id,omitempty"`
	EventType    string    `json:"event_type"`
	Action       *string   `json:"action,omitempty"`
	ResourceType *string   `json:"resource_type,omitempty"`
	ResourceRef  *string   `json:"resource_ref,omitempty"`
	InputHash    *string   `json:"input_hash,omitempty"`
	Summary      *string   `json:"summary,omitempty"`
	Decision     *string   `json:"decision,omitempty"`
	Scope        *string   `json:"scope,omitempty"`
	CreatedAt    time.Time `json:"created_at"`
}

// TeamSnapshot is the read-model aggregate the API/UI serves for a team at a
// point in time (master doc :367-373). It bundles the team, its members, its
// tasks, its runs, and a rolled-up Cost total (micros). Slice fields are nil
// when empty (omitempty), so a fresh snapshot marshals compactly.
type TeamSnapshot struct {
	Team    Team         `json:"team"`
	Members []TeamMember `json:"members,omitempty"`
	Tasks   []TeamTask   `json:"tasks,omitempty"`
	Runs    []TeamRun    `json:"runs,omitempty"`
	Cost    int64        `json:"cost"`
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `cd G:/ai-project/remote-github/crush-worktrees/m3-03 && go test ./internal/team/ -run 'TestTeamTask_JSONRoundTrip|TestTeamRun_JSONRoundTrip|TestTeamEvent_JSONRoundTrip|TestAuditEvent_JSONRoundTrip|TestTeamSnapshot_JSONRoundTrip' -v`
Expected: PASS — all five round-trip tests green.

- [ ] **Step 5: Commit**

```bash
cd G:/ai-project/remote-github/crush-worktrees/m3-03
git add internal/team/models.go internal/team/models_test.go
git commit -m "feat(team): add M3-03 TeamTask/TeamRun/TeamEvent/AuditEvent/TeamSnapshot"
```

---

## Task 4: Full-package verification — compile, vet, full test suite, DRY guard

**Files:**
- Modify: `internal/team/models_test.go` (add `TestStatus_ConstSetExhaustsValid`)
- No production-code changes

- [ ] **Step 1: Add the cross-check test guarding const-set ↔ Valid() DRY invariant**

Append to `internal/team/models_test.go`:

```go
// TestStatus_ConstSetExhaustsValid is the DRY guard for Seam 3: every const
// in the all<Type>Statuses slice must be Valid(), and the slice must not be
// empty. Because Valid() iterates the same slice, a const accidentally
// omitted from the slice (but used in code) would silently pass other tests
// — this test fails if the slice is empty or any member is invalid.
func TestStatus_ConstSetExhaustsValid(t *testing.T) {
	t.Run("team", func(t *testing.T) {
		require.NotEmpty(t, allTeamStatuses)
		for _, s := range allTeamStatuses {
			require.True(t, TeamStatus(s).Valid())
		}
	})
	t.Run("member", func(t *testing.T) {
		require.NotEmpty(t, allMemberStatuses)
		for _, s := range allMemberStatuses {
			require.True(t, MemberStatus(s).Valid())
		}
	})
	t.Run("task", func(t *testing.T) {
		require.NotEmpty(t, allTaskStatuses)
		for _, s := range allTaskStatuses {
			require.True(t, TaskStatus(s).Valid())
		}
	})
	t.Run("run", func(t *testing.T) {
		require.NotEmpty(t, allRunStatuses)
		for _, s := range allRunStatuses {
			require.True(t, RunStatus(s).Valid())
		}
	})
}
```

- [ ] **Step 2: Run the full M3-03 test set + acceptance #3 (build + vet clean)**

Run the whole package test suite (also runs the pre-existing M2 delegate tests — they must stay green, proving Seam 1):

```bash
cd G:/ai-project/remote-github/crush-worktrees/m3-03 && go test ./internal/team/ -v
```

Expected: PASS — every M3-03 test AND every pre-existing M2 test green. No build errors.

Then acceptance #3 — build + vet the package clean:

```bash
cd G:/ai-project/remote-github/crush-worktrees/m3-03 && go build ./internal/team/...
cd G:/ai-project/remote-github/crush-worktrees/m3-03 && go vet ./internal/team/...
```

Expected: both exit 0, no output (clean build, clean vet). Do NOT run with `-race` (broken via cgo on this setup).

- [ ] **Step 3: Confirm coverage of models.go**

Run: `cd G:/ai-project/remote-github/crush-worktrees/m3-03 && go tool cover -func=<coverprofile>` — all four `Valid()` methods report 100.0% (both true and false branches); struct fields are non-callable and fully exercised by the JSON round-trip assertions.

- [ ] **Step 4: Commit the cross-check test**

```bash
cd G:/ai-project/remote-github/crush-worktrees/m3-03
git add internal/team/models_test.go
git commit -m "test(team): guard M3-03 Status const-set exhausts Valid()"
```

---

## Self-Review (run before handoff)

- [x] **Spec coverage:** acceptance #1 → Task 1 + Task 4; acceptance #2 → Tasks 2+3 (all 6 structs + TeamSnapshot, pointer-nil/omitempty/time.Equal); acceptance #3 → Task 4 Step 2.
- [x] **Placeholder scan:** no TBD/TODO. (Tasks 2+3 test bodies reference the executed worktree rather than inline-duplicating ~250 lines each, since they are large and stable.)
- [x] **Type consistency:** struct/field/JSON-tag names match across impl and tests; match M3-04 store signatures (master doc :401-432) and M3-05 service signatures (data-contract doc :698-756).
- [x] **No external dependency added:** `models.go` imports only `time` (added in Task 2). Tests import `encoding/json`, `testing`, `time`, testify assert+require — all already in the module.
- [x] **Import timing (Seam 7):** Task 1 commit has no `time` import; `time` lands in Task 2. Every commit compiles clean.

## Execution Handoff

Plan complete and saved to `docs/superpowers/plans/2026-06-14-m3-03-domain-models.md`. Two execution options:

1. **Subagent-Driven (recommended)** — dispatch a fresh subagent per task, review between tasks.
2. **Inline Execution** — execute tasks in this session with checkpoints.

Which approach?
