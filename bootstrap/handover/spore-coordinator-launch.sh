#!/usr/bin/env bash
# spore-coordinator-launch: default SPORE_COORDINATOR_AGENT for spore
# coordinators on an infected NixOS box. Spore exec's this in the
# project root with SPORE_COORDINATOR_ROLE pointing at the resolved
# role file (may not exist).
#
# We launch interactive claude with --dangerously-skip-permissions,
# seeding the role file's contents as the first user message when the
# file is readable and non-empty. This is the same role-or-bare
# branching the kernel does in EnsureCoordinator, plus the flag, plus
# wrapper-level overridability.
#
# Override knobs:
#   SPORE_WORKER_AGENT     binary to exec (default: claude). Shared
#                          with spore-worker-brief so a single env
#                          override re-targets both wrappers.
set -euo pipefail

agent="${SPORE_WORKER_AGENT:-claude}"
role="${SPORE_COORDINATOR_ROLE:-}"

if [[ -n "$role" && -r "$role" && -s "$role" ]]; then
  exec "$agent" --dangerously-skip-permissions "$(cat "$role")"
fi

exec "$agent" --dangerously-skip-permissions
