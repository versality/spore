#!/usr/bin/env bash
# Login shell for the spore operator user. Attaches to the project
# coordinator tmux session; never drops to a prompt. The session
# itself runs the configured agent (claude-code or a greet wrapper),
# so the operator's interactive surface is whatever that agent
# exposes, nothing more.
#
# Detach (Ctrl-b d) ends the SSH session: the shell exits because
# there is nothing to fall through to. To do anything privileged,
# SSH in as root instead.
set -e

# pick the singleton coordinator. Non-zero / non-one matches mean
# something is off; surface and exit. tmux -F prints just the session
# name (no trailing window count / created stamp), so the grep anchor
# matches the actual session name.
sessions=$(tmux ls -F '#{session_name}' 2>/dev/null | grep '/coordinator$' || true)
n=$(printf '%s' "$sessions" | grep -c . || true)

case "$n" in
    0)
        echo "spore-attach: no coordinator session running on $(hostname)." >&2
        echo "spore-attach: ask the operator to run 'spore fleet enable" >&2
        echo "              && spore fleet reconcile' as the spore user." >&2
        exit 1
        ;;
    1)
        exec tmux attach -t "$sessions"
        ;;
    *)
        echo "spore-attach: multiple coordinator sessions present:" >&2
        printf '  %s\n' $sessions >&2
        echo "spore-attach: ssh in as root to reconcile." >&2
        exit 1
        ;;
esac
