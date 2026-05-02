---
slug: s7-coordinator
title: "S7 coordinator: state + verify + loopguard + tokenmonitor"
project: spore
status: done
created: 2026-05-01T20:10:43+03:00
evidence_required: [test, command]
needs: [s3-hooks-settings, s4-budget-parity, s6-task-parity]
---

# Brief

**Sibling**: spore-s7-fleet-coordinator (in nix-config)

Build the coordinator packages in spore that skyhelm consumes.

## New packages

1. `coordinator/state` - state.md parser/writer (H2/H3 format, active tasks table, recent events, rules, directives)
2. `coordinator/verify` - port nix-config's `harness/skyhelm-verify-done.sh` verdict logic to Go. Verdicts: real-impl, rational-close, cross-repo, suspect-hallucination, bogus-evidence, unknown.
3. `coordinator/loopguard` - circuit breaker on respawn rate
4. `coordinator/tokenmonitor` - wraps budget short window into stop-hook shape
5. `coordinator/statedebt` - port skyhelm-state-debt scanner

Reference sources:
- `~/nix-config/harness/skyhelm-verify-done.sh`
- `~/nix-config/nix/packages/wt/skyhelm-state-debt`
- `~/nix-config/nix/packages/wt/skyhelm-token-monitor`

## New CLI

`cmd/spore coordinator {role-brief, state-debt, verify-done, loop-guard}`

## Evidence

- test: internal/coordinator/{state,verify,loopguard,statedebt,tokenmonitor}/*_test.go
- command: `go test ./internal/coordinator/...` (covers all 6 verdict types)
