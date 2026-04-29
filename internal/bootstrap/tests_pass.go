package bootstrap

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// testRecipe describes how to run a project's existing test suite when
// the marker file is present at the project root. Order matters: the
// detector picks the first recipe whose marker is found and whose
// preflight check (if any) passes. Hermetic recipes are preferred
// (they don't need network or per-environment config).
type testRecipe struct {
	Marker  string
	Command []string
	// PreflightFile is an optional extra path that must exist (e.g.
	// `justfile` is enough to try just; but `just check` only works
	// if a `check:` recipe is defined). Empty string means no
	// preflight beyond Marker.
	PreflightFile string
	// PreflightContent, when non-nil, must report true on the marker
	// file's contents for the recipe to be picked.
	PreflightContent func([]byte) bool
}

func recipesFor() []testRecipe {
	hasJustCheck := func(b []byte) bool {
		s := string(b)
		// crude: looks for "check:" at start of line
		for _, line := range strings.Split(s, "\n") {
			if strings.HasPrefix(strings.TrimSpace(line), "check:") {
				return true
			}
		}
		return false
	}
	hasJustTest := func(b []byte) bool {
		s := string(b)
		for _, line := range strings.Split(s, "\n") {
			if strings.HasPrefix(strings.TrimSpace(line), "test:") {
				return true
			}
		}
		return false
	}
	return []testRecipe{
		{
			Marker:           "justfile",
			Command:          []string{"just", "check"},
			PreflightContent: hasJustCheck,
		},
		{
			Marker:           "justfile",
			Command:          []string{"just", "test"},
			PreflightContent: hasJustTest,
		},
		{Marker: "go.mod", Command: []string{"go", "test", "./..."}},
		{Marker: "Cargo.toml", Command: []string{"cargo", "test", "--no-run"}},
		{Marker: "pyproject.toml", Command: []string{"pytest", "-q"}},
		{Marker: "setup.py", Command: []string{"pytest", "-q"}},
		{Marker: "package.json", Command: []string{"npm", "test", "--silent"}},
		{
			Marker:        "Gemfile",
			PreflightFile: ".rspec",
			Command:       []string{"bundle", "exec", "rspec"},
		},
		{
			Marker:        "Gemfile",
			PreflightFile: "Rakefile",
			Command:       []string{"bundle", "exec", "rake", "test"},
		},
	}
}

func detectTestsPass(root string) (string, error) {
	if root == "" {
		return "", errors.New("tests-pass: empty root")
	}
	for _, r := range recipesFor() {
		markerPath := filepath.Join(root, r.Marker)
		b, err := os.ReadFile(markerPath)
		if err != nil {
			continue
		}
		if r.PreflightContent != nil && !r.PreflightContent(b) {
			continue
		}
		if r.PreflightFile != "" {
			if _, err := os.Stat(filepath.Join(root, r.PreflightFile)); err != nil {
				continue
			}
		}
		bin := r.Command[0]
		if _, err := exec.LookPath(bin); err != nil {
			return "", fmt.Errorf("test recipe %v needs %q on PATH; install it or skip the stage", r.Command, bin)
		}
		cmd := exec.Command(bin, r.Command[1:]...)
		cmd.Dir = root
		out, err := cmd.CombinedOutput()
		if err != nil {
			tail := tailLines(out, 20)
			return "", fmt.Errorf("`%s` failed: %v\n%s", strings.Join(r.Command, " "), err, tail)
		}
		return "ran `" + strings.Join(r.Command, " ") + "`", nil
	}
	return "", errors.New("no recognised test recipe (justfile with check:/test:, go.mod, Cargo.toml, pyproject.toml, setup.py, or package.json)")
}

func tailLines(b []byte, n int) string {
	lines := strings.Split(strings.TrimRight(string(b), "\n"), "\n")
	if len(lines) <= n {
		return strings.Join(lines, "\n")
	}
	return strings.Join(lines[len(lines)-n:], "\n")
}
