// Package audit is the post-merge integrity probe. For each path
// covered by the configured pathspecs it inspects the HEAD blob, the
// index blob, and the working-tree blob, and reports any path where
// the three disagree along with a best-effort "owner" (commit) for
// each blob. Exit non-zero <=> at least one path drifted.
//
// Pure logic operates over a Git interface so tests can drive it
// without spinning up a real repo. RunLocal wires the interface to
// the local git binary.
package audit

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"sort"
	"strings"
)

// DefaultPathspecs is empty: spore is consumer-agnostic, so callers
// must pass their own pathspecs (positional args to `spore merge audit`
// or `merge_audit.pathspecs` in spore.toml). Without args the audit
// becomes a no-op.
var DefaultPathspecs = []string{}

// OwnerScanLimit is the per-path commit-log scan budget when
// resolving a blob's authoring commit. The shell version exposed
// MERGE_INTEGRITY_OWNER_SCAN; we expose the same knob via Config.
const OwnerScanLimit = 200

// Drift is one row of the audit report.
type Drift struct {
	Path          string
	HEAD          string // blob sha or "-" for absent / "?" for hash-failure
	Index         string
	Worktree      string
	HEADOwner     string
	IndexOwner    string
	WorktreeOwner string
	Status        string // `git status --short` line, empty -> "clean"
}

// Format renders the row in the same shape the shell script printed.
func (d Drift) Format() string {
	owner := func(s string) string {
		if s == "" {
			return "unknown"
		}
		return s
	}
	status := d.Status
	if status == "" {
		status = "clean"
	}
	return fmt.Sprintf("path=%s HEAD=%s index=%s worktree=%s HEADOwner=%s indexOwner=%s worktreeOwner=%s status=%s",
		d.Path, d.HEAD, d.Index, d.Worktree,
		owner(d.HEADOwner), owner(d.IndexOwner), owner(d.WorktreeOwner),
		status)
}

// Git is the data source the audit queries. Implementations may
// shell out to the real binary or fake the responses in a test.
type Git interface {
	// ListPaths returns the union of (HEAD tracked, index, untracked)
	// paths covered by pathspecs.
	ListPaths(pathspecs []string) ([]string, error)
	// BlobAtRef returns the blob SHA at <ref>:<path>, or "-" when
	// absent.
	BlobAtRef(ref, path string) string
	// BlobAtIndex returns the staged blob SHA, or "-" when absent.
	BlobAtIndex(path string) string
	// BlobAtWorktree returns the working-tree blob SHA, "-" if the
	// file is missing, or "?" on hash-object failure.
	BlobAtWorktree(path string) string
	// LogScan returns up to limit commits that touched path. Each
	// entry is "<short> <subject>" (the meta column the shell
	// version emitted).
	LogScan(path string, limit int, noMerges bool) []LogEntry
	// StatusShort returns the first `git status --short` line for
	// path, or "" if clean.
	StatusShort(path string) string
}

// LogEntry pairs a full commit hash with its short+subject metadata.
type LogEntry struct {
	Commit string // full hash
	Meta   string // "<short> <subject>"
}

// Config tunes the run.
type Config struct {
	Pathspecs    []string // empty -> DefaultPathspecs
	OwnerScanLim int      // 0 -> OwnerScanLimit
}

// Run executes the audit and returns the (possibly empty) drift list
// in path-sorted order.
func Run(g Git, cfg Config) ([]Drift, error) {
	if len(cfg.Pathspecs) == 0 {
		cfg.Pathspecs = DefaultPathspecs
	}
	if cfg.OwnerScanLim <= 0 {
		cfg.OwnerScanLim = OwnerScanLimit
	}
	paths, err := g.ListPaths(cfg.Pathspecs)
	if err != nil {
		return nil, err
	}
	uniq := make(map[string]struct{}, len(paths))
	for _, p := range paths {
		if p == "" {
			continue
		}
		uniq[p] = struct{}{}
	}
	sorted := make([]string, 0, len(uniq))
	for p := range uniq {
		sorted = append(sorted, p)
	}
	sort.Strings(sorted)

	var drifts []Drift
	for _, p := range sorted {
		head := g.BlobAtRef("HEAD", p)
		idx := g.BlobAtIndex(p)
		wt := g.BlobAtWorktree(p)
		if head == idx && head == wt {
			continue
		}
		d := Drift{
			Path:          p,
			HEAD:          head,
			Index:         idx,
			Worktree:      wt,
			HEADOwner:     ownerForBlob(g, p, head, cfg.OwnerScanLim),
			IndexOwner:    ownerForBlob(g, p, idx, cfg.OwnerScanLim),
			WorktreeOwner: ownerForBlob(g, p, wt, cfg.OwnerScanLim),
			Status:        g.StatusShort(p),
		}
		drifts = append(drifts, d)
	}
	return drifts, nil
}

