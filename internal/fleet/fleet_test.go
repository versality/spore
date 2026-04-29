package fleet

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/versality/spore/internal/task/frontmatter"
)

func TestReconcileShortCircuitsWhenDisabled(t *testing.T) {
	dirs := newTestDirs(t)
	writeTask(t, dirs.tasks, "alpha", "active")

	r, err := Reconcile(Config{
		TasksDir:    dirs.tasks,
		ProjectRoot: dirs.project,
		MaxWorkers:  3,
	})
	if err != nil {
		t.Fatalf("Reconcile: %v", err)
	}
	if !r.Disabled {
		t.Errorf("expected Disabled=true with no flag set")
	}
	if len(r.Spawned)+len(r.Reaped)+len(r.Kept)+len(r.Skipped) != 0 {
		t.Errorf("expected empty actions when disabled, got %+v", r)
	}
}

func TestReconcileSpawnsAndReaps(t *testing.T) {
	requireToolchain(t)

	dirs := newTestDirs(t)
	gitInit(t, dirs.project)
	mustEnable(t)
	t.Setenv("SPORE_AGENT_BINARY", "sleep 30")

	writeTask(t, dirs.tasks, "alpha", "active")
	writeTask(t, dirs.tasks, "beta", "active")
	writeTask(t, dirs.tasks, "gamma", "draft")

	t.Cleanup(func() { killSporeSessions(dirs.project) })

	r, err := Reconcile(Config{
		TasksDir:    dirs.tasks,
		ProjectRoot: dirs.project,
		MaxWorkers:  3,
	})
	if err != nil {
		t.Fatalf("Reconcile: %v", err)
	}
	if got, want := r.Spawned, []string{"alpha", "beta"}; !equalSlices(got, want) {
		t.Errorf("Spawned = %v, want %v", got, want)
	}
	if len(r.Reaped) != 0 || len(r.Kept) != 0 || len(r.Skipped) != 0 {
		t.Errorf("first pass should not reap/keep/skip, got %+v", r)
	}

	r2, err := Reconcile(Config{
		TasksDir:    dirs.tasks,
		ProjectRoot: dirs.project,
		MaxWorkers:  3,
	})
	if err != nil {
		t.Fatalf("Reconcile pass 2: %v", err)
	}
	if got, want := r2.Kept, []string{"alpha", "beta"}; !equalSlices(got, want) {
		t.Errorf("Kept (pass 2) = %v, want %v", got, want)
	}
	if len(r2.Spawned) != 0 || len(r2.Reaped) != 0 {
		t.Errorf("pass 2 should be a no-op, got %+v", r2)
	}

	flipStatus(t, dirs.tasks, "alpha", "done")

	r3, err := Reconcile(Config{
		TasksDir:    dirs.tasks,
		ProjectRoot: dirs.project,
		MaxWorkers:  3,
	})
	if err != nil {
		t.Fatalf("Reconcile pass 3: %v", err)
	}
	if got, want := r3.Reaped, []string{"alpha"}; !equalSlices(got, want) {
		t.Errorf("Reaped (pass 3) = %v, want %v", got, want)
	}
	if got, want := r3.Kept, []string{"beta"}; !equalSlices(got, want) {
		t.Errorf("Kept (pass 3) = %v, want %v", got, want)
	}

	// Pause beta: reconcile must keep the session alive (pause is
	// the operator-attached state, not a teardown signal).
	flipStatus(t, dirs.tasks, "beta", "paused")

	r4, err := Reconcile(Config{
		TasksDir:    dirs.tasks,
		ProjectRoot: dirs.project,
		MaxWorkers:  3,
	})
	if err != nil {
		t.Fatalf("Reconcile pass 4: %v", err)
	}
	if len(r4.Reaped) != 0 {
		t.Errorf("Reaped (pass 4 / paused) = %v, want []", r4.Reaped)
	}
	if got, want := r4.Kept, []string{"beta"}; !equalSlices(got, want) {
		t.Errorf("Kept (pass 4 / paused) = %v, want %v", got, want)
	}
}

func TestReconcileRespectsMaxWorkers(t *testing.T) {
	requireToolchain(t)

	dirs := newTestDirs(t)
	gitInit(t, dirs.project)
	mustEnable(t)
	t.Setenv("SPORE_AGENT_BINARY", "sleep 30")

	for _, slug := range []string{"a", "b", "c", "d", "e"} {
		writeTask(t, dirs.tasks, slug, "active")
	}
	t.Cleanup(func() { killSporeSessions(dirs.project) })

	r, err := Reconcile(Config{
		TasksDir:    dirs.tasks,
		ProjectRoot: dirs.project,
		MaxWorkers:  2,
	})
	if err != nil {
		t.Fatalf("Reconcile: %v", err)
	}
	if got, want := r.Spawned, []string{"a", "b"}; !equalSlices(got, want) {
		t.Errorf("Spawned = %v, want %v", got, want)
	}
	if got, want := r.Skipped, []string{"c", "d", "e"}; !equalSlices(got, want) {
		t.Errorf("Skipped = %v, want %v", got, want)
	}
}

