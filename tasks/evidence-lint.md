---
status: done
slug: evidence-lint
title: task-evidence lint
created: 2026-04-30T07:54:41Z
project: spore
evidence_required: [commit, file, test]
---

# Brief

Add a portable `task-evidence` lint that walks `tasks/*.md`, flags
unknown kinds eagerly, and verdict-blocks any `status=done` task
with bogus, hallucinated, or unknown evidence. Pre-contract tasks
(no `evidence_required`) are silently skipped. The CLI demotes
findings to a stderr warning during the soak window or whenever
`SPORE_EVIDENCE_WARN_ONLY=1`.

## Evidence

- commit: bc84da02 evidence: ship Shape C contract, verifier, done gate, lint, docs
- file: internal/lints/taskevidence.go ships `TaskEvidence.Run` and the `EvidenceWarnOnly` helper; `internal/lints/lints.go` adds it to `Default()`; `cmd/spore/lint_cmd.go` honours the warn-only flag
- test: internal/lints/taskevidence_test.go ships passing + failing fixtures (good, missing-bullet, unknown-kind, pre-contract-skipped, not-done-skipped)
