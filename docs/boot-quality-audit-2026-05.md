# boot-quality-audit 2026-05

Cold-boot tool-call correctness, brief landing, and token efficiency for
skyhelm and the worker fleet (claude / codex / opencode backends).

Method: read jsonl/rollout transcripts directly; no synthetic re-launches.
Sources used:

- skyhelm (claude): `~/.claude/projects/-home-sky-nix-config/88b7a613-cf3c-4432-bd7d-1e5d488692a6.jsonl`,
  cold-boot at 2026-05-14T06:33Z (post token-monitor restart).
- claude worker: this session,
  `~/.claude/projects/-home-sky-projects-spore--worktrees-boot-quality-audit-skyhelm/2a757199-ccaf-43e7-b2a0-65e4116f2887.jsonl`,
  cold-boot at 2026-05-14T09:28Z.
- codex worker: latest available rollout
  `~/.codex/sessions/2026/05/13/rollout-2026-05-13T21-09-08-019e2287-39bb-7972-aed8-f4b0b21cbb32.jsonl`
  (driver paused; no fresher sample, no new fixture spun).
- opencode worker: n/a. `~/.local/share/opencode/log/dev.log` is empty;
  most recent historical log is 2026-04-22. No baseline captured.

## Profiles

| Profile        | Turn-1 input tokens         | Boot tool errors       | ToolSearch rounds at boot | MCP connect-warn surface             |
|----------------|------------------------------|------------------------|---------------------------|--------------------------------------|
| skyhelm        | ~48k (cc=33,071 + cr=14,915) | 1 (semantic, see W3)   | 0                         | none surfaced in turn-1              |
| claude worker  | ~26k (cc=11,650 + cr=14,915) | 0 boot, 2 mid-explore  | 2 (TaskCreate batch + TaskUpdate) | 1 batch (amazon, claude-in-chrome, github, hackernews, kagi, research) |
| codex worker   | ~24k (input=24,287; cached=6,528) | typical exec-error rate ~13% mid-explore | n/a (no deferred-tool concept) | n/a                  |
| opencode       | n/a                          | n/a                    | n/a                       | n/a                                  |

Turn-1 input tokens = `cache_creation_input_tokens + cache_read_input_tokens`
(claude) or `total_tokens` of the first token_count event (codex). This
covers system prompt + tools schemas + role file + operator brief +
SessionStart hook payload, before any worker output.

## Operator brief landing

- skyhelm: first user message is 5 chars (the bare boot trigger). All
  context lives in role + state.md + boot-probe output. Brief lands
  cleanly: 0 turns wasted.
- claude worker: first user message is the task brief, ~5,400 chars
  / ~1,400 tokens. Worker reads it, locates `tasks/<slug>.md`, executes
  the plan-first contract on turn 1. Brief lands cleanly.
- codex worker: first user message is AGENTS.md (~33,597 chars) followed
  by the task brief in turn 2 (~6,551 chars). Codex re-loads AGENTS.md
  inline rather than via a system-prompt slot, which is structural to
  Codex itself.

## Waste patterns

W1. **Deferred-tool ToolSearch round-trips on the claude worker.**
Every cold-boot worker that touches `TaskCreate` (the kernel rule
explicitly says "use TaskCreate to plan and track work") must spend a
ToolSearch round-trip to load its schema, then often a second one for
`TaskUpdate`. Each round-trip costs one assistant turn plus the loaded
schema (~0.5-3k tokens depending on batch size). In this session: 2
ToolSearch turns out of the first ~10 assistant turns.

W2. **MCP server set loaded broadly; mostly unused per worker.** User-
global `~/.claude.json` enables 5 MCP servers (amazon, github,
hackernews, kagi-ken-mcp, research) plus claude-in-chrome project-
scoped. All connect on every claude worker spawn. Most workers only
ever touch `mcp__github__*` and `mcp__kagi-ken-mcp__*`. Each connecting
server contributes tool-schema bytes once it lands plus an MCP "still
connecting" warning surface in turn 1.

