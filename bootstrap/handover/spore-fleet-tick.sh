#!/usr/bin/env bash
# spore-fleet-tick: periodic reconcile entry point. Walks /home/spore
# one level deep, runs `spore fleet reconcile` inside any project that
# looks like a spore harness (has both `tasks/` and `.git`). Driven by
# the spore-fleet-reconcile.timer systemd user unit so the coordinator
# (and any worker that died unexpectedly) gets respawned within the
# tick interval.
#
# Idempotent: each reconcile is itself idempotent, and a missing
# kill-switch flag short-circuits the call.
set -euo pipefail

shopt -s nullglob
for dir in "$HOME"/*/; do
    if [[ -d "$dir/tasks" && -e "$dir/.git" ]]; then
        echo "spore-fleet-tick: reconciling $dir"
        ( cd "$dir" && /usr/local/bin/spore fleet reconcile )
    fi
done
