#!/usr/bin/env bash
# report-main-worktree-dirty.sh [root]
# Print a one-shot diagnostic to stderr when the main checkout has
# local changes. Used by `wt merge` post-merge probes; worker merges do
# not own the main checkout's working tree.
#
# Always exits 0. Defaults root to `git rev-parse --show-toplevel`.
set -euo pipefail

root="${1:-$(git rev-parse --show-toplevel)}"
status="$(git -C "$root" status --short --untracked-files=all)"
[[ -n "$status" ]] || exit 0

echo "[main-worktree-dirty] coordinator/operator checkout has local changes:" >&2
printf '%s\n' "$status" | head -30 | sed 's/^/[main-worktree-dirty]   /' >&2
count="$(printf '%s\n' "$status" | wc -l | tr -d ' ')"
if [[ "$count" -gt 30 ]]; then
	echo "[main-worktree-dirty]   ... $((count - 30)) more path(s)" >&2
fi
echo "[main-worktree-dirty] worker merges do not own this dirt; inspect or clean: git -C $root status --short" >&2
