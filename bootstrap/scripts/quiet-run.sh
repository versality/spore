#!/usr/bin/env bash
# quiet-run.sh <label> <cmd...>
# QUIET_SUCCESS=1: capture output, print nothing on exit 0,
#                  print full captured output on non-zero exit.
# QUIET_SUCCESS unset/empty: passthrough (exec the command directly).
#
# When the label starts with "build", the run takes a per-user flock
# at "$XDG_RUNTIME_DIR/spore-build.lock" so concurrent worker fleets
# cannot fan out into N parallel builds. Nested invocations inherit
# the lock fd and skip via SPORE_BUILD_LOCK_HELD=1.
set -euo pipefail

label="$1"
shift

case "$label" in
build | build\ *)
	if [[ -z "${SPORE_BUILD_LOCK_HELD:-}" ]]; then
		lock_dir="${XDG_RUNTIME_DIR:-/tmp}"
		mkdir -p "$lock_dir"
		lock_file="$lock_dir/spore-build.lock"
		exec {build_lock_fd}>"$lock_file"
		if ! flock -n "$build_lock_fd"; then
			echo "[quiet-run] $label: another build holds $lock_file; queuing" >&2
			flock "$build_lock_fd"
		fi
		export SPORE_BUILD_LOCK_HELD=1
	fi
	;;
esac

if [[ "${QUIET_SUCCESS:-}" != "1" ]]; then
	exec "$@"
fi

tmp=$(mktemp)
trap 'rm -f "$tmp"' EXIT

if "$@" >"$tmp" 2>&1; then
	exit 0
else
	rc=$?
	cat "$tmp"
	exit "$rc"
fi
