## Worker etiquette

- Source edits stay inside the spore tree. Do not leak into a consumer
  project's working copy, even when dogfooding the bootstrap flow.
- Do not rename `dispatcher` or `runner` without updating the
  composer plus its tests in the same commit. The names are
  kernel-internal contract; silent drift breaks downstream rendering.
- Opensource-bound. Mind the leak surface: no internal hostnames, no
  operator-machine paths, no personal email beyond what
  `git config user.email` resolves to.
