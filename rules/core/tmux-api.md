# tmux

The operator works inside tmux. Treat it as a first-class API: use it both to surface live state to the operator and to drive interactive processes you'd otherwise lose control of. Sub-second one-shots stay in plain `Bash`.

**Launch user-watchable processes** (dev server, log tail, build, `--watch` test runner, REPL, batch job). Prefer this over `run_in_background` whenever the operator should *see* the process. Always pass `-d` so the operator's current view isn't dragged to the new window; they switch on their own time with `Ctrl-b w`. Don't target another session either (no `-t <attached-client>` tricks): the default target is the session you're running in, and that's where the operator expects work tied to this project to appear. Tell them the window name after you launch it:

```
tmux new-window -d -n <short-name> "<cmd>"
```

**Drive an existing window** (feed input to a REPL, restart a watcher, answer a prompt):

```
tmux send-keys -t <name> "<input>" Enter
tmux capture-pane -t <name> -p   # read recent output
```

**Inspect** with `tmux list-windows`. Pick short, descriptive names so the operator can find them (`Ctrl-b w`). Kill with `tmux kill-window -t <name>` when truly done; otherwise leave it for the operator.
