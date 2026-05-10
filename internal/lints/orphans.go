package lints

import (
	"bufio"
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
)

// Orphans walks one or more top-level Roots and reports each direct
// child (file or directory) that no .nix file under SearchDir
// references. The SearchDir excludes ExcludePrefix when set so prose
// files about the convention itself don't satisfy the wiring check.
//
// Allowlist is a path set: entries (e.g. `configs/broadlink`,
// `bash/foo.sh`) listed in AllowlistFile (relative to root) are
// skipped. Defaults match nix-config: Roots=[configs bash],
// SearchDir=nix, ExcludePrefix=nix/harness/, AllowlistFile=
// harness/orphans-allowlist.
type Orphans struct {
	Roots         []string
	SearchDir     string
	ExcludePrefix string
	AllowlistFile string
}

func (Orphans) Name() string { return "orphans" }

func (l Orphans) Run(root string) ([]Issue, error) {
	roots := l.Roots
	if len(roots) == 0 {
		roots = []string{"configs", "bash"}
	}
	searchDir := l.SearchDir
	if searchDir == "" {
		searchDir = "nix"
	}
	excludePrefix := l.ExcludePrefix
	if excludePrefix == "" {
		excludePrefix = "nix/harness/"
	}
	allowlistFile := l.AllowlistFile
	if allowlistFile == "" {
		allowlistFile = "harness/orphans-allowlist"
	}

	allowed, err := readAllowlist(filepath.Join(root, allowlistFile))
	if err != nil {
		return nil, err
	}

	var issues []Issue
	for _, r := range roots {
		dir := filepath.Join(root, r)
		entries, err := os.ReadDir(dir)
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			return nil, err
		}
		var names []string
		for _, e := range entries {
			names = append(names, e.Name())
		}
		sort.Strings(names)
		for _, name := range names {
			rel := filepath.ToSlash(filepath.Join(r, name))
			if allowed[rel] {
				continue
			}
			ok, err := referencedInNix(root, rel, searchDir, excludePrefix)
			if err != nil {
				return nil, err
			}
			if !ok {
				issues = append(issues, Issue{
					Path:    rel,
					Message: fmt.Sprintf("no %s/ reference - wire it in a nix module or add to %s", searchDir, allowlistFile),
				})
			}
		}
	}
	return issues, nil
}

func readAllowlist(path string) (map[string]bool, error) {
	f, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return map[string]bool{}, nil
		}
		return nil, err
	}
	defer f.Close()
	out := map[string]bool{}
	s := bufio.NewScanner(f)
	for s.Scan() {
		line := s.Text()
		if i := strings.IndexByte(line, '#'); i >= 0 {
			line = line[:i]
		}
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		out[line] = true
	}
	return out, s.Err()
}

func referencedInNix(root, needle, searchDir, excludePrefix string) (bool, error) {
	abs, err := filepath.Abs(root)
	if err != nil {
		return false, err
	}
	args := []string{
		"-c", "safe.directory=" + abs,
		"-C", root,
		"grep", "--quiet",
		needle,
		"--",
		searchDir + "/**/*.nix",
	}
	if excludePrefix != "" {
		args = append(args, ":!"+excludePrefix+"**")
	}
	cmd := exec.Command("git", args...)
	var errBuf bytes.Buffer
	cmd.Stderr = &errBuf
	if err := cmd.Run(); err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			if exitErr.ExitCode() == 1 {
				return false, nil
			}
		}
		return false, fmt.Errorf("git grep: %w (%s)", err, strings.TrimSpace(errBuf.String()))
	}
	return true, nil
}
