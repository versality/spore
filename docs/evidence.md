# evidence contract

Spore tasks declare what proof their done flip needs and the body
provides it. The structural verifier in `evidence/` runs
inside the done gate and the `task-evidence` lint, refusing flips it
cannot reconcile.

## Shape C

A task brief carries:

```
---
status: done
slug: foo
title: Foo
evidence_required: [commit, file, test]
---

## Evidence

- commit: a1b2c3d4 shipped the parser
- file: internal/foo/parse.go contains Parse
- test: internal/foo/parse_test.go covers the corpus
```

Two surfaces, one contract:

- frontmatter `evidence_required: [...]` declares the kinds.
- body `## Evidence` section provides one bullet per kind.

The frontmatter list reuses the YAML-subset spore's parser already
accepts (`[a, b, c]` inline, or a bare scalar for a single kind). The
body section is parsed line by line: a bullet is `- <kind>: <rest>`,
the kind comes from a fixed allowlist, the rest is whatever follows
the colon.

Recognised kinds (rejected anywhere else by the lint):

- `commit` - a SHA on this repo, or `<repo>:<sha>` for a sibling.
- `command` - a one-liner the operator can re-run.
- `file` - a repo-relative path the change touches.
- `test` - a test path that covers the change.
- `side-by-side` - a link or path showing before/after.
- `doc-link` - a doc updated alongside the code.

## Verdicts

`evidence.Verify(meta, body)` returns one of six verdicts plus a
diagnostic slice. The done gate maps three to refuse, three to allow:

| Verdict                | Done gate | Meaning                                                      |
|------------------------|-----------|--------------------------------------------------------------|
| `real-impl`            | allow     | Every required kind has a substantive bullet.                |
| `rational-close`       | allow     | Missing kinds are documented `N/A: <rationale>`.             |
| `cross-repo`           | allow     | A bullet points to a sibling repo (`<repo>:<sha>` or URL).   |
| `suspect-hallucination`| refuse    | Required kinds missing while the body claims completion.     |
| `bogus-evidence`       | refuse    | A bullet's rest is empty or shape-implausible for its kind.  |
| `unknown`              | refuse    | Some required kinds are unmet and the body is ambiguous.     |

The verifier is structural by design: it never shells out to git or
reads the working tree. A separate coordinator-side verifier layers
live-state checks (does the SHA resolve, does the file exist, does
the grep pattern match) on top. Decoupling lets the unit tests run
in-process without git fixtures and removes the SIGPIPE class of bug
the basecamp's bash verifier had with `git log | head | grep`
pipelines.

## Soak window and rollback

The contract starts on `evidence.ContractStart` (2026-04-30). The
first `evidence.SoakWindow` (7 days) the gate is warn-only by
default: blocking verdicts log to stderr and the flip proceeds. After
the window, blocking verdicts are hard errors.

`SPORE_EVIDENCE_WARN_ONLY=1` is a permanent operator override: when
set, blocking verdicts always degrade to a warning. Rollback path: if
a wave of false positives blocks legitimate flips, export the env var
in the operator's shell profile and the gate stays inert while the
parser stays on so `spore task verify` keeps producing diagnostics.
The structural change to revert is one short conditional in
`lifecycle.evidenceGate` and `cmd/spore/lint_cmd.go`.

## Sample run

```
$ spore task verify foo
foo: real-impl

$ spore task verify bar
bar: bogus-evidence
  commit: empty bullet rest

$ spore task verify baz
baz: suspect-hallucination
  missing required: commit, test
```

The lint variant is the same logic over `tasks/*.md`:

```
$ spore lint
[task-evidence] tasks/baz.md: [suspect-hallucination] missing required: commit, test
```

During the soak window the same line is emitted to stderr prefixed
with `warn:` and `spore lint` exits 0.
