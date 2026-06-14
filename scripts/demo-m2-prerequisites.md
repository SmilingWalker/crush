# M2 Live Demo — Prerequisites (not yet met)

The default (`--live`) demo path cannot run end-to-end today. M2 shipped the
runtime + UI + aggregation wiring but **deferred two blockers**. This doc names
them, cites the deferring code, and states the one-line change each needs.

Run `./scripts/demo-m2.sh --smoke` for the runnable mock-backed preview today;
it exercises the same render + aggregate path the live demo will show once the
prerequisites below land.

## Blocker 1: Production `AgentFactory` is not implemented

- **Symbol:** `agent.AgentFactory` interface — `internal/agent/team_call.go:108-113`.
- **Deferring note:** `internal/team/delegate_runner.go:24-27` — *"M1-04 shipped
  the AgentFactory/TurnRunner interfaces but no production implementation. ...
  the real factory (backed by sessionAgent) is a follow-up task."*
- **Consequence:** `DelegateRunner.RunGroup` calls `factory.BuildRunner`
  (`delegate_runner.go:115`) and there is no production type backing it — only
  the test mock in `internal/team/delegate_runner_test.go`. No real LLM-backed
  delegate can be launched.
- **To unblock:** implement a production `AgentFactory` whose `BuildRunner`
  builds a fresh `sessionAgent` per `AgentSpec`, mapping
  `agent.ReadOnlyDelegatePolicy()` (`team_call.go:60`) onto the runner's tool
  config. Scope: a new task (post-M2), touching `internal/agent` + `internal/team`
  wiring — out of M2-06's scope.

## Blocker 2: `Experimental.AgentTeamPreview` config flag is not wired

- **Symbol:** `var delegateUIEnabled atomic.Bool` — `internal/ui/chat/delegate.go:26`.
- **Deferring note:** `internal/ui/chat/delegate.go:18-25` — *"Until the
  Experimental.AgentTeamPreview config flag lands ... the gate is a package-level
  switch so the renderer is inert by default."*
- **Consequence:** the renderer returns `""` whenever the flag is off
  (`delegate.go:285`, `:296`, `:311`). No config field, env var, or CLI flag
  flips it; only the unexported `setDelegateUIEnabledForTest` sets it.
- **To unblock:** add `Experimental.AgentTeamPreview bool` to the config struct,
  then swap the single `delegateUIEnabled.Load()` read for
  `cfg.Experimental.AgentTeamPreview` — the M2-04 comment
  (`delegate.go:22-25`) prescribes this as a one-line change; the rest of the
  renderer is flag-agnostic. `demo-m2.sh`'s `--live` path will then pass the
  flag through (currently it prints a banner instead).

## Status

Neither blocker is in M2's scope. Both are tracked as the natural M3/post-M2
follow-ups. The `--smoke` demo runs today and reflects the enabled-renderer
output via `setDelegateUIEnabledForTest(true)` (same hook the M2-04 tests use).
