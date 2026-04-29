#!/usr/bin/env bash
# bootstrap/smoke.sh: end-to-end smoke for `spore bootstrap` plus a
# fleet up / one-task / fleet down cycle.
#
# Creates a fresh git repo + flake.nix + justfile + README.md +
# CLAUDE.md in a tempdir, pre-populates the operator-facing
# sentinels (info-gathered.json, readme-followed.json) and the
# alignment criteria, then walks `spore bootstrap` to
# worker-fleet-ready. Then mints + starts a task, brings the fleet
# up, lets a worker observe the task flip to done, and tears the
# fleet down. Cleans up the tempdir on exit.
#
# Run inside `nix develop` so go / git / just / spore / tmux are on
# PATH.
set -eu -o pipefail

script_dir="$(cd "$(dirname "$0")" && pwd)"
repo_root="$(cd "$script_dir/.." && pwd)"
spore_bin="${SPORE_BIN:-$repo_root/result/bin/spore}"

# Build a fresh binary if the caller did not provide one. Prefer
# `nix develop -c go build` over a cached one so the smoke matches
# the worktree.
if [ ! -x "$spore_bin" ] || [ -n "${SMOKE_FORCE_REBUILD:-}" ]; then
  spore_bin="$(mktemp)"
  (cd "$repo_root" && go build -o "$spore_bin" ./cmd/spore)
fi

work="$(mktemp -d)"
state="$work/state"
home="$work/home"
trap 'rm -rf "$work"' EXIT

export XDG_STATE_HOME="$state"
export HOME="$home"

mkdir -p "$work/proj"
cd "$work/proj"

git init -q -b main
git config user.email t@t
git config user.name t
git config commit.gpgsign false

cat > flake.nix <<'EOF'
{ description = "smoke"; }
EOF
cat > justfile <<'EOF'
check:
	true
EOF
cat > README.md <<'EOF'
# smoke

To use: run `just check`.
EOF
cat > CLAUDE.md <<'EOF'
# CLAUDE.md

Smoke project. Secrets live in `.envrc` (no values stored).
EOF
cat > .envrc <<'EOF'
export FOO=bar
EOF
git add -A
git commit -q -m "smoke fixture"

project="$(basename "$work/proj")"
state_dir="$state/spore/$project"
mkdir -p "$state_dir"

cat > "$state_dir/info-gathered.json" <<'EOF'
{
  "tickets": {"tool": "none", "decision": "use spore tasks"},
  "knowledge": {"tool": "none", "decision": "use docs/todo + spore docs/list.md"}
}
EOF

cat > "$state_dir/readme-followed.json" <<EOF
{
  "readme_path": "$work/proj/README.md",
  "items": [
    {"step": "run \`just check\`", "status": "ok"}
  ]
}
EOF

# Drive `spore align` through its criteria + flip.
for i in 1 2 3; do "$spore_bin" align note "[promoted] pref$i"; done
for i in 4 5 6 7 8 9 10; do "$spore_bin" align note "pref$i"; done
"$spore_bin" align flip

# Walk the bootstrap.
out="$("$spore_bin" bootstrap 2>&1)"
echo "$out"
if ! echo "$out" | grep -q "bootstrap complete"; then
  echo "smoke: bootstrap did not complete; current state:" >&2
  "$spore_bin" bootstrap status >&2 || true
  exit 1
fi

echo "smoke: bootstrap ok"

project_root="$work/proj"

# Fleet smoke: reconciler shape.
#   1. With the kill-switch flag missing, reconcile is a no-op
#      (and the disabled marker prints).
#   2. Enable the flag. Mint a task, flip it to active, run
#      reconcile: a tmux session for the slug appears.
#   3. Run reconcile again: the session is kept (no new spawn).
#   4. Mark the task done, run reconcile: the session is reaped.
#   5. Disable the flag, confirm reconcile short-circuits again.
export SPORE_AGENT_BINARY="sleep 600"

