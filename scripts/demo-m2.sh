#!/usr/bin/env bash
# M2 delegate-runner end-to-end demo launcher.
#
# Two modes:
#   ./scripts/demo-m2.sh --smoke   Run TODAY: mock-backed smoke preview
#                                 (builds crush, runs TestDelegateDemoSmoke,
#                                 prints render skeleton + AggregateResults).
#   ./scripts/demo-m2.sh           Live demo path. Needs two prerequisites
#   ./scripts/demo-m2.sh --live    (see scripts/demo-m2-prerequisites.md);
#                                 until they land it prints the prompt to
#                                 paste + a prerequisite banner.
#
# No bashisms beyond what git-bash on win32 supports (set -euo pipefail,
# forward slashes, /dev/null). Portable.

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "$SCRIPT_DIR/.." && pwd)"
MODE="${1:---live}"

print_prompt() {
    cat <<'PROMPT'
Demo prompt (paste into crush once prerequisites are met):

    Search the entire codebase for:
    a) All SQL migration files and their schemas
    b) All HTTP API route definitions
    c) All Go interface definitions
    Use 3 delegates in parallel.

See scripts/demo-m2-task.md for the T1-T4 expected behaviors and the
verification checklist.
PROMPT
}

case "$MODE" in
    --smoke)
        echo "[M2 DEMO] building crush (go build .)..."
        (cd "$REPO_ROOT" && go build -o /dev/null .)
        echo "[M2 DEMO] running mock-backed smoke preview..."
        (cd "$REPO_ROOT" && go test ./internal/team/ -run '^TestDelegateDemoSmoke$' -v)
        echo "[M2 DEMO] smoke done. For the live demo, see scripts/demo-m2-prerequisites.md."
        ;;
    --live|"")
        echo "[M2 DEMO] LIVE DEMO — PREREQUISITES NOT MET."
        echo "[M2 DEMO] Two blockers must clear first (see scripts/demo-m2-prerequisites.md):"
        echo "[M2 DEMO]   1. Production AgentFactory (internal/agent/team_call.go:111)"
        echo "[M2 DEMO]   2. Experimental.AgentTeamPreview config flag (internal/ui/chat/delegate.go:26)"
        echo "[M2 DEMO] Run './scripts/demo-m2.sh --smoke' for the runnable preview today."
        echo
        print_prompt
        ;;
    *)
        echo "usage: $0 [--smoke|--live]" >&2
        exit 2
        ;;
esac
