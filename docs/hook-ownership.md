# Hook ownership

Spore keeps hook ownership split between source files and runtime files.

## Source files

`configs/claude/hooks-config.json` and `configs/codex/hooks-config.json`
are user-owned source files. `spore install` creates them when they are
missing and preserves existing files. Spore does not merge managed ids
into vendor hook entries.

Spore-managed lifecycle commands are listed in
`internal/lifecyclehooks`. `spore doctor` checks that the source files
still contain those commands with the expected driver, event, timeout,
and kinds. Extra user hooks are allowed.

## Runtime files

Spore spawners generate runtime hook files for sessions they own:

- Claude Code: `<target>/.claude/settings.local.json`
- Codex: `<target>/.codex/hooks.json`

The coordinator target is the project root. Worker targets are task
worktrees. Runtime files may be overwritten on spawn because they are
derived from source files plus the session kind.

## Vendor contracts

Claude Code project-local hooks live in settings under `hooks`:
https://docs.anthropic.com/en/docs/claude-code/settings and
https://code.claude.com/docs/en/hooks.

Codex project-local hooks live in `.codex/hooks.json`; managed
automation must launch Codex with hook trust bypass:
https://developers.openai.com/codex/hooks and
https://developers.openai.com/codex/cli/reference.
