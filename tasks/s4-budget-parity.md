**Status**: done

## What shipped

1. `spore budget active-tier` subcommand: reads OAuth credentials, prints
   normalized tier (max/pro/team/free).
2. Fixed `rationLongFrac` from 0.8 to 0.9 (parity with agent-budget).
3. Added `AccountSnapshots` to `state` struct and `Tier` to `usageSnapshot`
   for byte-compatible state.json round-trip.

## Parity notes

Deliberately not ported (basecamp-specific, not generic spore concerns):
- `tier` subcommand (multi-account store)
- `orchestratorIdentity` gate in stop-hook
- `pokeChip` (waybar)
- `logStopHookEvent`
- `queryNeedsRefresh` auto-refresh
- Multi-account refresh (`refreshAllAccountSnapshots`)

## Evidence

- go test: all green (30 tests in internal/budget)
- go vet: clean
- nix build: ok
- spore lint: budget.go filesize pre-existing (747 -> 748 lines)