reconcile_disabled_out="$("$spore_bin" fleet disable >/dev/null && "$spore_bin" fleet reconcile)"
echo "$reconcile_disabled_out"
if ! echo "$reconcile_disabled_out" | grep -q "fleet: disabled"; then
  echo "smoke: fleet reconcile did not short-circuit when flag missing" >&2
  exit 1
fi

"$spore_bin" fleet enable >/dev/null

# Coordinator: enabled flag should bring up the singleton session
# alongside any worker activity. We enabled the flag above; reconcile
# once with no active tasks and check the coordinator appeared.
coord_session="spore/$(basename "$project_root")/coordinator"
"$spore_bin" fleet reconcile --max-workers=1 >/dev/null
if ! tmux has-session -t "$coord_session" 2>/dev/null; then
  echo "smoke: expected coordinator session $coord_session after enable+reconcile" >&2
  exit 1
fi
coord_created_first="$(tmux display-message -p -t "$coord_session" '#{session_created}')"

# Idempotency: a second reconcile must not respawn the coordinator.
"$spore_bin" fleet reconcile --max-workers=1 >/dev/null
if ! tmux has-session -t "$coord_session" 2>/dev/null; then
  echo "smoke: coordinator session vanished after no-op reconcile" >&2
  exit 1
fi
coord_created_second="$(tmux display-message -p -t "$coord_session" '#{session_created}')"
if [ "$coord_created_first" != "$coord_created_second" ]; then
  echo "smoke: coordinator was respawned on idempotent reconcile" >&2
  exit 1
fi
echo "smoke: coordinator singleton ok"

slug="$("$spore_bin" task new "fleet smoke")"
fleet_session="spore/$(basename "$project_root")/$slug"

# Flip status=active without going through `task start` so we
# observe the reconciler doing the worktree+session work itself.
sed -i 's/^status: draft$/status: active/' "$project_root/tasks/$slug.md"

reconcile_out="$("$spore_bin" fleet reconcile --max-workers=1)"
echo "$reconcile_out"
if ! echo "$reconcile_out" | grep -q "spawned: $slug"; then
  echo "smoke: reconcile pass 1 did not spawn $slug" >&2
  exit 1
fi
if ! tmux has-session -t "$fleet_session" 2>/dev/null; then
  echo "smoke: tmux session $fleet_session missing after reconcile" >&2
  exit 1
fi

reconcile_idem_out="$("$spore_bin" fleet reconcile --max-workers=1)"
echo "$reconcile_idem_out"
if echo "$reconcile_idem_out" | grep -q "spawned:"; then
  echo "smoke: reconcile pass 2 should have been a no-op, got spawn" >&2
  exit 1
fi
if ! echo "$reconcile_idem_out" | grep -q "kept=1"; then
  echo "smoke: reconcile pass 2 did not report kept=1" >&2
  exit 1
fi

# Flip status without going through `task done` so the session
# stays alive: we want the reconciler to be the one that reaps it.
sed -i 's/^status: active$/status: done/' "$project_root/tasks/$slug.md"

reconcile_reap_out="$("$spore_bin" fleet reconcile --max-workers=1)"
echo "$reconcile_reap_out"
if ! echo "$reconcile_reap_out" | grep -q "reaped: $slug"; then
  echo "smoke: reconcile after status flip did not reap $slug" >&2
  exit 1
fi
if tmux has-session -t "$fleet_session" 2>/dev/null; then
  echo "smoke: tmux session $fleet_session still alive after reap" >&2
  exit 1
fi

"$spore_bin" fleet disable >/dev/null
final_out="$("$spore_bin" fleet reconcile)"
if ! echo "$final_out" | grep -q "fleet: disabled"; then
  echo "smoke: post-disable reconcile did not short-circuit" >&2
  exit 1
fi
if tmux has-session -t "$coord_session" 2>/dev/null; then
  echo "smoke: coordinator session still alive after fleet disable" >&2
  exit 1
fi
echo "smoke: fleet reconciler ok"

echo "smoke: task lifecycle"
export SPORE_AGENT_BINARY="sleep 600"
life_slug="$("$spore_bin" task new "lifecycle smoke")"
life_path="$project_root/tasks/$life_slug.md"
life_worktree="$project_root/.worktrees/$life_slug"

