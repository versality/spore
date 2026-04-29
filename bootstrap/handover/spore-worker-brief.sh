#!/usr/bin/env bash
# spore-worker-brief: default SPORE_AGENT_BINARY for spore workers on
# an infected NixOS box. Spore exec's this inside a tmux session whose
# cwd is the worker's worktree, with SPORE_TASK_SLUG=<slug> in the env.
#
# We pipe the worker's brief (tasks/<slug>.md) into a headless `claude
# -p` run, tee the transcript to a per-slug log, drop a .done sentinel
# with the exit code, then drop the pane into an interactive claude so
# the operator can attach and keep iterating.
#
# Falls back to interactive claude when the slug or brief is missing,
# so a misconfigured spawn does not strand the operator.
#
# Override knobs:
#   SPORE_WORKER_AGENT     binary to exec (default: claude)
#   SPORE_WORKER_LOG_DIR   per-slug log dir, relative to worktree cwd
#                          (default: .spore/worker; falls back to
#                          /tmp/spore-worker if the cwd is read-only)
set -euo pipefail

slug="${SPORE_TASK_SLUG:-}"
agent="${SPORE_WORKER_AGENT:-claude}"
brief="tasks/${slug}.md"

if [[ -z "$slug" || ! -f "$brief" ]]; then
  echo "spore-worker-brief: no slug or brief at $(pwd)/$brief; dropping to interactive $agent" >&2
  exec "$agent" --dangerously-skip-permissions
fi

logdir="${SPORE_WORKER_LOG_DIR:-.spore/worker}"
if ! mkdir -p "$logdir" 2>/dev/null; then
  logdir="/tmp/spore-worker"
  mkdir -p "$logdir"
fi
log="${logdir}/${slug}.log"
sentinel="${logdir}/${slug}.done"

echo "spore-worker-brief: dispatching $slug from $brief"
echo "spore-worker-brief: log=$log"
echo "---"

set +e
"$agent" --dangerously-skip-permissions -p < "$brief" 2>&1 | tee "$log"
rc=${PIPESTATUS[0]}
set -e

printf '%s rc=%d\n' "$(date -u +%FT%TZ)" "$rc" > "$sentinel"
echo "---"
echo "spore-worker-brief: brief exited rc=$rc (sentinel=$sentinel)"
echo "spore-worker-brief: dropping into interactive $agent. Ctrl+D to end the worker session."

exec "$agent" --dangerously-skip-permissions
