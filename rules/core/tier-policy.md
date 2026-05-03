## Tier policy

Rules tier into root `CLAUDE.md` / `AGENTS.md` (project-wide), subdir
instruction mirrors (single-area, under 150 lines), `docs/<topic>.md`
(rationale and debugging notes), and `docs/todo/<slug>.md`
(multi-session specs, each starting with a `**Status**:` header). Test
for an inline comment:
would deleting it confuse a reader of the surrounding code plus loaded
rules? If no, drop it. Default to no comment.
