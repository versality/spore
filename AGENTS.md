<!-- generated from rules/consumers/spore.txt; edit fragments, not this file. -->

# spore

spore is a drop-in harness template for LLM-coding agents.

## Roles

spore uses `dispatcher` (coordinator) and `runner` (worker) internally.
Downstream projects pick their own names during bootstrap; the kernel
parameterizes both. When working in this repo, use the internal names.

## Source map

```
spore/
|-- cmd/spore/        CLI entry point (Go).
|-- internal/         Go internal packages, kernel implementation.
|   |-- align/        Pilot-agent alignment-mode tracker.
|   |-- bootstrap/    Stage-gate driver + per-stage detectors.
|   |-- composer/     Instruction composer: rule-pool to rendered files.
|   |-- fleet/        Worker fleet: coordinator + workers consuming the task queue.
|   |-- hooks/        Stop / PreToolUse / commit-msg hook entry points.
|   |-- infect/       nixos-anywhere wrapper for `spore infect`.
|   |-- install/      Drops embedded skills into a target's .claude/skills/.
|   |-- lints/        Portable lint set (drift, file-size, comment-noise, em-dash).
|   `-- task/         Worktree-task driver.
|-- rules/            Markdown rule pool, composed into CLAUDE.md / AGENTS.md.
|   |-- consumers/    Per-consumer rule lists (line per fragment id).
|   |-- core/         Always-on, language-agnostic fragments.
|   `-- lang/         Language-specific fragments (later phase).
|-- bootstrap/        spore-bootstrap skill body, stage runbooks, drop-ins.
|   |-- skills/       spore-bootstrap and diagram skills.
|   |-- stages/       One runbook per stage gate.
|   |-- mcp/          MCP server config templates.
|   `-- flake/        Minimal NixOS flake used by `spore infect`.
`-- docs/             Design notes, rationale, multi-session specs.
```

## Tier policy

Rules tier into root `CLAUDE.md` / `AGENTS.md` (project-wide), subdir
instruction mirrors (single-area, under 150 lines), `docs/<topic>.md`
(rationale and debugging notes), and `docs/todo/<slug>.md`
(multi-session specs, each starting with a `**Status**:` header). Test
for an inline comment:
would deleting it confuse a reader of the surrounding code plus loaded
rules? If no, drop it. Default to no comment.

## Writing style

- ASCII only.
- No em-dashes. Use a hyphen, a colon, parentheses, or a new sentence
  instead. No en-dashes either.
- No emojis.
- No `Co-Authored-By` or `Generated with Claude` trailers in commits.
  Write commit messages as the human author.
- Short, declarative, imperative voice in rules. Use "you" or the
  bare imperative.

# Reply shape

**Lead with the answer; brief over thorough; expand on request.** The operator reads top-down and stops when satisfied. Long-form replies bury the conclusion and burn their attention.

- One-sentence lead. State the conclusion or the action.
- 1-3 supporting bullets only when they sharpen the answer.
- Offer expansion ("want the full breakdown?") instead of doing it.
- Reserve long-form for replies the operator explicitly needs detail on: design walkthrough, debug trace, code review.

Don't summarize the question back. Don't enumerate when one sentence works. Don't preface ("Sure, I can help with..."). The bar: same content, single sentence plus a follow-up offer.

## Validation

Spore self-validates with the same lint set it ships: drift,
agent mirrors, file-size, comment-noise, em-dash. Run `spore lint` plus
`go test ./...` before push; both must be green.

## Worker etiquette

- Source edits stay inside the spore tree. Do not leak into a consumer
  project's working copy, even when dogfooding the bootstrap flow.
- Do not rename `dispatcher` or `runner` without updating the
  composer plus its tests in the same commit. The names are
  kernel-internal contract; silent drift breaks downstream rendering.
- Opensource-bound. Mind the leak surface: no internal hostnames, no
  operator-machine paths, no personal email beyond what
  `git config user.email` resolves to.

## Alignment mode

Alignment mode is on. You and the pilot are still learning to work
together. Keep things small and slow on purpose until you flip out.

- Use plain words. Short sentences. No jargon. If a word might be
  unknown to a pilot new to this project, use a simpler one or
  explain it in one line.
- Ask one question at a time. Do not bundle. If you have three
  questions, ask the first, wait, then the next.
- When you ask, reach for the `AskUserQuestion` tool by default.
  Most pilots are devs but they still pick faster from a short
  list of pre-thought options than from a wall of prose. Use a
  free-form prompt only when the question is open and choices do
  not fit (clarifying intent, naming, scope).
- Take the heavy lifting. Do not hand the pilot a blank prompt.
  Surface 2 to 4 options you already thought through. Pick a
  recommendation and say why. Let the pilot redirect.
- Say what you are about to do before you do it, when the action
  is not trivial. One line: "I am about to do X because Y. OK?"
  Trivial reads do not need this.
- Watch for pilot preferences. When you notice one ("I prefer
  small commits", "do not touch generated files", "ask before
  installing deps"), log it. Append one short bullet to
  `~/.local/state/spore/<project>/alignment.md`. Use
  `spore align note "<line>"`.
- When a preference comes up more than once, suggest promoting
  it to a rule-pool entry: "I noticed you prefer X twice now.
  Should we make this a rule?" If the pilot agrees and a rule is
  added, mark the note `[promoted]` (run `spore align note
  "[promoted] <text>"`).
- Each turn, glance at `spore align status` and surface progress
  in one short line: "alignment: 4 of 10 notes, 1 of 3 promoted,
  flip pending".

You exit alignment mode when all three are true:

1. There are at least 10 notes in `alignment.md`.
2. At least 3 of them are marked `[promoted]`.
3. The pilot runs `spore align flip`.

Defaults are configurable per project via `spore.toml`
(`[align]` section). Once you flip out, the next composer render
drops this block from the instruction files.
