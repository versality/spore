---
status: active
slug: s6-task-parity
title: "S6 task: Edit + Pick + Waybar + flag parity"
created: 2026-05-01T00:00:00Z
project: spore
---

## Brief

Complete spore's task package to reach parity with nix-config's wt-task bash blob.

## Missing APIs

1. `task.Edit(slug, editor string) error`
2. `task.Pick(filter string) (string, error)`
3. `task.Waybar() ([]byte, error)`
4. Flag parity: `--start`, `--body`, `--needs`, `--draft`, `--all`, `--done`, `--edit/--no-edit`
5. `task.AutoCommitDrift()`
6. `task.Merge(slug string) error`

## Evidence

- go-test: green
- flag-parity: `spore task --help` covers all flags from `wt task --help`