W3. **Skyhelm `harness/skyhelm-boot` exit=2 is operator signal, not a
tool error.** The boot probe aggregates inner exit codes: state.md
oversize (90 lines > cap 80) and sla-scan orphan findings both surface
via exit=2 by design. The brief explicitly forbids semantic changes
to boot probes. This is not a regression and not a candidate for fix;
it is a signal-as-tool-error pattern that the brief acknowledges as
intended.

W4. **Operator brief size is the main per-worker variable cost.** The
boot-quality-audit brief is ~1,400 tokens; tightly-scoped briefs
(`migrate-*-port-port` shape) run 200-400 tokens. No metric exists
today on `wt task new` to flag oversized briefs at author time.

W5. **Skyhelm turn-1 cache_creation is +21k vs worker.** Mostly the
larger composed role (skyhelm.md vs nix-config.md) plus the boot-probe
output text plus state.md re-read. State.md cap exists and is enforced
via boot-probe exit=2; in this sample it was breached (90 > 80 lines)
without operator action between probes.

W6. **Codex mid-explore exec-error rate ~13% (4/30) in the sampled
rollout** (exits 1, 2, 127, 1). Not boot-quality per se; agent quality
issue. Out of scope here.

W7. **opencode worker has no recent boot baseline.** Driver may be
dormant. Either it should be removed from the supported worker set or
a baseline needs capturing.

## Lift candidates

L1 (W1, low cost). Pre-load the deferred Task* tools on claude worker
spawn so a brand-new worker doesn't pay a ToolSearch round-trip just to
add its first task. Surface: `~/.claude/settings.json` or the wt-task
launch wrapper (`nix/packages/wt/wt-task`) sets the appropriate
`enabledTools`/`startupTools` slot if the harness supports it; falling
back, document that the first ToolSearch is structural and budget for
it. AskUserQuestion is conditional: skyhelm rule forbids it for skyhelm,
but workers in alignment-mode are encouraged to use it - so worker
preload should include AskUserQuestion, skyhelm preload should not.

L2 (W2, medium cost). Trim the MCP server set. amazon, hackernews, and
research are unlikely to be needed in 99% of worker boots. Demote those
to skyhelm-only (or remove entirely) by moving them out of user-global
`~/.claude.json` into a skyhelm-scoped `mcpServers` block. Workers keep
github + kagi only. Estimated saving: 3 MCP connect surfaces + ~2-5k
tokens of unused tool schemas at warm-up.

L3 (W4, low cost). Add a brief token-cost line to `wt task new` output.
After writing `tasks/<slug>.md`, print `brief: ~N tokens` (rough
estimate: chars/4). Soft warning if N > 1500. Doesn't gate; just makes
the cost visible to the brief author.

L4 (W7, scope clarification). Either capture an opencode worker
baseline (one-shot fixture) or formally retire opencode from the
supported backend set. Operator decision; not a code change.

L5 (acceptance #3, follow-up tooling). Lift this audit into a probe
the operator can re-run: `spore worker boot-audit [--session <jsonl>]`
that emits the profile table by reading a transcript. Pure read-only
analyzer; no semantic boot changes. Useful to detect regression after
L1/L2/L3 land.

## Out of scope (per brief)

- No semantic changes to `harness/skyhelm-boot` probes.
- No spore-side rename of `worker` to `rower`/`skyhelm`/`skybot`.
- No unpause of codex driver.

## Acceptance status

- [x] Profiles for skyhelm, claude worker, codex worker captured.
- [ ] opencode worker profile (n/a; see W7 / L4).
- [x] Waste patterns documented with per-pattern lift cost.
- [ ] Lift commits: deferred to follow-up tasks (L1, L2, L3, L5).
- [ ] `spore worker boot-audit` probe: deferred (L5).
