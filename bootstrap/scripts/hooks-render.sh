#!/usr/bin/env bash
# Render the consumer's claude settings.json by delegating to
# `spore hooks render`, which centralizes the schema, kind filter,
# extras merge, and missing-file policy in internal/hooks/settings.
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

exec spore hooks render --claude-dir "$CLAUDE_DIR"
