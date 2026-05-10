#!/usr/bin/env bash
# Stage + commit drift in tasks/*.md via `spore task drift`.
# Idempotent: noop if nothing changed. Scoped to tasks/.
#
# Usage: auto-commit-tasks.sh <repo-root>
set -euo pipefail

repo="${1:-}"
[[ -n "$repo" ]] || {
	echo "usage: auto-commit-tasks.sh <repo-root>" >&2
	exit 2
}
[[ -e "$repo/.git" ]] || {
	echo "not a git repo: $repo" >&2
	exit 2
}

cd "$repo"
[[ -d tasks ]] || exit 0

state="${WT_STATE:-$HOME/.local/state/wt}"
mkdir -p "$state"
lock="$state/merge-$(basename "$repo" | tr '/' '-').lock"
exec 9>"$lock"
flock -w 30 9 || exit 0

exec spore task drift
