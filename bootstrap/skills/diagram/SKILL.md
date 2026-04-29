---
name: diagram
description: Append an entry (diagram + optional note) to a tmux side-pane. Use for architecture, data flow, call graphs, or any structural explanation. Default to this instead of prose whenever a picture communicates faster, and use note-only entries to log checkpoints or decisions worth keeping visible.
---

# diagram

`diagram` appends one entry to a tmux side-pane. Each entry is a
title + optional note + optional diagram, timestamped. Entries print
chronologically (newest at the bottom); older ones live in tmux
scrollback, reachable with `prefix-[`. Entries also persist on disk in
`$DIAGRAM_HOME/sessions/<session>/entries/`.

Each calling tmux pane gets its own diagram pane and entries dir;
different LLM sessions don't share state. Session key = `$TMUX_PANE`;
override with `DIAGRAM_SESSION=<id>` if replaying across panes.

## When to use

- Structural explanations longer than a paragraph of prose: architecture,
  data flow, call graph, state machine, stages.
- Progress checkpoints and non-obvious decisions (note-only entries work).
- Summarise a finished refactor or investigation.

## Call forms

```
diagram "<title>" <file>         # DSL from file
echo "..." | diagram "<title>"   # DSL from stdin
diagram "<title>" <file> --raw   # copy verbatim (pre-rendered)
```

A line containing only `---` splits an optional note above from the DSL
below. Without `---`: lines starting with `[`, `graph {`, `digraph`, or
`node` are DSL; anything else is treated as a note-only entry.

```
# /tmp/foo.ge
Short explanation of the diagram.
---
[A] -> [B]
[B] -> [C]
```

## DSL

```
[ name ]                          # a node
[ A ] -> [ B ]                    # an edge
[ A ] -- label --> [ B ]          # labeled edge
[ A ] { fill: lightblue; }        # node attribute
graph { flow: east; }             # layout (east/west/south/north)
```

`graph-easy` treats `|`, `#`, `<`, `>` as structural inside node
brackets and edge labels. The bash version of this skill does NOT
preprocess them; escape them yourself with `\|`, `\#`, `\<`, `\>`.
Long labels also do not auto-wrap; keep them under ~18 chars or
insert `\n` for a hard break. Graphviz `dot` syntax also works.

## Authoring tips

- Keep node names short - long labels fragment the layout.
- 6-8 edges per diagram; split into multiple calls when denser.
- Avoid feedback edges (`A -> B` then `B -> A`); they force snake routing.
- Flow direction: `graph { flow: east; }` for left-to-right stories;
  the pane is taller than wide, so the default `south` fits vertical
  flows.

## Failure visibility

If `graph-easy` is missing or can't parse the DSL, the pane shows
verbatim text and the tool prints `(!) diagram: graph-easy failed` (or
`graph-easy not on PATH`) to stderr so the calling agent sees it in
tool output.

## Environment

- `DIAGRAM_SESSION` - override the session id (default: `$TMUX_PANE`).
- `DIAGRAM_HOME` - state dir (default: `~/.local/share/diagram`).

## State

Per-session dirs under `$DIAGRAM_HOME/sessions/`. Delete to reset.
