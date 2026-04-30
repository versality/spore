---
status: done
slug: evidence-verify-cli
title: spore task verify CLI
created: 2026-04-30T07:54:41Z
project: spore
evidence_required: [commit, command, file]
---

# Brief

Add `spore task verify <slug>` so the operator can preview the
evidence verdict without flipping status. Output is the verdict plus
diagnostic lines.

## Evidence

- commit: bc84da02 evidence: ship Shape C contract, verifier, done gate, lint, docs
- command: `spore task verify evidence-verify-cli` prints `evidence-verify-cli: real-impl`
- file: cmd/spore/main.go adds `runTaskVerify` and the `verify <slug>` line in taskUsage; `internal/task/lifecycle.go` exports `Verify`
