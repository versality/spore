# Matter plugins

Matter plugins pull work from external sources (Linear, GitHub Issues,
ad-hoc adapters) and mirror local task transitions back to them.
Configure one or more in `spore.toml` under `[matter.<name>]`, or via
`SPORE_MATTER_<NAME>__<KEY>` env vars when the NixOS module is the
source of truth (the loader merges both).

The fleet reconciler runs `Sync` against every enabled matter before it
enumerates tasks, so new upstream tickets land as `tasks/<slug>.md`
files automatically on the next pass. When a task carrying matter
metadata flips to done, an `OnDone` hook mirrors the close back
upstream; a fallback sweep on the next `Sync` covers misses (reconciler
off, adapter down, task edited out of band).

Tasks created by a matter carry three frontmatter keys: `matter`
(adapter name), `matter_id` (upstream id), and `matter_url` (deep link,
optional).

## Linear

The bundled `linear` adapter polls a configured team for issues in a
ready state, projects each one to `tasks/<slug>.md`, pushes the issue to
in-progress, and mirrors `status: done` flips back to a configured done
state.

```toml
# spore.toml
[matter.linear]
enabled = true
team = "MAR"                       # team key, required
ready_state = "Ready"              # default
in_progress_state = "In Progress"  # default
done_state = "Done"                # default
api_key_env = "LINEAR_API_KEY"
# or, when the NixOS module supplies the secret via systemd:
# api_key_file = "linear-api-key"
```

For NixOS deployments, configure the same shape under
`services.spore-fleet.matters.linear`. Secrets ride systemd
`LoadCredential` and never enter Nix evaluation or `/nix/store`. See
[../nixosModules/spore-fleet.nix](../nixosModules/spore-fleet.nix).
