// Package lints holds the portable lint set spore ships. Each Lint is
// pure stdlib and operates over a project root containing a git
// working tree. Targets are taken from `git ls-files` so untracked
// scratch files do not trigger noise.
package lints

import (
	"bufio"
	"bytes"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"sort"
	"strings"
)

// Issue is one finding produced by a Lint. Path is repo-relative.
// Line is 1-indexed; 0 means the issue is whole-file.
//
// The CLI emitter derives severity and a stable fingerprint from the
// (lint, path, line, message) tuple at output time; lints only fill
// these three fields.
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
		FileSize{Limit: defaultFileSizeLimit},
		CommentNoise{},
		Decoration{},
		ClaudeDrift{ConsumersDir: "rules/consumers", RulesDir: "rules"},
		ClaudeTotalSize{},
		AgentMirror{},
		TaskBrief{},
		TaskEvidence{TasksDir: "tasks"},
		TaskStatus{},
		TmuxSocketTest{},
	}
}

// Named returns every named lint spore knows about, including those
// not in Default(). The map keys are stable Lint.Name() values; the
// values are zero-valued structs that consumers configure via
// spore.toml [lint.<name>] or by wiring their own struct in Go.
//
// Lints not in Default() are project-policy-shaped: they assume a
// specific layout (docs/todo, harness/tech-debt-rulings.md, the
// configs/claude/ hooks render pipeline, ...). Consumers invoke them
// by name via `spore lint <name>` after their own opt-in.
//
// no-cross-repo-tasks in particular ships with empty maps; the
// kernel does not carry consumer-specific paths or slug prefixes.
// Configure via [lint.no-cross-repo-tasks] in spore.toml.
func Named() map[string]Lint {
	out := map[string]Lint{}
	for _, l := range Default() {
		out[l.Name()] = l
	}
	for _, l := range []Lint{
		TodoPriority{},
		NoCrossRepoTasks{},
		Orphans{},
		OverviewDrift{},
		PlanFirstRequired{},
		CodexEffortHighOnly{},
		HooksDrift{},
		TechDebtRulings{},
		TaskDoneZeroCommits{},
		UserSkillsParity{},
		CaptureSignalCoverage{},
		ClaudeSize{},
		ClaudeSubdir{},
		TaskPriority{},
		FlakeInputShadow{},
		Agenix{},
		AgentKillSwitches{},
		TaskSchedulerContext{},
		TaskNeeds{},
	} {
		out[l.Name()] = l
	}
	return out
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

func extSet(exts []string, defaults map[string]bool) map[string]bool {
	if len(exts) == 0 {
		return defaults
	}
	out := map[string]bool{}
	for _, ext := range exts {
		ext = strings.TrimSpace(ext)
		if ext == "" {
			continue
		}
		if strings.Contains(ext, ".") && !strings.HasPrefix(ext, ".") {
			out[filepath.ToSlash(ext)] = true
			continue
		}
		out[ext] = true
	}
	return out
}

// ownSlug returns the worktree slug for root: the current branch with
// the `wt/` prefix stripped, or "" when HEAD is not a wt/ branch (or
// git fails). The git call passes `-c safe.directory=<abs root>` so a
// repo imported via rsync (preserving a foreign uid) still resolves.
func ownSlug(root string) string {
	abs, err := filepath.Abs(root)
	if err != nil {
		return ""
	}
	cmd := exec.Command("git", "-c", "safe.directory="+abs, "-C", root, "symbolic-ref", "--short", "HEAD")
	var out bytes.Buffer
	cmd.Stdout = &out
	if err := cmd.Run(); err != nil {
		return ""
	}
	branch := strings.TrimSpace(out.String())
	if !strings.HasPrefix(branch, "wt/") {
		return ""
	}
	return strings.TrimPrefix(branch, "wt/")
}

// newLineScanner returns a bufio.Scanner over r sized for the long
// lines spore lints routinely meet (minified assets, generated code):
// a 64 KiB initial buffer growing to 4 MiB.
func newLineScanner(r io.Reader) *bufio.Scanner {
	s := bufio.NewScanner(r)
	s.Buffer(make([]byte, 64*1024), 4*1024*1024)
	return s
}

// countLines returns the number of newline-delimited lines in the file
// at path, using the shared large-buffer scanner.
func countLines(path string) (int, error) {
	f, err := os.Open(path)
	if err != nil {
		return 0, err
	}
	defer f.Close()
	scanner := newLineScanner(f)
	n := 0
	for scanner.Scan() {
		n++
	}
	return n, scanner.Err()
}

// scanDirsConfigured reports whether dirs names at least one concrete
// directory to scan (anything other than empty or "."). When false the
// caller scans the whole tree.
func scanDirsConfigured(dirs []string) bool {
	for _, d := range dirs {
		if s := strings.TrimSpace(d); s != "" && s != "." {
			return true
		}
	}
	return false
}

// inScanDirs reports whether the repo-relative path rel falls under any
// directory in dirs. An empty or "." entry matches everything.
func inScanDirs(rel string, dirs []string) bool {
	rel = filepath.ToSlash(rel)
	for _, d := range dirs {
		d = strings.TrimSpace(filepath.ToSlash(d))
		if d == "" || d == "." {
			return true
		}
		d = strings.TrimSuffix(d, "/")
		if rel == d || strings.HasPrefix(rel, d+"/") {
			return true
		}
	}
	return false
}

func skipPath(rel string, skips []string) bool {
	rel = filepath.ToSlash(rel)
	for _, skip := range skips {
		skip = filepath.ToSlash(strings.TrimSpace(skip))
		if skip == "" {
			continue
		}
		if strings.HasSuffix(skip, "/") && strings.HasPrefix(rel, skip) {
			return true
		}
		if rel == skip {
			return true
		}
		if ok, _ := path.Match(skip, rel); ok {
			return true
		}
	}
	return false
}
