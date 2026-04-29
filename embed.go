// Package spore exists solely to host go:embed assets that ship with
// the spore CLI. The kernel implementation lives under cmd/ and
// internal/; this top-level package is just an asset container.
package spore

import "embed"

// BundledFlake is the minimal NixOS flake `spore infect` stages into a
// temp directory and runs nixos-anywhere against when the operator
// does not pass --flake. See bootstrap/flake/README.md for shape and
// limits.
//
//go:embed all:bootstrap/flake
var BundledFlake embed.FS

// BundledSkills is the skill tree `spore install` drops into a target
// project's .claude/skills/ directory so the agent can discover the
// spore-bootstrap and diagram skills without a source-tree checkout.
//
//go:embed all:bootstrap/skills
var BundledSkills embed.FS

// BundledCoordinatorRole is the default role file the fleet reconciler
// uses to boot the singleton coordinator agent. Consumers can override
// by writing their own bootstrap/coordinator/role.md before bootstrap.
//
//go:embed bootstrap/coordinator/role.md
var BundledCoordinatorRole []byte
