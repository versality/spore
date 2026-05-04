#!/usr/bin/env bash
# spore-coordinator-launch: default SPORE_COORDINATOR_AGENT for spore
# coordinators on an infected NixOS box. Spore exec's this in the
# project root with SPORE_COORDINATOR_ROLE pointing at the resolved
# role file (may not exist).
#
# We launch the selected interactive agent, seeding the role file's
# contents as the first user message when the file is readable and
# non-empty. If the agent exits immediately because first login is
# still needed, the coordinator pane stays alive with a clear shell.
#
# Override knobs:
#   SPORE_COORDINATOR_PROVIDER  claude or codex (default: claude)
#   SPORE_COORDINATOR_MODEL     model to pass to the selected CLI
#   SPORE_COORDINATOR_EFFORT    codex effort (default: high)
set -euo pipefail

provider="${SPORE_COORDINATOR_PROVIDER:-claude}"
model="${SPORE_COORDINATOR_MODEL:-}"
effort="${SPORE_COORDINATOR_EFFORT:-high}"
role="${SPORE_COORDINATOR_ROLE:-}"

prompt=()
if [[ -n "$role" && -r "$role" && -s "$role" ]]; then
  prompt=("$(cat "$role")")
fi

case "$provider" in
  claude)
    args=(--dangerously-skip-permissions)
    if [[ -n "$model" ]]; then
      args+=(--model "$model")
    fi
    claude "${args[@]}" "${prompt[@]}" || exec /usr/local/bin/spore-greet-coordinator
    ;;
  codex)
    args=(--dangerously-bypass-approvals-and-sandbox --no-alt-screen --disable apps)
    if [[ -n "$model" ]]; then
      args+=(-m "$model")
    fi
    if [[ -n "$effort" ]]; then
      args+=(-c "model_reasoning_effort=\"$effort\"")
    fi
    codex "${args[@]}" "${prompt[@]}" || exec /usr/local/bin/spore-greet-coordinator
    ;;
  *)
    echo "spore-coordinator-launch: unsupported SPORE_COORDINATOR_PROVIDER=$provider" >&2
    exec /usr/local/bin/spore-greet-coordinator
    ;;
esac
