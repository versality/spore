## Source map

```
spore/
|-- cmd/
|   |-- spore/                  CLI entry point (Go).
|   `-- spore-sandbox/          bwrap+proxy sandbox launcher for worker agents.
|-- internal/                   Go internal packages, kernel implementation.
|   |-- agentpane/              tmux pane capture + classify (idle/typing/tool).
|   |-- agentpolicy/            Per-agent effort + interactive-argv policy (claude, codex).
|   |-- align/                  Pilot-agent alignment-mode tracker.
|   |-- auditversions/          Compare deployed agent binaries to lockfiles.
|   |-- bootstrap/              Stage-gate driver + per-stage detectors.
|   |-- budget/                 Account-tier + token-budget gating for coordinator + workers.
|   |-- composer/               Instruction composer: rule-pool to rendered files.
|   |-- coordinator/            Coordinator lifecycle (spawn, workerwatch, tokenmonitor, verify).
|   |-- evidence/               Parse + verify the task-close evidence contract.
|   |-- evictor/                Idle-worker eviction sweep.
|   |-- fleet/                  Worker fleet: coordinator + workers consuming the task queue.
|   |-- gh/                     gh-cli wrapper (PR view/create/merge, run lists).
|   |-- hooks/                  Stop / PreToolUse / commit-msg hook entry points.
|   |   `-- wtgit/              Shared worktree git probes used by ship-cycle hooks.
|   |-- infect/                 nixos-anywhere wrapper for `spore infect`.
|   |-- initconfig/             `spore init` config file generator.
|   |-- install/                Drops embedded skills into a target's .claude/skills/.
|   |-- lints/                  Portable lint set (drift, file-size, comment-noise, em-dash, ...).
|   |-- matter/                 External-issue backends (Linear, GitHub) for task done/sync.
|   |-- merge/                  wt/<slug> merge integrity audit + unblock.
|   |-- opencode/               opencode-specific fleetstop + liveness probes.
|   |-- sandboxcfg/             `[sandbox]` TOML config loader for spore-sandbox.
|   |-- scout/                  Healer-task minter: clusters lint findings, writes briefs.
|   |-- search/                 Token-budgeted search wrapper.
|   |-- secret/                 age-encrypted operator secret store.
|   |-- sessionkind/            Coordinator vs worker session-kind constants.
|   |-- signal/                 Cross-session inbox signal driver.
|   |-- task/                   Worktree-task driver (lifecycle, inbox, merge, ship, ...).
|   |-- tmuxsess/               Shared tmux has/kill/list-session helpers.
|   |-- todo/                   `spore todo` command for the docs/todo epic register.
|   |-- transcript/             Codex + claude transcript parsing for hooks.
|   |-- worker/                 Worker-side helpers (bootaudit, exitkind, tokenmonitor).
|   `-- wtcheck/                `spore wt-check` lint+test gate driver.
|-- rules/                      Markdown rule pool, composed into CLAUDE.md / AGENTS.md.
|   |-- consumers/              Per-consumer rule lists (line per fragment id).
|   |-- core/                   Always-on, language-agnostic fragments.
|   `-- lang/                   Language-specific fragments (later phase).
|-- bootstrap/                  spore-bootstrap skill body, stage runbooks, drop-ins.
|   |-- skills/                 spore-bootstrap and diagram skills.
|   |-- stages/                 One runbook per stage gate.
|   |-- mcp/                    MCP server config templates.
|   `-- flake/                  Minimal NixOS flake used by `spore infect`.
`-- docs/                       Design notes, rationale.
```
