// Package lints holds the portable lint set spore ships. Each Lint is
// pure stdlib and operates over a project root containing a git
// working tree. Targets are taken from `git ls-files` so untracked
// scratch files do not trigger noise.
package lints

import (
	"bytes"
	"fmt"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
)

// Issue is one finding produced by a Lint. Path is repo-relative.
// Line is 1-indexed; 0 means the issue is whole-file.
type Issue struct {
	Path    string
	Line    int
	Message string
}

func (i Issue) String() string {
	if i.Line > 0 {
		return fmt.Sprintf("%s:%d: %s", i.Path, i.Line, i.Message)
	}
	return fmt.Sprintf("%s: %s", i.Path, i.Message)
}

// Lint is the contract every lint implements.
type Lint interface {
	Name() string
	Run(root string) ([]Issue, error)
}

// Default returns the lint set spore runs by default.
func Default() []Lint {
	return []Lint{
		EmDash{},
		FileSize{Limit: 500},
		CommentNoise{},
		ClaudeDrift{ConsumersDir: "rules/consumers", RulesDir: "rules"},
	}
}

// listFiles runs `git ls-files` rooted at root. extOnly, when
// non-empty, filters results to repo-relative paths whose extension is
// in the set; basenames in extOnly (e.g. "Makefile") match by name.
//
// The git invocation passes `-c safe.directory=<abs root>` so a repo
// imported via rsync (which preserves the remote uid and trips git's
// "dubious ownership" guard) still lints. The narrower form is used
// instead of `*` so we only trust the path being linted.
func listFiles(root string, extOnly map[string]bool) ([]string, error) {
	abs, err := filepath.Abs(root)
	if err != nil {
		return nil, fmt.Errorf("resolve root: %w", err)
	}
	cmd := exec.Command("git", "-c", "safe.directory="+abs, "-C", root, "ls-files")
	var out, errBuf bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &errBuf
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("git ls-files: %w (%s)", err, strings.TrimSpace(errBuf.String()))
	}
	var paths []string
	for _, line := range strings.Split(out.String(), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		if extOnly != nil {
			ext := strings.ToLower(filepath.Ext(line))
			base := filepath.Base(line)
			if !extOnly[ext] && !extOnly[base] {
				continue
			}
		}
		paths = append(paths, line)
	}
	sort.Strings(paths)
	return paths, nil
}

// sourceExts is the default file-extension set treated as "source code"
// for lints that only make sense on code (comment-noise, file-size).
// Markdown / data files are out of scope by design.
var sourceExts = map[string]bool{
	".go":   true,
	".sh":   true,
	".bash": true,
	".nix":  true,
	".py":   true,
	".rs":   true,
	".js":   true,
	".ts":   true,
	".rb":   true,
	".lua":  true,
	".clj":  true,
	".cljs": true,
	".cljc": true,
	".bb":   true,
	".edn":  true,
}

// generatedExacts, generatedSuffixes, and generatedDirs encode the
// default set of files spore treats as "generated, not authored". Both
// filesize and comment-noise skip these because the operator cannot
// fix a finding at the source: regenerating overwrites any local edit.
// The list is deliberately conservative; per-project allowlists remain
// out of scope per the comment-noise design note.
var (
	generatedExacts = map[string]bool{
		"db/schema.rb":     true,
		"db/structure.sql": true,
	}
	generatedSuffixes = []string{
		".pb.go",
		".pb.gw.go",
		"_generated.go",
		"_gen.go",
	}
	generatedDirs = []string{
		"sorbet/rbi/",
	}
)

// isGenerated reports whether rel (a forward-slash, repo-relative path
// from listFiles) matches the built-in generated-file set.
func isGenerated(rel string) bool {
	if generatedExacts[rel] {
		return true
	}
	for _, sfx := range generatedSuffixes {
		if strings.HasSuffix(rel, sfx) {
			return true
		}
	}
	for _, dir := range generatedDirs {
		if strings.HasPrefix(rel, dir) {
			return true
		}
	}
	return false
}
