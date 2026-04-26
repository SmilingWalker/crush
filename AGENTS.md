# AGENTS.md

This file provides guidance to agents when working with code in this repository.

## Build & Test Commands

```bash
# Build (CGO disabled, green tea GC experimental)
task build
# or: go build -v .

# Run tests with race detector (required by default)
task test
# or: go test -race -failfast ./...

# Run single test package
go test -race -failfast ./internal/agent

# Run specific test
go test -race -failfast ./internal/agent -run TestCoderAgent/simple_test

# Lint (includes custom log capitalization check)
task lint
# or: golangci-lint run --path-mode=abs --config=".golangci.yml" --timeout=5m

# Format
task fmt
# or: gofumpt -w .

# Record VCR test cassettes (re-runs all agent tests)
task test:record
```

## Architecture

```
main.go                            CLI entry point (cobra via internal/cmd)
internal/
  app/app.go                       Top-level wiring: DB, config, agents, LSP, MCP, events
  cmd/                             CLI commands (root, run, login, models, stats, sessions)
  config/
    config.go                      Config struct, context file paths, agent definitions
    load.go                        crush.json loading and validation
    provider.go                    Provider configuration and model resolution
  agent/
    agent.go                       SessionAgent: runs LLM conversations per session
    coordinator.go                 Coordinator: manages named agents ("coder", "task")
    hooked_tool.go                 Decorator that runs PreToolUse hooks before tool execution
    prompts.go                     Loads Go-template system prompts
    templates/                     System prompt templates (coder.md.tpl, task.md.tpl, etc.)
    tools/                         All built-in tools (bash, edit, view, grep, glob, etc.)
      mcp/                         MCP client integration
  hooks/                           Hook engine: runs user shell commands on hook events
    hooks.go                       Decision types, aggregation logic, event constants
    runner.go                      Parallel hook execution, timeout, dedup
    input.go                       Stdin payload builder, env vars, stdout parsing (Crush + Claude Code compat)
  session/session.go               Session CRUD backed by SQLite
  message/                         Message model and content types
  db/                              SQLite via sqlc, with migrations
    sql/                           Raw SQL queries (consumed by sqlc)
    migrations/                    Schema migrations
  lsp/                             LSP client manager, auto-discovery, on-demand startup
  ui/                              Bubble Tea v2 TUI (see internal/ui/AGENTS.md)
  permission/                      Tool permission checking and allow-lists
  skills/                          Skill file discovery and loading
  shell/                           Bash command execution with background job support
  event/                           Telemetry (PostHog)
  pubsub/                          Internal pub/sub for cross-component messaging
  filetracker/                     Tracks files touched per session
  history/                         Prompt history
```

## Critical Non-Obvious Patterns

### Testing
- **VCR Cassettes**: Tests use VCR to record/replay HTTP responses. Test fixtures are in `internal/agent/testdata/TestCoderAgent/{provider}/{test_name}.yaml`
- **Race detection**: Tests MUST use `-race` flag (enforced by Taskfile)
- **Test recording**: `task test:record` deletes and regenerates all VCR cassettes - use when adding new test cases

### Agent Behavior
- **Message queuing**: Agent automatically queues prompts when busy; queued messages process after current request completes
- **Auto-summarization**: Sessions auto-summarize when approaching context window limits (20K buffer for large windows, 20% for small)
- **Loop detection**: Built-in detection stops repeated tool calls (configurable window size and max repeats)
- **MCP initialization**: MUST call `mcp.WaitForInit()` before using MCP tools in non-interactive mode

### Key Patterns
- **Config is a Service**: accessed via `config.Service`, not global state.
- **Tools are self-documenting**: each tool has a `.go` implementation and a
  `.md` description file in `internal/agent/tools/`.
- **System prompts are Go templates**: `internal/agent/templates/*.md.tpl`
  with runtime data injected.
- **Context files**: Crush reads AGENTS.md, CRUSH.md, CLAUDE.md, GEMINI.md
  (and `.local` variants) from the working directory for project-specific
  instructions.
- **Persistence**: SQLite + sqlc. All queries live in `internal/db/sql/`,
  generated code in `internal/db/`. Migrations in `internal/db/migrations/`.
- **Pub/sub**: `internal/pubsub` for decoupled communication between agent,
  UI, and services.
- **Hooks**: User-defined shell commands in `crush.json` that fire before
  tool execution. The engine (`internal/hooks/`) is independent of fantasy
  and agent — it takes inputs, runs commands, returns decisions. The
  `hookedTool` decorator in `internal/agent/hooked_tool.go` wraps tools at
  the coordinator level. Hooks run before permission checks. See
  `HOOKS.md` for the user-facing protocol.
- **CGO disabled**: builds with `CGO_ENABLED=0` and
  `GOEXPERIMENT=greenteagc`.

### Provider Workarounds
- **Media in tool results**: OpenAI, Google, OpenRouter don't support images in tool results. Agent converts media to user messages with attachments for these providers. Anthropic/Bedrock support native media.
- **Cache control**: Anthropic ephemeral caching applied to last 2 messages and last tool by default (disable with `CRUSH_DISABLE_ANTHROPIC_CACHE=1`)

### Code Style Requirements
- **Log capitalization**: ALL log messages MUST start with capital letter (enforced by `scripts/check_log_capitalization.sh`)
  - ✅ `slog.Info("Starting agent")`
  - ❌ `slog.Info("starting agent")`
- **Formatting**: Uses `gofumpt` (stricter than gofmt) and `goimports` (enforced by golangci-lint)

### Configuration
- **Priority order**: `.crush.json` > `crush.json` > `~/.config/crush/crush.json`
- **Data vs config**: Ephemeral data (sessions, logs) in `.crush/` directory, config in separate location
- **Environment overrides**: `CRUSH_GLOBAL_DATA` and `CRUSH_GLOBAL_CONFIG` override default paths

### Build Environment
- **CGO disabled**: All builds use `CGO_ENABLED=0` (enforced in Taskfile)
- **Experimental GC**: Uses `GOEXPERIMENT=greenteagc` for green tea garbage collector
- **Version injection**: Build version injected via ldflags: `-X github.com/charmbracelet/crush/internal/version.Version={{.VERSION}}`

### Architecture Notes
- **LSP integration**: Uses LSP for code context and diagnostics, not just file reading
- **Pub/sub events**: Services communicate via pub/sub (internal/pubsub) with 2-second send timeout
- **Concurrent-safe values**: Uses `csync` package for thread-safe value sharing
- **Session management**: Parent-child session relationships; cannot continue child sessions directly

### Tool Implementation
- **Tool context**: Tools receive `SessionID` and `MessageID` via context for logging and permissions
- **Permission system**: All tool calls go through permission service (can auto-approve for non-interactive)
- **File tracking**: File changes tracked via `filetracker.Service` for history
