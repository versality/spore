package todo

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestArchiveAgedMovesOldMaybeSkipsFreshAndDoneAndOther(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skipf("git not available: %v", err)
	}
	repo := newRepo(t)

	// Aged maybe -> should move.
	writeAndCommitDated(t, repo, "docs/todo/aged-maybe.md",
		"**Priority**: maybe\n\nbody\n", "2026-01-01T00:00:00")
	// Fresh maybe -> should stay.
	writeAndCommitDated(t, repo, "docs/todo/fresh-maybe.md",
		"**Priority**: maybe\n\nbody\n", "2026-05-10T00:00:00")
	// Done maybe -> should stay regardless of age.
	writeAndCommitDated(t, repo, "docs/todo/done-maybe.md",
		"**Status**: done\n**Priority**: maybe\n\nbody\n", "2026-01-01T00:00:00")
	// Non-maybe -> should stay.
	writeAndCommitDated(t, repo, "docs/todo/high.md",
		"**Priority**: high\n\nbody\n", "2026-01-01T00:00:00")
	// README -> should be ignored even if it parses.
	writeAndCommitDated(t, repo, "docs/todo/README.md",
		"# todo\n", "2026-01-01T00:00:00")

	now := mustParse(t, "2026-05-16T00:00:00")
	res, err := ArchiveAged(ArchiveOptions{Repo: repo, Now: now})
	if err != nil {
		t.Fatalf("ArchiveAged: %v", err)
	}
	if got, want := res.Archived, []string{"aged-maybe"}; !equal(got, want) {
		t.Fatalf("Archived = %v, want %v", got, want)
	}

	mustExist(t, filepath.Join(repo, "docs/parked/aged-maybe.md"))
	mustMissing(t, filepath.Join(repo, "docs/todo/aged-maybe.md"))
	mustExist(t, filepath.Join(repo, "docs/todo/fresh-maybe.md"))
	mustExist(t, filepath.Join(repo, "docs/todo/done-maybe.md"))
	mustExist(t, filepath.Join(repo, "docs/todo/high.md"))

	// Parked README must exist, be seeded, and contain a dated bullet.
	readme := readFile(t, filepath.Join(repo, "docs/parked/README.md"))
	if !strings.Contains(readme, "## Archived") {
		t.Errorf("parked README missing seed:\n%s", readme)
	}
	if !strings.Contains(readme, "- 2026-05-16: aged-maybe\n") {
		t.Errorf("parked README missing bullet:\n%s", readme)
	}
}

func TestArchiveAgedIdempotent(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skipf("git not available: %v", err)
	}
	repo := newRepo(t)
	writeAndCommitDated(t, repo, "docs/todo/aged-maybe.md",
		"**Priority**: maybe\n\nbody\n", "2026-01-01T00:00:00")

	now := mustParse(t, "2026-05-16T00:00:00")
	first, err := ArchiveAged(ArchiveOptions{Repo: repo, Now: now})
	if err != nil {
		t.Fatalf("first ArchiveAged: %v", err)
	}
	if len(first.Archived) != 1 {
		t.Fatalf("first run Archived = %v, want 1", first.Archived)
	}
	// Commit the move so the second run sees a clean tree.
	runGit(t, repo, "commit", "-q", "-m", "park aged-maybe")

	second, err := ArchiveAged(ArchiveOptions{Repo: repo, Now: now})
	if err != nil {
		t.Fatalf("second ArchiveAged: %v", err)
	}
	if len(second.Archived) != 0 {
		t.Fatalf("second run Archived = %v, want empty", second.Archived)
	}
}

func TestArchiveAgedRespectsAgeDays(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skipf("git not available: %v", err)
	}
	repo := newRepo(t)
	writeAndCommitDated(t, repo, "docs/todo/m.md",
		"**Priority**: maybe\n", "2026-05-01T00:00:00")

	now := mustParse(t, "2026-05-16T00:00:00")

	// 30-day threshold: not aged yet.
	res, err := ArchiveAged(ArchiveOptions{Repo: repo, Now: now})
	if err != nil {
		t.Fatalf("default: %v", err)
	}
	if len(res.Archived) != 0 {
		t.Fatalf("default threshold Archived = %v, want empty", res.Archived)
	}

	// 7-day threshold: aged.
	res, err = ArchiveAged(ArchiveOptions{Repo: repo, Now: now, AgeDays: 7})
	if err != nil {
		t.Fatalf("custom: %v", err)
	}
	if got, want := res.Archived, []string{"m"}; !equal(got, want) {
		t.Fatalf("AgeDays=7 Archived = %v, want %v", got, want)
	}
}

