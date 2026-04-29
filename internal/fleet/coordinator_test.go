package fleet

import (
	"os/exec"
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

func TestCoordinatorAgentPrecedence(t *testing.T) {
	cases := []struct {
		name      string
		coordEnv  string
		workerEnv string
		want      string
	}{
		{name: "coord_wins", coordEnv: "agent-A", workerEnv: "agent-B", want: "agent-A"},
		{name: "worker_fallback", coordEnv: "", workerEnv: "agent-B", want: "agent-B"},
		{name: "default", coordEnv: "", workerEnv: "", want: "claude-code"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("SPORE_COORDINATOR_AGENT", tc.coordEnv)
			t.Setenv("SPORE_AGENT_BINARY", tc.workerEnv)
			if got := coordinatorAgent(); got != tc.want {
				t.Errorf("coordinatorAgent() = %q, want %q", got, tc.want)
			}
		})
	}
}

// sessionCreated returns the tmux #{session_created} for name. Used by
// the idempotency check to detect a respawn.
func sessionCreated(name string) (string, error) {
	out, err := exec.Command(
		"tmux", "display-message", "-p", "-t", name, "#{session_created}",
	).Output()
	if err != nil {
		return "", err
	}
	return string(out), nil
}
