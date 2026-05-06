package task

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/versality/spore/evidence"
)

func TestMergeNoBranch(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skipf("git not available: %v", err)
	}

	repo := t.TempDir()
	t.Chdir(repo)

	runGit(t, repo, "init", "-q", "-b", "main")
	runGit(t, repo, "config", "user.email", "test@example.com")
	runGit(t, repo, "config", "user.name", "Test")
	runGit(t, repo, "commit", "-q", "--allow-empty", "-m", "init")

	tasksDir := filepath.Join(repo, "tasks")
	if err := os.MkdirAll(tasksDir, 0o755); err != nil {
		t.Fatal(err)
	}

	err := Merge(tasksDir, "nonexistent")
	if err == nil {
		t.Fatal("expected error for missing branch, got nil")
	}
}

func TestMergeFastForward(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skipf("git not available: %v", err)
	}

	repo := t.TempDir()
	t.Chdir(repo)

	runGit(t, repo, "init", "-q", "-b", "main")
	runGit(t, repo, "config", "user.email", "test@example.com")
	runGit(t, repo, "config", "user.name", "Test")
	runGit(t, repo, "commit", "-q", "--allow-empty", "-m", "init")
	configureOrigin(t, repo)

	// Create the wt/<slug> branch with a commit ahead of main.
	runGit(t, repo, "checkout", "-q", "-b", "wt/demo")
	runGit(t, repo, "commit", "-q", "--allow-empty", "-m", "feat: demo work")
	runGit(t, repo, "checkout", "-q", "main")

	tasksDir := filepath.Join(repo, "tasks")
	if err := os.MkdirAll(tasksDir, 0o755); err != nil {
		t.Fatal(err)
	}

	if err := Merge(tasksDir, "demo"); err != nil {
		t.Fatalf("Merge: %v", err)
	}

	// The branch should be deleted after merge.
	if branchExists(repo, "wt/demo") {
		t.Error("wt/demo branch still exists after Merge")
	}

	// main should include the feature commit.
	out, err := exec.Command("git", "-C", repo, "log", "--oneline").Output()
	if err != nil {
		t.Fatal(err)
	}
	if countLines(string(out)) < 2 {
		t.Errorf("expected at least 2 commits on main after merge, got:\n%s", out)
	}
}

