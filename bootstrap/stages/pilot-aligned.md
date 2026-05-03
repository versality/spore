# Stage: pilot-aligned

Sequenced just before `worker-fleet-ready`. The bootstrap blocks
here until the agent and pilot have been through the alignment
period together: the agent has logged enough pilot preferences,
some of them have been promoted to rule-pool entries, and the
pilot has flipped alignment off.

## Exit criteria

Default thresholds (override per project in `spore.toml` under
`[align]`):

1. At least 10 notes in `~/.local/state/spore/<project>/alignment.md`.
2. At least 3 of them marked `[promoted]`.
3. The operator has run `spore align flip`.

## Runbook

```
spore align status               # see N/M and the checklist
spore align note "<line>"        # log one observed pilot preference
spore align note "[promoted] <line>"   # mark a promoted preference
spore align flip                 # confirm; turn alignment mode off
spore bootstrap                  # re-enter the bootstrap; advance
```

`spore align flip` writes the sentinel that turns alignment mode
off. The next compose drops the alignment-mode rule from the
project's instruction files.

## Why this stage exists

The first sessions on a fresh project are where the agent learns
the pilot's specifics: commit cadence, where they want to be asked
versus left alone, which kinds of files are off-limits, naming
conventions, test discipline. Promoting a handful of those
observations into rule-pool entries (and only then flipping out)
seeds the per-project rule pool with real signal instead of
defaults.

See `rules/core/alignment-mode.md` for the agent-facing
instructions that govern behavior while this stage is active.
