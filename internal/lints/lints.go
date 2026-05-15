// Package lints holds the portable lint set spore ships. Each Lint is
// pure stdlib and operates over a project root containing a git
// working tree. Targets are taken from `git ls-files` so untracked
// scratch files do not trigger noise.
package lints

import (
	"bytes"
	"fmt"
	"os/exec"
	"path"
	"path/filepath"
	"sort"
	"strings"
)

// Issue is one finding produced by a Lint. Path is repo-relative.
// Line is 1-indexed; 0 means the issue is whole-file.
//
// Severity and Fingerprint are optional; both are populated by the CLI
// emitter when blank so existing lints stay terse. Severity is one of
// "info", "warn", or "error" (default "warn"). Fingerprint is a stable
// content hash used by `spore scout mint-healers` to dedup findings
// across runs; format is "v<n>:<16-hex>" where the version is bumped
// whenever the lint's semantic output changes.
type Issue struct {
	Path        string
	Line        int
	Message     string
	Severity    string
	Fingerprint string
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
		LeakGuard{},
	}
}

// Named returns every named lint spore knows about, including those
// not in Default(). The map keys are stable Lint.Name() values; the
// values carry default configuration suitable for the nix-config
// layout (overridable by callers wiring their own struct).
//
// Lints not in Default() are project-policy-shaped: they assume a
// specific layout (docs/todo, harness/tech-debt-rulings.md, the
// configs/claude/ hooks render pipeline, the nix-config nix eval
// surfaces, ...). Consumers invoke them by name via
// `spore lint <name>` after their own opt-in.
func Named() map[string]Lint {
	out := map[string]Lint{}
	for _, l := range Default() {
		out[l.Name()] = l
	}
	for _, l := range []Lint{
		TodoPriority{},
		NoCrossRepoTasks{
			ForbiddenSlugs: map[string]string{
				"spore-":    "~/projects/spore",
				"marketer-": "~/projects/marketer",
			},
			ForbiddenPaths: map[string]string{
				"~/projects/spore":              "~/projects/spore",
				"/home/sky/projects/spore":      "~/projects/spore",
				"github.com/versality/spore":    "~/projects/spore",
				"~/projects/marketer":           "~/projects/marketer",
				"/home/sky/projects/marketer":   "~/projects/marketer",
				"github.com/versality/marketer": "~/projects/marketer",
			},
		},
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
