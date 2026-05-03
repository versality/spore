#!/usr/bin/env bash
# Greet the operator on attach to a worker session, then drop to a
# login shell. Used as SPORE_AGENT_BINARY for boxes that do not yet
# run a real claude-code agent per task. The session inherits
# SPORE_TASK_SLUG=<slug> and runs in the worktree cwd.
clear
slug="${SPORE_TASK_SLUG:-unknown}"
cwd="$(pwd)"
host="$(hostname)"

cat <<BANNER
+--------------------------------------------------------------+
|  spore worker                                                |
+--------------------------------------------------------------+
  task    : $slug
  cwd     : $cwd  (worktree)
  host    : $host
+--------------------------------------------------------------+

Welcome to the worker pane for "$slug". This is a git worktree on
the wt/$slug branch. Commit here; the coordinator will merge it into
local main and publish only origin main:main. Do not push worker or
feature branches unless the operator explicitly asks outside the
worker close flow.

Drop to shell (Ctrl-b d to detach, leave running).
BANNER
exec bash --login -i
