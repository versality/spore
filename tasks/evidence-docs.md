---
status: done
slug: evidence-docs
title: docs/evidence.md design + rollback
created: 2026-04-30T07:54:41Z
project: spore
evidence_required: [commit, doc-link]
---

# Brief

Document Shape C, the verdict matrix, the soak window, and the
`SPORE_EVIDENCE_WARN_ONLY` rollback path so a future operator can
read the design without re-deriving it from S5. No code change.

## Evidence

- commit: bc84da02 evidence: ship Shape C contract, verifier, done gate, lint, docs
- doc-link: docs/evidence.md describes Shape C, the six verdicts, the 7-day soak window, and the rollback override
