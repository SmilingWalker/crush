# 06 Notion：辩证设计评审

Source: https://www.notion.so/36e3d57b14178076980fe23984de7fee

## Review Perspectives

| Perspective | Focus | Conclusion |
| --- | --- | --- |
| Runtime/Agent | `SessionAgent`, `Coordinator`, runner lifecycle | Do not put TeamRunner into `currentAgent`; extract narrow turn-runner ability. |
| Data/API/Event | schema, service, proto, SSE | Team needs its own domain; do not use `sessions.todos` or message parts. |
| Protocol/Permission/Tools | mailbox, permission, tool protocol | DB-backed mailbox is required for product mode; permission must be team-aware. |
| TUI/Product/Test | minimum UI, tests | First version should be compact team item, not command center. |
| Opposing architecture review | MVP complexity | Prove long-lived mate with in-memory runner spike before DB productization. |
| Implementation review | touched files | Proceed by service, proto, tools, runner, permission, TUI layers. |

## Debate: DB-Backed Mailbox On Day One

Pro:

- Crush already has SQLite/sqlc.
- Server/client mode needs unified API.
- TUI needs snapshot/replay.
- Permission pending needs audit.
- Task claim needs CAS.

Con:

- Long-lived mate lifecycle is the hardest unknown.
- Premature schema can lock in the wrong runtime model.
- Ack/retry/outbox/task lease can turn MVP into a scheduler platform.

Decision:

1. Phase 0 uses in-memory mailbox for hidden runtime spike.
2. Product MVP must be DB-backed.

## Debate: TeamRunner In Coordinator Or `internal/team`

Coordinator is tempting because provider/tool/model construction lives nearby.
But Team crosses DB, proto, backend, workspace, TUI, and permission. It would
pollute `currentAgent` semantics around `CancelAll`, `IsBusy`, `Model`, and
`QueuedPrompts`.

Decision: Team lifecycle belongs in `internal/team`. Coordinator offers a narrow
agent/turn-runner factory capability.

## Debate: Direct Chat Or Typed Protocol

Directly writing leader output into mate session and mate output into leader
session fails because:

- normal output is not protocol
- task status, permission request, and shutdown are ambiguous
- no recipient/read/ack
- no reliable TUI/audit aggregation

Decision: leader/mate use typed mailbox plus task board. "Email" is only a
mental model; engineering needs kind, correlation, ack, version, and audit.

## Debate: Use A2A Now

A2A is attractive because it is standardized and supports future external
agents. But it does not replace local runtime, SQLite domain, tool permission,
message flush, or TUI reducer.

Decision: Phase 9 A2A gateway; Phase 0-8 native `internal/team`.

## Debate: User Or Leader Approves Permission

Leader approval increases autonomy but lets a model grant file-write or shell
permissions. That is too risky by default.

Decision: user/UI approves by default. Leader model approval is future explicit
opt-in with scope/path/tool/action limits, expiry, audit, and strong UI warning.

## Debate: Dashboard Or Compact UI

Dashboard would show more, but early event contracts and layout will be brittle.

Decision: first version compact item:

```text
Team: parser-migration | 2 running / 1 blocked | $0.23 | last: researcher waiting write permission
  researcher | waiting_permission | write | 01:12 | foo.go
  tester     | running            | grep  | 00:38 | parser tests
```

## Debate: Automatic Multi-Mate

Automatic concurrency is the promise of "team", but it amplifies permission
storms, write conflicts, TUI noise, and task races.

Decision: single mate E2E first. Multi-mate requires task CAS, permission queue
limits, event seq/replay, file conflict detection, and debug snapshot.

## Final Judgment

Do not copy Claude Code Team Mode mechanically and do not directly wrap A2A.
Evolve from Crush's own skeleton:

```text
SessionAgent turn runner
  + SQLite/sqlc domain
  + pubsub/SSE/workspace contract
  + permission bridge
  + compact TUI
  + later process/A2A adapter
```
