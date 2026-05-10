package lints

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
)

// CaptureSignalCoverage asserts that every entry in the
// "bounded set" (just recipes + a configured BoundedFiles globbed
// list) has a row under `## Coverage Matrix` in DocPath. Defaults
// match nix-config: DocPath=docs/todo/harness-universal-error-warning.md,
// BoundedFiles populated from the nix-config layout.
type CaptureSignalCoverage struct {
	DocPath      string
	BoundedFiles []string
}

func (CaptureSignalCoverage) Name() string { return "capture-signal-coverage" }

// DefaultCaptureSignalBoundedFiles enumerates the file paths
// nix-config tracks. Glob entries (containing `*`) are expanded
// against the project root at lint time.
var DefaultCaptureSignalBoundedFiles = []string{
	"nix/packages/wt/skyhelm-*",
	"nix/packages/wt/codex-*",
	"nix/packages/wt/wt",
	"nix/packages/wt/wt-task",
	"nix/packages/wt/rower-token-monitor",
	"nix/packages/wt/spore-context-tee",
	"nix/packages/wt-go/cmd/wt-go/main.go",
	"nix/packages/sky-harness/*.sh",
	"nix/packages/skyler-tools/*.sh",
	"harness/skyhelm-boot",
	"harness/skyhelm-no-source-edits.sh",
	"harness/opencode-*.sh",
}

func (l CaptureSignalCoverage) Run(root string) ([]Issue, error) {
	docPath := l.DocPath
	if docPath == "" {
		docPath = "docs/todo/harness-universal-error-warning.md"
	}
	bounded := l.BoundedFiles
	if bounded == nil {
		bounded = DefaultCaptureSignalBoundedFiles
	}

	docAbs := filepath.Join(root, docPath)
	raw, err := os.ReadFile(docAbs)
	if err != nil {
		if os.IsNotExist(err) {
			return []Issue{{
				Path:    docPath,
				Message: "missing doc",
			}}, nil
		}
		return nil, err
	}
	literals, globs := parseCoverageMatrix(raw)
	if len(literals) == 0 && len(globs) == 0 {
		return []Issue{{
			Path:    docPath,
			Message: "could not parse any rows from ## Coverage Matrix",
		}}, nil
	}

	var ids []string
	recipes, ok, err := justRecipes(root)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, nil
	}
	for _, r := range recipes {
		ids = append(ids, "just "+r)
	}

	for _, pat := range bounded {
		matched, err := filepath.Glob(filepath.Join(root, pat))
		if err != nil {
			return nil, err
		}
		for _, m := range matched {
			rel, err := filepath.Rel(root, m)
			if err != nil {
				return nil, err
			}
			ids = append(ids, filepath.ToSlash(rel))
		}
	}
	sort.Strings(ids)

	covers := func(id string) bool {
		for _, lit := range literals {
			if id == lit {
				return true
			}
		}
		for _, g := range globs {
			if matchedGlob(id, g) {
				return true
			}
		}
		return false
	}

	var issues []Issue
	for _, id := range ids {
		if !covers(id) {
			issues = append(issues, Issue{
				Path:    docPath,
				Message: fmt.Sprintf("missing matrix row for %s (add coverage: direct|transitive|unwrapped|oos)", id),
			})
		}
	}
	return issues, nil
}

// parseCoverageMatrix extracts the first column of every data row
// under "## Coverage Matrix". Returns literals and glob entries
// (those containing `*`) separately.
func parseCoverageMatrix(raw []byte) (literals, globs []string) {
	in := false
	for _, ln := range strings.Split(string(raw), "\n") {
		if strings.HasPrefix(ln, "## Coverage Matrix") {
			in = true
			continue
		}
		if in && strings.HasPrefix(ln, "## ") {
			break
		}
		if !in {
			continue
		}
		if !strings.HasPrefix(ln, "| ") {
			continue
		}
		first := strings.TrimSpace(strings.SplitN(ln, "|", 3)[1])
		if first == "" || first == "id" {
			continue
		}
		if strings.HasPrefix(first, "---") {
			continue
		}
		if strings.Contains(first, "*") {
			globs = append(globs, first)
		} else {
			literals = append(literals, first)
		}
	}
	return literals, globs
}

// justRecipes returns the recipe names from `just --summary`. ok is
// false when just is not on PATH; callers treat that as "skip".
func justRecipes(root string) ([]string, bool, error) {
	if _, err := exec.LookPath("just"); err != nil {
		return nil, false, nil
	}
	cmd := exec.Command("just", "--summary")
	cmd.Dir = root
	var out, errBuf bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &errBuf
	if err := cmd.Run(); err != nil {
		return nil, false, fmt.Errorf("just --summary: %w (%s)", err, strings.TrimSpace(errBuf.String()))
	}
	fields := strings.Fields(out.String())
	seen := map[string]bool{}
	var recipes []string
	for _, f := range fields {
		if !seen[f] {
			seen[f] = true
			recipes = append(recipes, f)
		}
	}
	sort.Strings(recipes)
	return recipes, true, nil
}

// matchedGlob applies bash-glob style `*` matching of pat against id.
func matchedGlob(id, pat string) bool {
	ok, _ := filepath.Match(pat, id)
	return ok
}
