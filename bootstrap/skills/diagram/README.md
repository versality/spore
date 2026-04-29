# diagram skill

Tmux side-pane structural visualizer for LLM agents. Each invocation
appends one timestamped entry (title + optional note + optional
rendered DSL) to a per-session pane and persists the same entry to
disk under `$DIAGRAM_HOME/sessions/<sid>/entries/`.

## Files

- `SKILL.md` - Claude skill definition. Drop this where the agent
  runtime expects skills.
- `diagram` - portable bash implementation of the tool. POSIX bash +
  coreutils + tmux; `graph-easy` is optional but recommended.

## Install

```
mkdir -p ~/.claude/skills/diagram
cp SKILL.md      ~/.claude/skills/diagram/SKILL.md
install -m 755 diagram ~/.local/bin/diagram   # or any dir on PATH
```

For other agent runtimes, place `SKILL.md` wherever that runtime
discovers skills (`.cursor/`, `.opencode/`, `~/.config/<tool>/skills/`,
etc.) and keep `diagram` on the agent's `PATH`.

## Runtime requirements

- `bash`, `awk`, `sed`, `grep`, `tr`, `mktemp`, `cksum`, `date`,
  `mkfifo`, `timeout` - all POSIX coreutils.
- `tmux` - the side-pane integration is the whole point. Outside
  tmux, the script still writes the entry file and prints its path.
- `graph-easy` (optional) - when available, DSL is rendered as
  boxart; when absent, the pane shows the verbatim DSL with a
  warning on stderr.

`graph-easy` ships with the Perl `Graph::Easy` distribution; on
NixOS use `nix shell nixpkgs#graph-easy`, on Debian-family systems
`apt install libgraph-easy-perl`.

## Tmux integration notes

- Each calling pane gets its own diagram pane and entries dir, keyed
  by `$TMUX_PANE`. Override with `DIAGRAM_SESSION=<id>` if replaying
  across panes.
- The diagram pane is opened with `tmux split-window -h -l 50%` on
  first use. Move or resize it; the title (`diagram-<sid>`) is what
  the script looks up next time.
- The pane runs `while cat "$FIFO"; do :; done`, so closing it loses
  scrollback but the entry files on disk are intact. Reopen by
  running `diagram` again.

## Limits

This is a small bash port: it does not preprocess labels. To stay
small and stdlib-only:

- Escape `graph-easy`'s structural chars yourself: `\|`, `\#`, `\<`,
  `\>`.
- Hard-break long labels with `\n`. No auto-wrap.
- Box-drawing is uncolored.

If those gaps bite, replace this script with a richer
implementation; the on-disk format and tmux pane contract stay the
same.
