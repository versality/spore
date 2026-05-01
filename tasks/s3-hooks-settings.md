---
slug: s3-hooks-settings
title: "S3 hooks: Settings + WatchInbox + NotifySkyhelm"
project: spore
status: done
created: 2026-05-01T20:10:43+03:00
evidence_required: [commit, file, test]
---

# Brief

**Sibling**: spore-s3-hooks-protocol (in nix-config)

Add three missing APIs to spore's hooks package (`internal/hooks/`):

## 1. hooks.Settings()

```go
func Settings(stops, postToolUse, notification []HookBin) ([]byte, error)
```

Emits a complete `settings.json` JSON blob for claude-code. Each `HookBin` carries a name, binary path, and hook type. Output must be deterministic (sorted keys). Test: golden-file comparison against a reference settings.json.

## 2. hooks.WatchInbox()

```go
func WatchInbox(slug string) error
```

Port of nix-config's `wt-task watch-inbox` bash function. Watches `~/.local/state/wt/inbox/<slug>/` for new `.json` files, reads them, and injects as system reminders. On drain, moves files to `inbox/read/`.

## 3. hooks.NotifySkyhelm()

```go
func NotifySkyhelm(slug string) error
```

Port of nix-config's `wt-task notify-skyhelm` bash function. Writes a poke to the skyhelm inbox (`~/.local/state/skyhelm/$project/inbox/`).

## Evidence

- commit: 62f92302 hooks: Settings, WatchInbox, NotifySkyhelm
- file: internal/hooks/settings.go declares HookBin and Settings;
  internal/hooks/watchinbox.go declares WatchInbox and ErrWake;
  internal/hooks/notifyskyhelm.go declares NotifySkyhelm.
- test: internal/hooks/settings_test.go pins
  testdata/settings.golden.json; internal/hooks/watchinbox_test.go
  exercises drain plus wake; internal/hooks/notifyskyhelm_test.go
  exercises atomic write into the project inbox.
