Rule-pool fragments. Markdown files here are composed into a project's
`CLAUDE.md` / `AGENTS.md` by `internal/composer`. `core/` holds always-on,
language-agnostic rules; `lang/` holds language-specific fragments
selected per project (`lang/` is empty until per-language fragments
land). Per-consumer rule lists live in `consumers/<name>.txt`, one
fragment id per line.
