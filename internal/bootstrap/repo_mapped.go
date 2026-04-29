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

// starterClaude is the minimum CLAUDE.md spore drops when the project
// has none. It points at `spore compose` for the real per-project
// rendering; this is the seed.
const starterClaude = `# CLAUDE.md

This project uses spore for agent governance. Run ` + "`spore compose --consumer <name>`" + ` to
render the per-project rule set into this file once a consumer list
exists under ` + "`rules/consumers/`" + `.

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

	claudePath := filepath.Join(root, "CLAUDE.md")
	wroteStarter := false
	if _, err := os.Stat(claudePath); err != nil {
		if !os.IsNotExist(err) {
			return "", err
		}
		if err := os.WriteFile(claudePath, []byte(starterClaude), 0o644); err != nil {
			return "", fmt.Errorf("write starter CLAUDE.md: %w", err)
		}
		wroteStarter = true
	}
	skills, err := install.Install(root, spore.BundledSkills, "bootstrap/skills")
	if err != nil {
		return "", fmt.Errorf("install skills: %w", err)
	}

	notes := "detected: " + strings.Join(hits, ",")
	if wroteStarter {
		notes += "; wrote starter CLAUDE.md"
	}
	if len(skills.Written) > 0 {
		notes += fmt.Sprintf("; installed %d skill file(s)", len(skills.Written))
	}
	return notes, nil
}
