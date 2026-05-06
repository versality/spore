package fleet

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestReconcileSpawnsCoordinatorSingleton(t *testing.T) {
	requireToolchain(t)

	dirs := newTestDirs(t)
	gitInit(t, dirs.project)
	mustEnable(t)
	t.Setenv("SPORE_AGENT_BINARY", "sleep 30")

	t.Cleanup(func() { killSporeSessions(dirs.project) })

	session := CoordinatorSessionName(dirs.project)

	if _, err := Reconcile(Config{
		TasksDir: dirs.tasks, ProjectRoot: dirs.project, MaxWorkers: 3,
	}); err != nil {
		t.Fatalf("Reconcile pass 1: %v", err)
	}
	if !hasSession(session) {
		t.Fatalf("expected coordinator session %q after first reconcile", session)
	}

	// Idempotency: a second reconcile must not double-spawn. Capture
	// the session creation timestamp; tmux returns it with #{session_created}.
	ts1, err := sessionCreated(session)
	if err != nil {
		t.Fatalf("sessionCreated #1: %v", err)
	}

	if _, err := Reconcile(Config{
		TasksDir: dirs.tasks, ProjectRoot: dirs.project, MaxWorkers: 3,
	}); err != nil {
		t.Fatalf("Reconcile pass 2: %v", err)
	}
	if !hasSession(session) {
		t.Fatalf("expected coordinator session %q after second reconcile", session)
	}
	ts2, err := sessionCreated(session)
	if err != nil {
		t.Fatalf("sessionCreated #2: %v", err)
	}
	if ts1 != ts2 {
		t.Errorf("coordinator session was respawned (created %q -> %q); expected idempotent", ts1, ts2)
	}
}

func TestReconcileReapsCoordinatorOnDisable(t *testing.T) {
	requireToolchain(t)

	dirs := newTestDirs(t)
	gitInit(t, dirs.project)
	mustEnable(t)
	t.Setenv("SPORE_AGENT_BINARY", "sleep 30")

	t.Cleanup(func() { killSporeSessions(dirs.project) })

	session := CoordinatorSessionName(dirs.project)

	if _, err := Reconcile(Config{
		TasksDir: dirs.tasks, ProjectRoot: dirs.project, MaxWorkers: 3,
	}); err != nil {
		t.Fatalf("Reconcile (enable): %v", err)
	}
	if !hasSession(session) {
		t.Fatalf("expected coordinator session %q before disable", session)
	}

	if err := Disable(); err != nil {
		t.Fatalf("Disable: %v", err)
	}

	r, err := Reconcile(Config{
		TasksDir: dirs.tasks, ProjectRoot: dirs.project, MaxWorkers: 3,
	})
	if err != nil {
		t.Fatalf("Reconcile (disabled): %v", err)
	}
	if !r.Disabled {
		t.Errorf("expected Disabled=true after Disable(), got %+v", r)
	}
	if hasSession(session) {
		t.Errorf("expected coordinator session %q reaped on flag-disable, still alive", session)
	}
}

func TestReconcileCoordinatorDoesNotCountTowardCap(t *testing.T) {
	requireToolchain(t)

	dirs := newTestDirs(t)
	gitInit(t, dirs.project)
	mustEnable(t)
	t.Setenv("SPORE_AGENT_BINARY", "sleep 30")

	for _, slug := range []string{"a", "b"} {
		writeTask(t, dirs.tasks, slug, "active")
	}
	t.Cleanup(func() { killSporeSessions(dirs.project) })

	r, err := Reconcile(Config{
		TasksDir: dirs.tasks, ProjectRoot: dirs.project, MaxWorkers: 2,
	})
	if err != nil {
		t.Fatalf("Reconcile: %v", err)
	}
	if got, want := r.Spawned, []string{"a", "b"}; !equalSlices(got, want) {
		t.Errorf("Spawned = %v, want %v (coordinator must not consume a worker slot)", got, want)
	}
	if len(r.Skipped) != 0 {
		t.Errorf("Skipped = %v, want []", r.Skipped)
	}

	if !hasSession(CoordinatorSessionName(dirs.project)) {
		t.Errorf("expected coordinator session alive after reconcile")
	}
}