func TestMergeFlipsTaskDoneAndCommitsClose(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skipf("git not available: %v", err)
	}

	repo := t.TempDir()
	t.Chdir(repo)
	t.Setenv("XDG_STATE_HOME", t.TempDir())

	runGit(t, repo, "init", "-q", "-b", "main")
	runGit(t, repo, "config", "user.email", "test@example.com")
	runGit(t, repo, "config", "user.name", "Test")

	tasksDir := filepath.Join(repo, "tasks")
	if err := os.MkdirAll(tasksDir, 0o755); err != nil {
		t.Fatal(err)
	}
	taskPath := filepath.Join(tasksDir, "demo.md")
	if err := os.WriteFile(taskPath, []byte("---\nstatus: active\nslug: demo\ntitle: Demo\n---\nbody\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runGit(t, repo, "add", ".")
	runGit(t, repo, "commit", "-q", "-m", "task active")
	configureOrigin(t, repo)

	runGit(t, repo, "checkout", "-q", "-b", "wt/demo")
	if err := os.WriteFile(filepath.Join(repo, "feature.txt"), []byte("feature\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runGit(t, repo, "add", "feature.txt")
	runGit(t, repo, "commit", "-q", "-m", "feat: demo work")
	runGit(t, repo, "checkout", "-q", "main")

	if err := Merge(tasksDir, "demo"); err != nil {
		t.Fatalf("Merge: %v", err)
	}
	if status := readStatus(t, taskPath); status != "done" {
		t.Errorf("status = %q want done", status)
	}
	out, err := exec.Command("git", "-C", repo, "log", "--oneline", "-1").Output()
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(out), "tasks/demo: status -> done") {
		t.Errorf("last commit should close task, got:\n%s", out)
	}
}

func TestMergeNoDeltaStillClosesTask(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skipf("git not available: %v", err)
	}

	repo := t.TempDir()
	t.Chdir(repo)
	t.Setenv("XDG_STATE_HOME", t.TempDir())

	runGit(t, repo, "init", "-q", "-b", "main")
	runGit(t, repo, "config", "user.email", "test@example.com")
	runGit(t, repo, "config", "user.name", "Test")

	tasksDir := filepath.Join(repo, "tasks")
	if err := os.MkdirAll(tasksDir, 0o755); err != nil {
		t.Fatal(err)
	}
	taskPath := filepath.Join(tasksDir, "demo.md")
	if err := os.WriteFile(taskPath, []byte("---\nstatus: active\nslug: demo\ntitle: Demo\n---\nbody\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runGit(t, repo, "add", ".")
	runGit(t, repo, "commit", "-q", "-m", "task active")
	configureOrigin(t, repo)
	runGit(t, repo, "branch", "wt/demo")

	if err := Merge(tasksDir, "demo"); err != nil {
		t.Fatalf("Merge: %v", err)
	}
	if status := readStatus(t, taskPath); status != "done" {
		t.Errorf("status = %q want done", status)
	}
	if branchExists(repo, "wt/demo") {
		t.Error("wt/demo branch still exists after no-delta close")
	}
}

func TestMergeCloseGateFailureKeepsBranchAndWorktree(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skipf("git not available: %v", err)
	}

	repo := t.TempDir()
	t.Chdir(repo)
	t.Setenv("XDG_STATE_HOME", t.TempDir())

	runGit(t, repo, "init", "-q", "-b", "main")
	runGit(t, repo, "config", "user.email", "test@example.com")
	runGit(t, repo, "config", "user.name", "Test")

	tasksDir := filepath.Join(repo, "tasks")
	if err := os.MkdirAll(tasksDir, 0o755); err != nil {
		t.Fatal(err)
	}
	taskPath := filepath.Join(tasksDir, "demo.md")
	body := "---\nstatus: active\nslug: demo\ntitle: Demo\nevidence_required: [commit]\n---\n## Evidence\n- commit:\n"
	if err := os.WriteFile(taskPath, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	runGit(t, repo, "add", ".")
	runGit(t, repo, "commit", "-q", "-m", "task active")

	worktree := filepath.Join(repo, ".worktrees", "demo")
	runGit(t, repo, "worktree", "add", "-q", "-b", "wt/demo", worktree, "main")
	if err := os.WriteFile(filepath.Join(worktree, "feature.txt"), []byte("feature\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runGit(t, worktree, "add", "feature.txt")
	runGit(t, worktree, "commit", "-q", "-m", "feat: demo work")

	origStart := evidence.ContractStart
	evidence.ContractStart = time.Date(2000, 1, 1, 0, 0, 0, 0, time.UTC)
	t.Cleanup(func() { evidence.ContractStart = origStart })

	err := Merge(tasksDir, "demo")
	if err == nil {
		t.Fatal("Merge should fail when close gate fails")
	}
	if !strings.Contains(err.Error(), "evidence verdict") {
		t.Errorf("error = %q should mention evidence verdict", err)
	}
	if status := readStatus(t, taskPath); status != "active" {
		t.Errorf("status = %q want active", status)
	}
	if !branchExists(repo, "wt/demo") {
		t.Error("wt/demo branch was deleted despite close gate failure")
	}
	if _, statErr := os.Stat(worktree); statErr != nil {
		t.Errorf("worktree missing despite close gate failure: %v", statErr)
	}
}

func TestMergeRefusesNonFFOnDivergedBranch(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skipf("git not available: %v", err)
	}

	repo := t.TempDir()
	t.Chdir(repo)

	runGit(t, repo, "init", "-q", "-b", "main")
	runGit(t, repo, "config", "user.email", "test@example.com")
	runGit(t, repo, "config", "user.name", "Test")
	runGit(t, repo, "commit", "-q", "--allow-empty", "-m", "init")

	// Create diverging branch.
	runGit(t, repo, "checkout", "-q", "-b", "wt/demo")
	runGit(t, repo, "commit", "-q", "--allow-empty", "-m", "feat: demo")
	runGit(t, repo, "checkout", "-q", "main")

	// Add a commit to main so the two branches diverge.
	runGit(t, repo, "commit", "-q", "--allow-empty", "-m", "main: extra")

	tasksDir := filepath.Join(repo, "tasks")
	if err := os.MkdirAll(tasksDir, 0o755); err != nil {
		t.Fatal(err)
	}

	err := Merge(tasksDir, "demo")
	if err == nil {
		t.Fatal("expected error for non-FF merge, got nil")
	}
}

func TestMergeRemovesWorktree(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skipf("git not available: %v", err)
	}

	repo := t.TempDir()
	t.Chdir(repo)

	runGit(t, repo, "init", "-q", "-b", "main")
	runGit(t, repo, "config", "user.email", "test@example.com")
	runGit(t, repo, "config", "user.name", "Test")
	runGit(t, repo, "commit", "-q", "--allow-empty", "-m", "init")
	configureOrigin(t, repo)

	runGit(t, repo, "checkout", "-q", "-b", "wt/demo")
	runGit(t, repo, "commit", "-q", "--allow-empty", "-m", "feat: demo")
	runGit(t, repo, "checkout", "-q", "main")

	// Create a worktree directory so we can test that Merge removes it.
	worktree := filepath.Join(repo, ".worktrees", "demo")
	runGit(t, repo, "worktree", "add", worktree, "wt/demo")

	tasksDir := filepath.Join(repo, "tasks")
	if err := os.MkdirAll(tasksDir, 0o755); err != nil {
		t.Fatal(err)
	}

	if err := Merge(tasksDir, "demo"); err != nil {
		t.Fatalf("Merge: %v", err)
	}

	// Worktree should be gone.
	if _, err := os.Stat(worktree); !os.IsNotExist(err) {
		t.Errorf("worktree %q still exists after Merge", worktree)
	}
}

func TestMergeNoWorktreeStillSucceeds(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skipf("git not available: %v", err)
	}

	repo := t.TempDir()
	t.Chdir(repo)

	runGit(t, repo, "init", "-q", "-b", "main")
	runGit(t, repo, "config", "user.email", "test@example.com")
	runGit(t, repo, "config", "user.name", "Test")
	runGit(t, repo, "commit", "-q", "--allow-empty", "-m", "init")
	configureOrigin(t, repo)

	runGit(t, repo, "checkout", "-q", "-b", "wt/demo")
	runGit(t, repo, "commit", "-q", "--allow-empty", "-m", "feat: demo")
	runGit(t, repo, "checkout", "-q", "main")

	tasksDir := filepath.Join(repo, "tasks")
	if err := os.MkdirAll(tasksDir, 0o755); err != nil {
		t.Fatal(err)
	}

	// No worktree exists - Merge should still succeed (cleanup is best-effort).
	if err := Merge(tasksDir, "demo"); err != nil {
		t.Fatalf("Merge without worktree: %v", err)
	}
}

func TestMergePushesOnlyMainToOrigin(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skipf("git not available: %v", err)
	}

	repo := t.TempDir()
	t.Chdir(repo)
	t.Setenv("XDG_STATE_HOME", t.TempDir())

	runGit(t, repo, "init", "-q", "-b", "main")
	runGit(t, repo, "config", "user.email", "test@example.com")
	runGit(t, repo, "config", "user.name", "Test")

	tasksDir := filepath.Join(repo, "tasks")
	if err := os.MkdirAll(tasksDir, 0o755); err != nil {
		t.Fatal(err)
	}
	taskPath := filepath.Join(tasksDir, "demo.md")
	if err := os.WriteFile(taskPath, []byte("---\nstatus: active\nslug: demo\ntitle: Demo\n---\nbody\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runGit(t, repo, "add", ".")
	runGit(t, repo, "commit", "-q", "-m", "task active")
	remote := configureOrigin(t, repo)

	runGit(t, repo, "checkout", "-q", "-b", "wt/demo")
	if err := os.WriteFile(filepath.Join(repo, "feature.txt"), []byte("feature\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runGit(t, repo, "add", "feature.txt")
	runGit(t, repo, "commit", "-q", "-m", "feat: demo work")
	runGit(t, repo, "checkout", "-q", "main")
	runGit(t, repo, "branch", "feat/local-only")

	if err := Merge(tasksDir, "demo"); err != nil {
		t.Fatalf("Merge: %v", err)
	}

	local := gitOutput(t, repo, "rev-parse", "main")
	upstream := gitOutput(t, remote, "rev-parse", "refs/heads/main")
	if local != upstream {
		t.Fatalf("origin main = %s, want local main %s", upstream, local)
	}
	heads := gitOutput(t, remote, "for-each-ref", "--format=%(refname)", "refs/heads")
	if heads != "refs/heads/main" {
		t.Fatalf("origin heads = %q, want only refs/heads/main", heads)
	}
}

func TestMergePushFailureKeepsBranchAndWorktree(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skipf("git not available: %v", err)
	}

	repo := t.TempDir()
	t.Chdir(repo)
	t.Setenv("XDG_STATE_HOME", t.TempDir())

	runGit(t, repo, "init", "-q", "-b", "main")
	runGit(t, repo, "config", "user.email", "test@example.com")
	runGit(t, repo, "config", "user.name", "Test")

	tasksDir := filepath.Join(repo, "tasks")
	if err := os.MkdirAll(tasksDir, 0o755); err != nil {
		t.Fatal(err)
	}
	taskPath := filepath.Join(tasksDir, "demo.md")
	if err := os.WriteFile(taskPath, []byte("---\nstatus: active\nslug: demo\ntitle: Demo\n---\nbody\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runGit(t, repo, "add", ".")
	runGit(t, repo, "commit", "-q", "-m", "task active")

	worktree := filepath.Join(repo, ".worktrees", "demo")
	runGit(t, repo, "worktree", "add", "-q", "-b", "wt/demo", worktree, "main")
	if err := os.WriteFile(filepath.Join(worktree, "feature.txt"), []byte("feature\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runGit(t, worktree, "add", "feature.txt")
	runGit(t, worktree, "commit", "-q", "-m", "feat: demo work")

	err := Merge(tasksDir, "demo")
	if err == nil {
		t.Fatal("expected push failure, got nil")
	}
	if !strings.Contains(err.Error(), "git push origin main:main") {
		t.Errorf("error = %q should mention push command", err)
	}
	if !branchExists(repo, "wt/demo") {
		t.Error("wt/demo branch was deleted despite push failure")
	}
	if _, statErr := os.Stat(worktree); statErr != nil {
		t.Errorf("worktree missing despite push failure: %v", statErr)
	}
	if status := readStatus(t, taskPath); status != "done" {
		t.Errorf("status = %q want done after local close commit", status)
	}
}

func TestMergeJustCheckRedRefuses(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skipf("git not available: %v", err)
	}
	if _, err := exec.LookPath("just"); err != nil {
		t.Skipf("just not available: %v", err)
	}
	repo, taskPath := setupGateRepo(t, "demo", "@exit 1")

	preMain, err := exec.Command("git", "-C", repo, "rev-parse", "main").Output()
	if err != nil {
		t.Fatal(err)
	}

	mergeErr := Merge(filepath.Join(repo, "tasks"), "demo")
	if mergeErr == nil {
		t.Fatal("Merge should refuse on red just check")
	}
	var gateErr *MergeGateError
	if !errors.As(mergeErr, &gateErr) {
		t.Fatalf("error %v should be a *MergeGateError", mergeErr)
	}
	if gateErr.ExitCode() != 2 {
		t.Errorf("ExitCode = %d, want 2", gateErr.ExitCode())
	}
	if !strings.Contains(gateErr.Error(), "just check") {
		t.Errorf("error %q should name the failing recipe", gateErr.Error())
	}

	postMain, err := exec.Command("git", "-C", repo, "rev-parse", "main").Output()
	if err != nil {
		t.Fatal(err)
	}
	if string(preMain) != string(postMain) {
		t.Errorf("main moved despite red gate: pre=%q post=%q", preMain, postMain)
	}
	if !branchExists(repo, "wt/demo") {
		t.Error("wt/demo branch deleted despite red gate")
	}
	if _, statErr := os.Stat(filepath.Join(repo, ".worktrees", "demo")); statErr != nil {
		t.Errorf("worktree removed despite red gate: %v", statErr)
	}
	if status := readStatus(t, taskPath); status != "active" {
		t.Errorf("status = %q want active", status)
	}
}

func TestMergeJustCheckGreenLands(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skipf("git not available: %v", err)
	}
	if _, err := exec.LookPath("just"); err != nil {
		t.Skipf("just not available: %v", err)
	}
	repo, taskPath := setupGateRepo(t, "demo", "@echo ok")

	if err := Merge(filepath.Join(repo, "tasks"), "demo"); err != nil {
		t.Fatalf("Merge: %v", err)
	}
	if status := readStatus(t, taskPath); status != "done" {
		t.Errorf("status = %q want done", status)
	}
	if branchExists(repo, "wt/demo") {
		t.Error("wt/demo branch still exists after green merge")
	}
}

func TestMergeForceMergeRedOverridesAndLogs(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skipf("git not available: %v", err)
	}
	if _, err := exec.LookPath("just"); err != nil {
		t.Skipf("just not available: %v", err)
	}
	repo, taskPath := setupGateRepo(t, "demo", "@exit 1")
	ledger := filepath.Join(t.TempDir(), "merge-override.jsonl")
	t.Setenv("SPORE_MERGE_OVERRIDE_LOG", ledger)

	err := MergeWithOptions(filepath.Join(repo, "tasks"), "demo", MergeOptions{ForceMergeRed: "shipping during outage"})
	if err != nil {
		t.Fatalf("MergeWithOptions: %v", err)
	}
	if status := readStatus(t, taskPath); status != "done" {
		t.Errorf("status = %q want done", status)
	}
	row, err := os.ReadFile(ledger)
	if err != nil {
		t.Fatalf("read ledger: %v", err)
	}
	got := string(row)
	for _, want := range []string{`"slug":"demo"`, `"branch":"wt/demo"`, `"reason":"shipping during outage"`} {
		if !strings.Contains(got, want) {
			t.Errorf("ledger missing %q: got %q", want, got)
		}
	}
}

func TestMergeNoJustfileSkipsGate(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skipf("git not available: %v", err)
	}
	repo, taskPath := setupGateRepo(t, "demo", "")

	if err := Merge(filepath.Join(repo, "tasks"), "demo"); err != nil {
		t.Fatalf("Merge: %v", err)
	}
	if status := readStatus(t, taskPath); status != "done" {
		t.Errorf("status = %q want done", status)
	}
}

// setupGateRepo wires a repo + tasks/<slug>.md + worktree at
// .worktrees/<slug> + a feature.txt commit on wt/<slug>. When body
// is non-empty, a justfile with a `check` recipe whose body is the
// argument is committed onto wt/<slug>; an empty body skips the
// justfile so callers can exercise the no-justfile branch.
func setupGateRepo(t *testing.T, slug, justBody string) (repo, taskPath string) {
	t.Helper()
	repo = t.TempDir()
	t.Chdir(repo)
	t.Setenv("XDG_STATE_HOME", t.TempDir())

	runGit(t, repo, "init", "-q", "-b", "main")
	runGit(t, repo, "config", "user.email", "test@example.com")
	runGit(t, repo, "config", "user.name", "Test")

	tasksDir := filepath.Join(repo, "tasks")
	if err := os.MkdirAll(tasksDir, 0o755); err != nil {
		t.Fatal(err)
	}
	taskPath = filepath.Join(tasksDir, slug+".md")
	body := "---\nstatus: active\nslug: " + slug + "\ntitle: Demo\n---\nbody\n"
	if err := os.WriteFile(taskPath, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	runGit(t, repo, "add", ".")
	runGit(t, repo, "commit", "-q", "-m", "task active")
	configureOrigin(t, repo)

	worktree := filepath.Join(repo, ".worktrees", slug)
	runGit(t, repo, "worktree", "add", "-q", "-b", "wt/"+slug, worktree, "main")
	runGit(t, worktree, "config", "user.email", "test@example.com")
	runGit(t, worktree, "config", "user.name", "Test")
	if justBody != "" {
		just := "check:\n\t" + justBody + "\n"
		if err := os.WriteFile(filepath.Join(worktree, "justfile"), []byte(just), 0o644); err != nil {
			t.Fatal(err)
		}
		runGit(t, worktree, "add", "justfile")
		runGit(t, worktree, "commit", "-q", "-m", "add justfile")
	}
	if err := os.WriteFile(filepath.Join(worktree, "feature.txt"), []byte("feature\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runGit(t, worktree, "add", "feature.txt")
	runGit(t, worktree, "commit", "-q", "-m", "feat: demo work")
	return repo, taskPath
}
