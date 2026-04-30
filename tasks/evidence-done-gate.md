---
status: done
slug: evidence-done-gate
title: evidence done gate in task.Done
created: 2026-04-30T07:54:41Z
project: spore
evidence_required: [commit, file, test]
---

# Brief

Hook `task.Done` in `internal/task/lifecycle.go` to call
`evidence.Verify` and refuse the flip when the verdict blocks. Honour
`SPORE_EVIDENCE_WARN_ONLY=1` and the `evidence.InSoakWindow` grace
period so the first 7 days only warn.

## Evidence

- commit: bc84da02 evidence: ship Shape C contract, verifier, done gate, lint, docs
- file: internal/task/lifecycle.go adds `evidenceGate` and `metaToAny` and the `EvidenceWarnOnlyEnv` const
- test: internal/task/lifecycle_test.go adds TestDoneRefusesBogusEvidence, TestDoneAllowsRealImpl, TestDoneWarnOnlyAllowsBlockedVerdict
