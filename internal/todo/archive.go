// Package todo provides automation over docs/todo/ specs: the
// markdown briefs that hold multi-session work the harness has
// agreed to remember. ArchiveAged moves stale **Priority**: maybe
// specs to docs/parked/, freeing the active todo board from
// items that have not earned their keep.
package todo

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
)

const (
	defaultAgeDays = 30
	todoDir        = "docs/todo"
	parkedDir      = "docs/parked"
	parkedReadme   = "docs/parked/README.md"
)

const parkedReadmeSeed = `# docs/parked

Specs that didn't earn their keep within 30 days. Resurrect any with
` + "`git mv docs/parked/<slug>.md docs/todo/<slug>.md`" + ` and edit the
Priority field.

## Archived

`

var (
	rePriorityMaybe = regexp.MustCompile(`(?mi)^\*\*Priority\*\*:[[:space:]]*maybe\b`)
	reStatusDone    = regexp.MustCompile(`(?mi)^\*\*Status\*\*:[[:space:]]*done\b`)
)

// ArchiveOptions configures ArchiveAged.
type ArchiveOptions struct {
	// Repo is the repo root. Required. Must contain .git and docs/todo.
	Repo string
	// AgeDays is the archive threshold in days. Zero defaults to 30.
	AgeDays int
	// Now is the reference time for age comparison. Zero defaults to
	// time.Now(). Tests pin this for deterministic thresholds.
	Now time.Time
}

// ArchiveResult reports what ArchiveAged moved.
type ArchiveResult struct {
	// Archived holds the slugs (basename without .md) of every spec
	// moved from docs/todo to docs/parked this run.
	Archived []string
}

// ArchiveAged walks docs/todo/*.md, finds specs whose frontmatter is
// **Priority**: maybe and whose last git commit is older than the
// threshold, and git-mv's them to docs/parked. It also appends a
// dated bullet to docs/parked/README.md per move and seeds that
// README when absent. Idempotent across runs: a spec that already
// exists in docs/parked is skipped.
func ArchiveAged(opts ArchiveOptions) (*ArchiveResult, error) {
	if opts.Repo == "" {
		return nil, fmt.Errorf("archive-aged-maybes: --repo is required")
	}
	repo, err := filepath.Abs(opts.Repo)
	if err != nil {
		return nil, fmt.Errorf("archive-aged-maybes: resolve repo: %w", err)
	}
	if _, err := os.Stat(filepath.Join(repo, ".git")); err != nil {
		return nil, fmt.Errorf("archive-aged-maybes: not a git repo: %s", repo)
	}

	ageDays := opts.AgeDays
	if ageDays <= 0 {
		ageDays = defaultAgeDays
	}
	now := opts.Now
	if now.IsZero() {
		now = time.Now()
	}
	threshold := int64(ageDays) * 86400
	nowUnix := now.Unix()
	today := now.UTC().Format("2006-01-02")

	todoAbs := filepath.Join(repo, todoDir)
	entries, err := os.ReadDir(todoAbs)
	if err != nil {
		if os.IsNotExist(err) {
			return &ArchiveResult{}, nil
		}
		return nil, fmt.Errorf("archive-aged-maybes: read %s: %w", todoDir, err)
	}

	if err := os.MkdirAll(filepath.Join(repo, parkedDir), 0o755); err != nil {
		return nil, fmt.Errorf("archive-aged-maybes: mkdir parked: %w", err)
	}
	readmePath := filepath.Join(repo, parkedReadme)
	if _, err := os.Stat(readmePath); os.IsNotExist(err) {
		if err := os.WriteFile(readmePath, []byte(parkedReadmeSeed), 0o644); err != nil {
			return nil, fmt.Errorf("archive-aged-maybes: seed parked README: %w", err)
		}
	}

	names := make([]string, 0, len(entries))
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		if name == "README.md" || !strings.HasSuffix(name, ".md") {
			continue
		}
		names = append(names, name)
	}
	sort.Strings(names)

	res := &ArchiveResult{}
	for _, name := range names {
		rel := filepath.Join(todoDir, name)
		abs := filepath.Join(repo, rel)
		body, err := os.ReadFile(abs)
		if err != nil {
			return nil, fmt.Errorf("archive-aged-maybes: read %s: %w", rel, err)
		}
		if reStatusDone.Match(body) {
			continue
		}
		if !rePriorityMaybe.Match(body) {
			continue
		}
		last, ok, err := lastCommitUnix(repo, rel)
		if err != nil {
			return nil, err
		}
		if !ok {
			continue
		}
		if nowUnix-last <= threshold {
			continue
		}
		destRel := filepath.Join(parkedDir, name)
		destAbs := filepath.Join(repo, destRel)
		if _, err := os.Stat(destAbs); err == nil {
			continue
		}
		if out, err := gitCmd(repo, "mv", rel, destRel).CombinedOutput(); err != nil {
			return nil, fmt.Errorf("archive-aged-maybes: git mv %s: %w: %s",
				rel, err, strings.TrimSpace(string(out)))
		}
		slug := strings.TrimSuffix(name, ".md")
		if err := appendReadmeLine(readmePath, today, slug); err != nil {
			return nil, err
		}
		res.Archived = append(res.Archived, slug)
	}
	return res, nil
}

func lastCommitUnix(repo, rel string) (int64, bool, error) {
	out, err := gitCmd(repo, "log", "-1", "--format=%at", "--", rel).Output()
	if err != nil {
		return 0, false, fmt.Errorf("archive-aged-maybes: git log %s: %w", rel, err)
	}
	s := strings.TrimSpace(string(out))
	if s == "" {
		return 0, false, nil
	}
	var ts int64
	if _, err := fmt.Sscanf(s, "%d", &ts); err != nil {
		return 0, false, fmt.Errorf("archive-aged-maybes: parse mtime %q: %w", s, err)
	}
	return ts, true, nil
}

func appendReadmeLine(path, today, slug string) error {
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return fmt.Errorf("archive-aged-maybes: open parked README: %w", err)
	}
	defer f.Close()
	if _, err := fmt.Fprintf(f, "- %s: %s\n", today, slug); err != nil {
		return fmt.Errorf("archive-aged-maybes: append parked README: %w", err)
	}
	return nil
}

func gitCmd(repo string, args ...string) *exec.Cmd {
	abs, err := filepath.Abs(repo)
	if err != nil {
		abs = repo
	}
	full := append([]string{"-c", "safe.directory=" + abs, "-C", repo}, args...)
	return exec.Command("git", full...)
}
