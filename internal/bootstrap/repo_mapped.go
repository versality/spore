package bootstrap

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	spore "github.com/versality/spore"
	"github.com/versality/spore/internal/install"
)

// repoMarkers maps a marker file (project root relative) to a short
// language / build-system label. Order is irrelevant; the detector
// reports every marker it finds, sorted, so notes are deterministic.
var repoMarkers = map[string]string{
	"flake.nix":      "nix",
	"Cargo.toml":     "rust",
	"go.mod":         "go",
	"package.json":   "node",
	"pyproject.toml": "python",
	"setup.py":       "python",
	"Gemfile":        "ruby",
	"deps.edn":       "clojure",
	"project.clj":    "clojure",
	"pom.xml":        "java",
	"build.gradle":   "gradle",
	"Makefile":       "make",
	"justfile":       "just",
}

// starterInstructions is the minimum agent-instruction file spore
// drops when the project has none. CLAUDE.md and AGENTS.md receive the
// same bytes so Claude Code and Codex start from the same contract.
const starterInstructions = `# Agent Instructions

This project uses spore for agent governance. Run ` + "`spore compose --consumer <name>`" + ` to
render the per-project rule set into CLAUDE.md, then mirror it to
AGENTS.md once a consumer list exists under ` + "`rules/consumers/`" + `.

## Validation

Run ` + "`spore lint`" + ` for the portable lint set and ` + "`spore bootstrap status`" + ` to
see which onboarding stages are still pending.
`

func detectRepoMapped(root string) (string, error) {
	if root == "" {
		return "", errors.New("repo-mapped: empty root")
	}
	var hits []string
	seenLabel := map[string]bool{}
	for marker, label := range repoMarkers {
		if _, err := os.Stat(filepath.Join(root, marker)); err == nil {
			if !seenLabel[label] {
				hits = append(hits, label)
				seenLabel[label] = true
			}
		}
	}
	if len(hits) == 0 {
		return "", errors.New("no recognised project marker (flake.nix / Cargo.toml / go.mod / package.json / pyproject.toml / Gemfile / deps.edn / pom.xml / Makefile / justfile)")
	}
	sort.Strings(hits)

	wrote, err := ensureInstructionFiles(root)
	if err != nil {
		return "", err
	}
	skills, err := install.Install(root, spore.BundledSkills, "bootstrap/skills")
	if err != nil {
		return "", fmt.Errorf("install skills: %w", err)
	}

	notes := "detected: " + strings.Join(hits, ",")
	if len(wrote) > 0 {
		notes += "; wrote starter " + strings.Join(wrote, " / ")
	}
	if len(skills.Written) > 0 {
		notes += fmt.Sprintf("; installed %d skill file(s)", len(skills.Written))
	}
	return notes, nil
}

func ensureInstructionFiles(root string) ([]string, error) {
	claudePath := filepath.Join(root, "CLAUDE.md")
	agentsPath := filepath.Join(root, "AGENTS.md")
	claude, claudeErr := os.ReadFile(claudePath)
	agents, agentsErr := os.ReadFile(agentsPath)

	if claudeErr != nil && !os.IsNotExist(claudeErr) {
		return nil, claudeErr
	}
	if agentsErr != nil && !os.IsNotExist(agentsErr) {
		return nil, agentsErr
	}

	var wrote []string
	switch {
	case os.IsNotExist(claudeErr) && os.IsNotExist(agentsErr):
		if err := os.WriteFile(claudePath, []byte(starterInstructions), 0o644); err != nil {
			return nil, fmt.Errorf("write starter CLAUDE.md: %w", err)
		}
		if err := os.WriteFile(agentsPath, []byte(starterInstructions), 0o644); err != nil {
			return nil, fmt.Errorf("write starter AGENTS.md: %w", err)
		}
		wrote = append(wrote, "CLAUDE.md", "AGENTS.md")
	case os.IsNotExist(claudeErr):
		if err := os.WriteFile(claudePath, agents, 0o644); err != nil {
			return nil, fmt.Errorf("write starter CLAUDE.md: %w", err)
		}
		wrote = append(wrote, "CLAUDE.md")
	case os.IsNotExist(agentsErr):
		if err := os.WriteFile(agentsPath, claude, 0o644); err != nil {
			return nil, fmt.Errorf("write starter AGENTS.md: %w", err)
		}
		wrote = append(wrote, "AGENTS.md")
	}
	return wrote, nil
}