if ! grep -q '^status: draft$' "$life_path"; then
  echo "smoke: lifecycle: expected status=draft after task new" >&2
  exit 1
fi

life_session="$("$spore_bin" task start "$life_slug")"
if ! grep -q '^status: active$' "$life_path"; then
  echo "smoke: lifecycle: expected status=active after task start" >&2
  exit 1
fi
if [ ! -d "$life_worktree" ]; then
  echo "smoke: lifecycle: expected worktree $life_worktree after task start" >&2
  exit 1
fi
if ! tmux has-session -t "$life_session" 2>/dev/null; then
  echo "smoke: lifecycle: expected tmux session $life_session after task start" >&2
  exit 1
fi

"$spore_bin" task pause "$life_slug" >/dev/null
if ! grep -q '^status: paused$' "$life_path"; then
  echo "smoke: lifecycle: expected status=paused after task pause" >&2
  exit 1
fi
if [ ! -d "$life_worktree" ]; then
  echo "smoke: lifecycle: expected worktree to remain after task pause" >&2
  exit 1
fi

"$spore_bin" task start "$life_slug" >/dev/null
if ! grep -q '^status: active$' "$life_path"; then
  echo "smoke: lifecycle: expected status=active after resume" >&2
  exit 1
fi
if ! tmux has-session -t "$life_session" 2>/dev/null; then
  echo "smoke: lifecycle: expected tmux session after resume" >&2
  exit 1
fi

"$spore_bin" task done "$life_slug" >/dev/null
if ! grep -q '^status: done$' "$life_path"; then
  echo "smoke: lifecycle: expected status=done after task done" >&2
  exit 1
fi
if [ -d "$life_worktree" ]; then
  echo "smoke: lifecycle: expected worktree gone after task done" >&2
  exit 1
fi
if tmux has-session -t "$life_session" 2>/dev/null; then
  echo "smoke: lifecycle: expected tmux session gone after task done" >&2
  exit 1
fi
echo "smoke: task lifecycle ok"

echo "smoke: hooks + skills install"
for skill_relpath in spore-bootstrap/SKILL.md diagram/SKILL.md; do
  if [ ! -f "$project_root/.claude/skills/$skill_relpath" ]; then
    echo "smoke: hooks: expected .claude/skills/$skill_relpath" >&2
    exit 1
  fi
done

hooks_dir="$("$spore_bin" hooks install)"
if [ -z "$hooks_dir" ]; then
  echo "smoke: hooks: spore hooks install produced no output" >&2
  exit 1
fi
if [ ! -x "$hooks_dir/commit-msg" ]; then
  echo "smoke: hooks: expected executable $hooks_dir/commit-msg" >&2
  exit 1
fi
hooks_path_cfg="$(git -C "$project_root" config --get core.hooksPath)"
if [ "$hooks_path_cfg" != "$hooks_dir" ]; then
  echo "smoke: hooks: core.hooksPath=$hooks_path_cfg, want $hooks_dir" >&2
  exit 1
fi
echo "smoke: hooks + skills install ok"

echo "smoke: compose"
mkdir -p "$project_root/rules/test" "$project_root/rules/consumers"
cat > "$project_root/rules/test/header.md" <<'EOF'
# smoke project

Composed by spore.
EOF
cat > "$project_root/rules/test/body.md" <<'EOF'
## Validation

Run `spore lint`.
EOF
cat > "$project_root/rules/consumers/test.txt" <<'EOF'
# target: CLAUDE.md
test/header
test/body
EOF

"$spore_bin" compose --consumer test > "$project_root/CLAUDE.md"
if ! diff -u <("$spore_bin" compose --consumer test) "$project_root/CLAUDE.md"; then
  echo "smoke: compose: rendered output drifts from on-disk CLAUDE.md" >&2
  exit 1
fi
echo "smoke: compose ok"

echo "smoke: lint"
if ! "$spore_bin" lint; then
  echo "smoke: lint: spore lint reported issues" >&2
  exit 1
fi
echo "smoke: lint ok"

echo "smoke: ok"
