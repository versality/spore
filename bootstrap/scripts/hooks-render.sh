#!/usr/bin/env bash
# Render the consumer's claude settings.json by piping
# hooks-config.json through `spore hooks settings` and merging the
# result with settings-extras.json.
#
# Claude config dir resolution:
#   $SPORE_HOOKS_CLAUDE_DIR (if set), else <repo>/configs/claude.
# The repo root is derived from the script's parent directory
# (harness/ -> repo root) or $SPORE_HOOKS_REPO if set.
set -euo pipefail

if ! command -v spore >/dev/null 2>&1; then
	echo "hooks-render: spore not on PATH" >&2
	exit 1
fi
if ! command -v jq >/dev/null 2>&1; then
	echo "hooks-render: jq not on PATH" >&2
	exit 1
fi

if [[ -n "${SPORE_HOOKS_CLAUDE_DIR:-}" ]]; then
	CLAUDE_DIR="$SPORE_HOOKS_CLAUDE_DIR"
else
	if [[ -n "${SPORE_HOOKS_REPO:-}" ]]; then
		PROJ_DIR="$SPORE_HOOKS_REPO"
	else
		SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
		PROJ_DIR="$(cd "$SCRIPT_DIR/.." && pwd)"
	fi
	CLAUDE_DIR="$PROJ_DIR/configs/claude"
fi

[[ -f "$CLAUDE_DIR/hooks-config.json" ]] || {
	echo "hooks-render: missing $CLAUDE_DIR/hooks-config.json" >&2
	exit 2
}
[[ -f "$CLAUDE_DIR/settings-extras.json" ]] || {
	echo "hooks-render: missing $CLAUDE_DIR/settings-extras.json" >&2
	exit 2
}

hooks_json=$(spore hooks settings <"$CLAUDE_DIR/hooks-config.json")

jq -s '.[0] * .[1]' \
	<(echo "$hooks_json") \
	"$CLAUDE_DIR/settings-extras.json" \
	>"$CLAUDE_DIR/settings.json"