func TestCoordinatorSessionNameUsesMainRepoFromWorktree(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not on PATH")
	}
	parent := t.TempDir()
	main := filepath.Join(parent, "marketercom")
	if err := os.MkdirAll(main, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{
		{"init", "-q", "-b", "main"},
		{"config", "user.email", "test@example.com"},
		{"config", "user.name", "Test"},
		{"commit", "-q", "--allow-empty", "-m", "init"},
	} {
		out, err := exec.Command("git", append([]string{"-C", main}, args...)...).CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %v: %s", args, err, out)
		}
	}
	worktree := filepath.Join(main, ".worktrees", "wt-rover-slug")
	if err := os.MkdirAll(filepath.Dir(worktree), 0o755); err != nil {
		t.Fatal(err)
	}
	out, err := exec.Command("git", "-C", main, "worktree", "add", "-q", worktree, "-b", "wt/rover").CombinedOutput()
	if err != nil {
		t.Fatalf("git worktree add: %v: %s", err, out)
	}

	got := CoordinatorSessionName(worktree)
	want := "spore/marketercom/coordinator"
	if got != want {
		t.Errorf("CoordinatorSessionName(worktree) = %q, want %q", got, want)
	}
}

func TestCoordinatorAgentPrecedence(t *testing.T) {
	cases := []struct {
		name      string
		coordEnv  string
		workerEnv string
		driver    string
		want      string
	}{
		{name: "coord_wins", coordEnv: "agent-A", workerEnv: "agent-B", driver: "codex", want: "agent-A"},
		{name: "worker_fallback", coordEnv: "", workerEnv: "agent-B", driver: "codex", want: "agent-B"},
		{name: "driver_claude", coordEnv: "", workerEnv: "", driver: "claude", want: "claude"},
		{name: "driver_codex", coordEnv: "", workerEnv: "", driver: "codex", want: "codex"},
		{name: "driver_passthrough", coordEnv: "", workerEnv: "", driver: "/usr/local/bin/spore-coordinator-launch", want: "/usr/local/bin/spore-coordinator-launch"},
		{name: "default", coordEnv: "", workerEnv: "", driver: "", want: "claude"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("SPORE_COORDINATOR_AGENT", tc.coordEnv)
			t.Setenv("SPORE_AGENT_BINARY", tc.workerEnv)
			cfg := CoordinatorConfig{Driver: tc.driver}
			if got := coordinatorAgent(cfg); got != tc.want {
				t.Errorf("coordinatorAgent(%+v) = %q, want %q", cfg, got, tc.want)
			}
		})
	}
}

func TestEnsureCoordinatorDefersToExternalSession(t *testing.T) {
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skipf("tmux not available: %v", err)
	}

	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "spore.toml"),
		[]byte("[coordinator]\nexternal_session_pattern = \"^helm-mcom( \\[.*\\])?$\"\n"),
		0o600); err != nil {
		t.Fatal(err)
	}

	external := "helm-mcom [opus]"
	if err := exec.Command("tmux", "-L", testTmuxSocket, "new-session", "-d", "-s", external, "sleep 86400").Run(); err != nil {
		t.Fatalf("spawn external session: %v", err)
	}
	t.Cleanup(func() {
		_ = exec.Command("tmux", "-L", testTmuxSocket, "kill-session", "-t", external).Run()
	})

	got, spawned, err := EnsureCoordinator(dir)
	if err != nil {
		t.Fatalf("EnsureCoordinator: %v", err)
	}
	if spawned {
		t.Errorf("expected spawned=false (external coordinator owns the role), got true")
	}
	if got != external {
		t.Errorf("EnsureCoordinator returned %q, want external %q", got, external)
	}
	if hasSession(CoordinatorSessionName(dir)) {
		t.Errorf("kernel coordinator session %q was spawned despite external match", CoordinatorSessionName(dir))
	}
	if !CoordinatorAlive(dir) {
		t.Errorf("CoordinatorAlive returned false despite matching external session")
	}
}

func TestEnsureCoordinatorPatternNoMatchSpawnsKernel(t *testing.T) {
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skipf("tmux not available: %v", err)
	}

	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "spore.toml"),
		[]byte("[coordinator]\nexternal_session_pattern = \"^helm-mcom\"\n"),
		0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("SPORE_COORDINATOR_AGENT", "sleep 30")
	t.Cleanup(func() {
		_ = exec.Command("tmux", "-L", testTmuxSocket, "kill-session", "-t", CoordinatorSessionName(dir)).Run()
	})

	got, spawned, err := EnsureCoordinator(dir)
	if err != nil {
		t.Fatalf("EnsureCoordinator: %v", err)
	}
	if !spawned {
		t.Errorf("expected spawned=true when pattern set but no session matches, got false")
	}
	if got != CoordinatorSessionName(dir) {
		t.Errorf("EnsureCoordinator returned %q, want kernel session %q", got, CoordinatorSessionName(dir))
	}
}

// sessionCreated returns the tmux #{session_created} for name. Used by
// the idempotency check to detect a respawn.
func sessionCreated(name string) (string, error) {
	out, err := exec.Command(
		"tmux", "-L", testTmuxSocket, "display-message", "-p", "-t", name, "#{session_created}",
	).Output()
	if err != nil {
		return "", err
	}
	return string(out), nil
}
