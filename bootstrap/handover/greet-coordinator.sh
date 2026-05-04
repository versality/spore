#!/usr/bin/env bash
# Greet the operator on attach to the coordinator session, then drop
# to a login shell. Used as SPORE_COORDINATOR_AGENT for boxes that do
# not yet run a real claude-code agent. The session inherits
# SPORE_TASK_SLUG=coordinator from EnsureCoordinator.
clear
project="${SPORE_PROJECT_ROOT:-$(pwd)}"
slug="${SPORE_TASK_SLUG:-coordinator}"
host="$(hostname)"
ip="$(hostname -I 2>/dev/null | awk '{print $1}')"
nixver="$(nixos-version 2>/dev/null)"
fleet_state="disabled"
[ -e "${XDG_STATE_HOME:-$HOME/.local/state}/spore/fleet-enabled" ] && fleet_state="enabled"
agent_provider="${SPORE_COORDINATOR_PROVIDER:-claude}"
agent_model="${SPORE_COORDINATOR_MODEL:-cli-default}"
agent_effort="${SPORE_COORDINATOR_EFFORT:-high}"
active=0
total=0
if [ -d "$project/tasks" ]; then
    active=$(grep -lE '^status:[[:space:]]*active' "$project"/tasks/*.md 2>/dev/null | wc -l)
    total=$(ls "$project"/tasks/*.md 2>/dev/null | wc -l)
fi

cat <<BANNER
+--------------------------------------------------------------+
|  spore coordinator                                           |
+--------------------------------------------------------------+
  project : $(basename "$project")  (cwd: $project)
  host    : $host  ($ip)
  os      : $nixver
  role    : $slug
  agent   : $agent_provider  model: $agent_model  effort: $agent_effort
  fleet   : $fleet_state
  tasks   : $active active / $total total
+--------------------------------------------------------------+

Welcome. You are attached to the singleton coordinator session.
Spawn / inspect tasks with:
  spore task ls
  spore task new "<title>"
  spore task start <slug>
  spore fleet status

Drop to shell below; the session stays alive (Ctrl-b d to detach).
If this pane appeared after an agent login failure, run the selected
agent's login command, then run:
  spore fleet reconcile
BANNER
exec bash --login -i
