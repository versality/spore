package fleet

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// TestReapSmokeDoneTaskRealGit drives runReap against a real
// short-lived git repo with two worktrees. One task is done, the other
// stays active. Reap must teardown only the done one's worktree +
// branch and leave the active one untouched. Mirrors wt-task's
// integration coverage for cmd_fleet_reap.
func TestReapSmokeDoneTaskRealGit(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skipf("git not available: %v", err)
	}

	tmp := t.TempDir()
	mainRoot := filepath.Join(tmp, "p", "project")
	if err := os.MkdirAll(mainRoot, 0o755); err != nil {
		t.Fatal(err)
	}
	gitSh(t, mainRoot, "init", "-q", "-b", "main")
	gitSh(t, mainRoot, "config", "user.email", "test@example.com")
	gitSh(t, mainRoot, "config", "user.name", "Test")
	gitSh(t, mainRoot, "commit", "-q", "--allow-empty", "-m", "init")

	if err := os.MkdirAll(filepath.Join(mainRoot, "tasks"), 0o755); err != nil {
		t.Fatal(err)
	}
	writeReapTaskFile(t, mainRoot, "done-slug", "done")
	writeReapTaskFile(t, mainRoot, "active-slug", "active")

	// Spawn two worktrees rooted under .worktrees/<slug>.
	doneWt := filepath.Join(mainRoot, ".worktrees", "done-slug")
	activeWt := filepath.Join(mainRoot, ".worktrees", "active-slug")
	gitSh(t, mainRoot, "worktree", "add", "-b", "wt/done-slug", doneWt)
	gitSh(t, mainRoot, "worktree", "add", "-b", "wt/active-slug", activeWt)

	projectsFile := filepath.Join(tmp, "projects")
	if err := os.WriteFile(projectsFile, []byte(mainRoot+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	tmux := &fakeReapTmux{sessions: map[string]bool{}, listOut: ""}
	e := reapEnv{
		projectsFile:  projectsFile,
		currentRoot:   func() (string, error) { return mainRoot, nil },
		gitRunner:     defaultReapGit,
		tmuxRunner:    tmux,
		listWorktrees: func(root string) ([]string, error) { return listWorktreesAt(defaultReapGit, root) },
	}

	var stdout, stderr bytes.Buffer
	rc, err := runReap(false, &stdout, &stderr, e)
	if err != nil {
		t.Fatalf("runReap: %v", err)
	}
	if rc != 0 {
		t.Fatalf("rc=%d stderr=%s", rc, stderr.String())
	}

	if _, err := os.Stat(doneWt); !os.IsNotExist(err) {
		t.Errorf("expected done worktree removed, stat err=%v", err)
	}
	if _, err := os.Stat(activeWt); err != nil {
		t.Errorf("expected active worktree preserved: %v", err)
	}

	branches := gitOut(t, mainRoot, "branch", "--list", "wt/*")
	if got := string(branches); !contains(got, "wt/active-slug") {
		t.Errorf("active branch must survive, got %q", got)
	}
	if got := string(branches); contains(got, "wt/done-slug") {
		t.Errorf("done branch must be deleted, got %q", got)
	}
}

func gitSh(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-c", "safe.directory=" + dir, "-C", dir}, args...)...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, string(out))
	}
}

func gitOut(t *testing.T, dir string, args ...string) []byte {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-c", "safe.directory=" + dir, "-C", dir}, args...)...)
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("git %v: %v", args, err)
	}
	return out
}

func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
