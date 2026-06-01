# 02 Notion：AgentTeam 目标架构

Source: https://www.notion.so/36e3d57b14178014bb7cd89b3a504711

## Principles

1. Team is an independent domain, not an implementation detail of the `agent` tool.
2. SQLite is the product truth source; pubsub/SSE is notification.
3. Mailbox, task, and audit are separate models; do not put everything in session messages.
4. Runner lifecycle must be separated from agent turn lifecycle.
5. Permission is least-privilege by default; wider scope must be explicit.
6. Local workspace and server/client workspace must share the same contract.
7. UI should be snapshot/reducer-driven and resilient to dropped events.

## Proposed Package

```text
internal/team/
  models.go             Team, Mate, TeamTask, MailboxMessage, TeamEvent
  service.go            Service interface + sql implementation
  sqlstore.go           DB transaction wrapper
  mailbox.go            append/list/mark-delivered/mark-read
  tasks.go              create/update/claim/dependency
  audit.go              audit append/query
  outbox.go             event outbox append/drain
  runner.go             TeamRunner lifecycle
  mate_runner.go        MateRunner loop
  permission_bridge.go  team-aware permission wrapper
  tools.go              team_* tool construction glue
  prompts.go            leader/mate system addendum
  debug.go              snapshot and diagnostics
```

Do not put the domain directly under `internal/agent/tools`; it must be usable
from app, backend, server/client, workspace, UI, and permission layers.

## Domain Models

Core fields:

```go
type Team struct {
    ID              string
    WorkspaceID     string
    LeaderSessionID string
    Name            string
    Description     string
    Status          TeamStatus
    Version         int64
    CreatedAt       int64
    UpdatedAt       int64
    DeletedAt       *int64
}

type Mate struct {
    ID              string
    TeamID          string
    SessionID       string
    Name            string
    Role            string
    AgentProfile    string
    ModelType       string
    Status          MateStatus
    CurrentRunID    string
    CurrentTaskID   string
    CurrentToolName string
    LastEventSeq    int64
    Version         int64
    CreatedAt       int64
    UpdatedAt       int64
    DeletedAt       *int64
}
```

Suggested statuses:

```text
TeamStatus:
  created
  running
  canceling
  completed
  failed
  archived

MateStatus:
  created
  starting
  idle
  queued
  running
  waiting_permission
  blocked
  canceling_turn
  shutting_down
  stopped
  failed
```

`Version` is needed for CAS task claim/update, stale state protection, and UI
cache invalidation.

## DB Schema Areas

Recommended tables:

```text
teams
team_mates
team_tasks
team_task_dependencies
team_mailbox_messages
team_audit_events
team_event_outbox
```

Important schema requirements:

- `team_mates(team_id, name)` has a unique active index.
- `team_tasks` uses `version` and an atomic update for `ClaimNextTask`.
- `team_mailbox_messages` has `kind`, `correlation_id`, `summary`,
  `payload_json`, `delivered_at`, and `read_at`.
- `team_audit_events` records workspace/team/mate/session/tool/action/path/scope.
- `team_event_outbox` has unique `(team_id, seq)` for replay.

Atomic task claim must be a single SQL update, not select-then-update.

## Service Interface

```go
type Service interface {
    pubsub.Subscriber[TeamEvent]

    CreateTeam(ctx context.Context, req CreateTeamRequest) (Team, error)
    GetTeam(ctx context.Context, teamID string) (TeamSnapshot, error)
    ListTeams(ctx context.Context, workspaceID string) ([]Team, error)
    ArchiveTeam(ctx context.Context, teamID string, expectedVersion int64) error

    SpawnMate(ctx context.Context, req SpawnMateRequest) (Mate, error)
    UpdateMateState(ctx context.Context, req UpdateMateStateRequest) (Mate, error)
    StopMate(ctx context.Context, req StopMateRequest) error
    ListMates(ctx context.Context, teamID string) ([]Mate, error)

    AppendMailbox(ctx context.Context, req AppendMailboxRequest) (MailboxMessage, error)
    ListUnread(ctx context.Context, teamID, mateID string, limit int) ([]MailboxMessage, error)
    MarkDelivered(ctx context.Context, ids []string, deliveredAt int64) error
    MarkRead(ctx context.Context, ids []string, readAt int64) error

    CreateTask(ctx context.Context, req CreateTaskRequest) (TeamTask, error)
    UpdateTask(ctx context.Context, req UpdateTaskRequest) (TeamTask, error)
    ClaimNextTask(ctx context.Context, req ClaimNextTaskRequest) (TeamTask, error)
    ListTasks(ctx context.Context, teamID string, filter TaskFilter) ([]TeamTask, error)

    AppendAudit(ctx context.Context, req AuditEvent) error
    DebugSnapshot(ctx context.Context, teamID string) (DebugSnapshot, error)
}
```

Transaction rule: each write operation updates domain rows, audit, and outbox in
one DB transaction. LLM calls and permission waits must not hold transactions.

## TeamEvent Contract

Events need enough identity for UI, replay, and diagnostics:

```go
type TeamEvent struct {
    WorkspaceID       string
    ParentSessionID   string
    TeamID            string
    TeamRunID         string
    MateID            string
    MateSessionID     string
    EventID           string
    Seq               int64
    Type              TeamEventType
    State             string
    PreviousState     string
    Reason            string
    Error             string
    CurrentToolName   string
    CurrentToolCallID string
    PermissionID      string
    QueueDepth        int
    TokensIn          int64
    TokensOut         int64
    Cost              float64
    CreatedAt         int64
}
```

Event type examples:

```text
team.created
team.status_changed
mate.spawned
mate.status_changed
mate.turn_started
mate.tool_started
mate.tool_finished
mate.waiting_permission
mate.turn_finished
mate.failed
mate.stopped
mailbox.message_created
task.created
task.assigned
task.claimed
task.updated
permission.requested
permission.resolved
audit.appended
```

## API / Workspace Contract

Add `internal/proto/team.go`, backend team methods, HTTP routes under
`/v1/workspaces/{id}/teams`, and matching methods for both `AppWorkspace` and
`ClientWorkspace`.

Do not implement only local workspace mode. Server/client mode must round-trip
TeamEvent and snapshot behavior.

## Context Identity

Use a typed identity object rather than scattered string keys:

```go
type TeamIdentity struct {
    WorkspaceID     string
    TeamID          string
    TeamRunID       string
    MateID          string
    MateName        string
    MateRole        string
    MateSessionID   string
    LeaderSessionID string
}
```

Tools, permission bridge, hooks, and audit should read this identity from context.

## Hooks And Shared Services

Team mode should support actor-aware hooks. Default behavior can remain
conservative, but hook payloads should include `CRUSH_TEAM_ID`, `CRUSH_MATE_ID`,
`CRUSH_MATE_ROLE`, and `CRUSH_LEADER_SESSION_ID` when enabled.

Shared services that need review:

- `skillTracker`: may mix stats across mates.
- MCP manager: shared tool state needs actor context.
- LSP manager: read requests can share, restart/write paths need limits.
- job manager: background job ids should be visible in debug snapshot.
- filetracker: per-session read tracking does not prevent cross-mate write conflicts.
