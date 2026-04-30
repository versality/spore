---
status: done
slug: evidence-package
title: evidence package: Shape C parser + verifier
created: 2026-04-30T07:54:41Z
project: spore
evidence_required: [commit, file, test]
---

# Brief

Stand up `internal/evidence/` with the Shape C API surface declared
in S5: `Required`, `Parse`, `Verify`, plus the recognised-kind
allowlist. Verifier is structural and pure; no shell, no git, no
filesystem so unit tests run in-process.

## Evidence

- commit: bc84da02 evidence: ship Shape C contract, verifier, done gate, lint, docs
- file: internal/evidence/verify.go contains the six-verdict precedence ladder (`Verify`, `isPlausibleRest`, `isCrossRepoRest`)
- test: internal/evidence/evidence_test.go covers all six verdicts plus the pre-contract / scalar / inline-list / []any required shapes