// ownerForBlob walks two log passes (no-merges then all) and returns
// the meta column of the first commit whose blob at path matches.
// "-" or "?" blobs short-circuit to "-"; no match -> "unknown".
func ownerForBlob(g Git, path, blob string, limit int) string {
	if blob == "-" || blob == "?" {
		return "-"
	}
	for _, noMerges := range []bool{true, false} {
		entries := g.LogScan(path, limit, noMerges)
		for _, e := range entries {
			if e.Commit == "" {
				continue
			}
			candidate := g.BlobAtRef(e.Commit, path)
			if candidate == blob {
				return e.Meta
			}
		}
	}
	return "unknown"
}

// FormatReport writes the drift list to w in the original shell
// script's textual shape. Returns true iff drifts were reported.
func FormatReport(w io.Writer, drifts []Drift) bool {
	for _, d := range drifts {
		fmt.Fprintln(w, d.Format())
	}
	return len(drifts) > 0
}

// LocalGit drives the audit against a real repo at Root.
type LocalGit struct {
	Root string
}

func (l LocalGit) ListPaths(pathspecs []string) ([]string, error) {
	var out []string
	add := func(args []string) error {
		full := append([]string{"-C", l.Root}, args...)
		full = append(full, "--")
		full = append(full, pathspecs...)
		cmd := exec.Command("git", full...)
		buf, err := cmd.Output()
		if err != nil {
			// `git ls-tree` against an empty tree errors; the
			// shell version swallowed via `|| true`. Mirror.
			return nil
		}
		for _, p := range strings.Split(string(buf), "\x00") {
			if p == "" {
				continue
			}
			out = append(out, p)
		}
		return nil
	}
	_ = add([]string{"ls-tree", "-r", "-z", "--name-only", "HEAD"})
	_ = add([]string{"ls-files", "-z", "--cached"})
	_ = add([]string{"ls-files", "-z", "--others", "--exclude-standard"})
	return out, nil
}

func (l LocalGit) BlobAtRef(ref, path string) string {
	return l.revParse(ref + ":" + path)
}

func (l LocalGit) BlobAtIndex(path string) string {
	return l.revParse(":" + path)
}

func (l LocalGit) BlobAtWorktree(path string) string {
	full := l.Root + "/" + path
	if !exists(full) {
		return "-"
	}
	cmd := exec.Command("git", "-C", l.Root, "hash-object", "--path="+path, "--", path)
	out, err := cmd.Output()
	if err != nil {
		return "?"
	}
	return strings.TrimSpace(string(out))
}

func (l LocalGit) LogScan(path string, limit int, noMerges bool) []LogEntry {
	args := []string{"-C", l.Root, "log", "--all"}
	if noMerges {
		args = append(args, "--no-merges")
	}
	args = append(args, "-n", fmt.Sprintf("%d", limit), "--format=%H\t%h %s", "--", path)
	cmd := exec.Command("git", args...)
	out, err := cmd.Output()
	if err != nil {
		return nil
	}
	var entries []LogEntry
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		if line == "" {
			continue
		}
		tab := strings.IndexByte(line, '\t')
		if tab < 0 {
			continue
		}
		entries = append(entries, LogEntry{
			Commit: line[:tab],
			Meta:   line[tab+1:],
		})
	}
	return entries
}

func (l LocalGit) StatusShort(path string) string {
	cmd := exec.Command("git", "-C", l.Root, "status", "--short", "--untracked-files=all", "--", path)
	out, err := cmd.Output()
	if err != nil {
		return ""
	}
	for _, line := range strings.Split(string(out), "\n") {
		if line != "" {
			return line
		}
	}
	return ""
}

func (l LocalGit) revParse(spec string) string {
	cmd := exec.Command("git", "-C", l.Root, "rev-parse", "-q", "--verify", spec)
	out, err := cmd.Output()
	if err != nil {
		return "-"
	}
	return strings.TrimSpace(string(out))
}

func exists(p string) bool {
	if _, err := os.Lstat(p); err == nil {
		return true
	}
	return false
}