func TestArchiveAgedSkipsUncommittedFile(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skipf("git not available: %v", err)
	}
	repo := newRepo(t)
	// Untracked maybe spec: no git history, no archiving.
	mustMkdirAll(t, filepath.Join(repo, "docs/todo"))
	if err := os.WriteFile(filepath.Join(repo, "docs/todo/untracked.md"),
		[]byte("**Priority**: maybe\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	now := mustParse(t, "2026-05-16T00:00:00")
	res, err := ArchiveAged(ArchiveOptions{Repo: repo, Now: now})
	if err != nil {
		t.Fatalf("ArchiveAged: %v", err)
	}
	if len(res.Archived) != 0 {
		t.Fatalf("Archived = %v, want empty", res.Archived)
	}
	mustExist(t, filepath.Join(repo, "docs/todo/untracked.md"))
}

func TestArchiveAgedMissingTodoDirNoop(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skipf("git not available: %v", err)
	}
	repo := newRepo(t)
	// No docs/todo: still a clean exit.
	res, err := ArchiveAged(ArchiveOptions{Repo: repo})
	if err != nil {
		t.Fatalf("ArchiveAged: %v", err)
	}
	if len(res.Archived) != 0 {
		t.Fatalf("Archived = %v, want empty", res.Archived)
	}
}

func TestArchiveAgedMissingRepoErrs(t *testing.T) {
	if _, err := ArchiveAged(ArchiveOptions{}); err == nil {
		t.Fatal("want error for empty repo")
	}
	if _, err := ArchiveAged(ArchiveOptions{Repo: t.TempDir()}); err == nil {
		t.Fatal("want error for non-git dir")
	}
}

func TestArchiveAgedSkipsExistingParkedDest(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skipf("git not available: %v", err)
	}
	repo := newRepo(t)
	writeAndCommitDated(t, repo, "docs/todo/clash.md",
		"**Priority**: maybe\n", "2026-01-01T00:00:00")
	// Pre-seed a parked file with the same basename.
	writeAndCommitDated(t, repo, "docs/parked/clash.md",
		"already-parked\n", "2026-01-02T00:00:00")

	now := mustParse(t, "2026-05-16T00:00:00")
	res, err := ArchiveAged(ArchiveOptions{Repo: repo, Now: now})
	if err != nil {
		t.Fatalf("ArchiveAged: %v", err)
	}
	if len(res.Archived) != 0 {
		t.Fatalf("Archived = %v, want empty (clash should skip)", res.Archived)
	}
	mustExist(t, filepath.Join(repo, "docs/todo/clash.md"))
}

// helpers

func newRepo(t *testing.T) string {
	t.Helper()
	repo := t.TempDir()
	runGit(t, repo, "init", "-q", "-b", "main")
	runGit(t, repo, "config", "user.email", "test@example.com")
	runGit(t, repo, "config", "user.name", "Test")
	runGit(t, repo, "commit", "-q", "--allow-empty", "-m", "init")
	return repo
}

func writeAndCommitDated(t *testing.T, repo, rel, body, isoDate string) {
	t.Helper()
	abs := filepath.Join(repo, rel)
	mustMkdirAll(t, filepath.Dir(abs))
	if err := os.WriteFile(abs, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	runGit(t, repo, "add", "--", rel)
	cmd := exec.Command("git", "-C", repo, "commit", "-q",
		"--date="+isoDate, "-m", "add "+rel)
	cmd.Env = append(os.Environ(),
		"GIT_COMMITTER_DATE="+isoDate,
		"GIT_AUTHOR_DATE="+isoDate,
	)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git commit %s: %v: %s", rel, err, out)
	}
}

func runGit(t *testing.T, repo string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", repo}, args...)...)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v: %s", args, err, out)
	}
}

func mustMkdirAll(t *testing.T, dir string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
}

func mustExist(t *testing.T, p string) {
	t.Helper()
	if _, err := os.Stat(p); err != nil {
		t.Errorf("missing %s: %v", p, err)
	}
}

func mustMissing(t *testing.T, p string) {
	t.Helper()
	if _, err := os.Stat(p); err == nil {
		t.Errorf("expected %s to be gone", p)
	}
}

func readFile(t *testing.T, p string) string {
	t.Helper()
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func mustParse(t *testing.T, iso string) time.Time {
	t.Helper()
	ts, err := time.Parse("2006-01-02T15:04:05", iso)
	if err != nil {
		t.Fatal(err)
	}
	return ts
}

func equal(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