func TestEnableDisableFlag(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	on, err := Enabled()
	if err != nil {
		t.Fatalf("Enabled: %v", err)
	}
	if on {
		t.Error("expected disabled with fresh state dir")
	}
	if err := Enable(); err != nil {
		t.Fatalf("Enable: %v", err)
	}
	on, err = Enabled()
	if err != nil || !on {
		t.Errorf("after Enable: enabled=%v err=%v, want true nil", on, err)
	}
	// Idempotent.
	if err := Enable(); err != nil {
		t.Errorf("Enable second call: %v", err)
	}
	if err := Disable(); err != nil {
		t.Fatalf("Disable: %v", err)
	}
	on, err = Enabled()
	if err != nil || on {
		t.Errorf("after Disable: enabled=%v err=%v, want false nil", on, err)
	}
	if err := Disable(); err != nil {
		t.Errorf("Disable on missing flag: %v", err)
	}
}

func TestLoadMaxWorkersTOML(t *testing.T) {
	root := t.TempDir()
	if got, err := LoadMaxWorkers(root); err != nil || got != DefaultMaxWorkers {
		t.Fatalf("missing toml: got %d err %v, want %d nil", got, err, DefaultMaxWorkers)
	}
	if err := os.WriteFile(filepath.Join(root, "spore.toml"), []byte("[fleet]\nmax_workers = 7\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	got, err := LoadMaxWorkers(root)
	if err != nil {
		t.Fatalf("LoadMaxWorkers: %v", err)
	}
	if got != 7 {
		t.Errorf("max_workers = %d, want 7", got)
	}
}

type testDirs struct {
	project string
	tasks   string
}

func newTestDirs(t *testing.T) testDirs {
	t.Helper()
	// Use the test name as the project basename so session names
	// (`spore/<basename>/<slug>`) don't collide with sibling tests:
	// t.TempDir() produces unique full paths but reuses basename
	// `001` across the binary's tests.
	name := strings.ReplaceAll(t.Name(), "/", "_")
	project := filepath.Join(t.TempDir(), name)
	tasks := filepath.Join(project, "tasks")
	if err := os.MkdirAll(tasks, 0o755); err != nil {
		t.Fatal(err)
	}
	return testDirs{project: project, tasks: tasks}
}

func gitInit(t *testing.T, repo string) {
	t.Helper()
	for _, args := range [][]string{
		{"init", "-q", "-b", "main"},
		{"config", "user.email", "test@example.com"},
		{"config", "user.name", "Test"},
		{"commit", "-q", "--allow-empty", "-m", "init"},
	} {
		out, err := exec.Command("git", append([]string{"-C", repo}, args...)...).CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %v: %s", args, err, out)
		}
	}
}

func mustEnable(t *testing.T) {
	t.Helper()
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	if err := Enable(); err != nil {
		t.Fatalf("Enable: %v", err)
	}
}

func writeTask(t *testing.T, tasksDir, slug, status string) {
	t.Helper()
	m := frontmatter.Meta{Status: status, Slug: slug, Title: slug}
	if err := os.WriteFile(filepath.Join(tasksDir, slug+".md"), frontmatter.Write(m, nil), 0o644); err != nil {
		t.Fatal(err)
	}
}

func flipStatus(t *testing.T, tasksDir, slug, status string) {
	t.Helper()
	path := filepath.Join(tasksDir, slug+".md")
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	m, body, err := frontmatter.Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	m.Status = status
	if err := os.WriteFile(path, frontmatter.Write(m, body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func requireToolchain(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skipf("git not available: %v", err)
	}
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skipf("tmux not available: %v", err)
	}
}

func killSporeSessions(projectRoot string) {
	out, err := exec.Command("tmux", "list-sessions", "-F", "#{session_name}").Output()
	if err != nil {
		return
	}
	prefix := "spore/" + filepath.Base(projectRoot) + "/"
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		if strings.HasPrefix(line, prefix) {
			_ = exec.Command("tmux", "kill-session", "-t", line).Run()
		}
	}
}

func equalSlices(a, b []string) bool {
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
