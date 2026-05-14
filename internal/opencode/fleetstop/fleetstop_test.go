package fleetstop

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func writeTask(t *testing.T, dir, slug, status, agent, host string) {
	t.Helper()
	body := "---\nstatus: " + status + "\nslug: " + slug +
		"\ntitle: t\ncreated: 2026-01-01T00:00:00Z\nproject: spore\n" +
		"agent: " + agent + "\n"
	if host != "" {
		body += "host: " + host + "\n"
	}
	body += "---\nbody\n"
	if err := os.WriteFile(filepath.Join(dir, slug+".md"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestListActiveSlugs_FilterByAgentStatusHost(t *testing.T) {
	tasks := t.TempDir()
	writeTask(t, tasks, "alpha", "active", "opencode", "")          // ok (empty host)
	writeTask(t, tasks, "bravo", "active", "opencode", "host-a")    // ok matches
	writeTask(t, tasks, "charlie", "active", "opencode", "skybase") // skip wrong host
	writeTask(t, tasks, "delta", "paused", "opencode", "")          // skip status
	writeTask(t, tasks, "echo", "active", "claude", "")             // skip agent
	if err := os.WriteFile(filepath.Join(tasks, "README.md"),
		[]byte("not frontmatter\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	got, err := ListActiveSlugs(tasks, "host-a")
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"alpha", "bravo"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}
}

func TestListActiveSlugs_MissingTasksDir(t *testing.T) {
	got, err := ListActiveSlugs("/nonexistent/tasks/path/xyz", "host")
	if err != nil {
		t.Fatalf("expected nil error, got %v", err)
	}
	if got != nil {
		t.Fatalf("got %v, want nil", got)
	}
}

func TestRun_PausesEverySlug_KillsOrphans_WritesSummary(t *testing.T) {
	prev := PostKillDelay
	PostKillDelay = 0
	defer func() { PostKillDelay = prev }()

	root := t.TempDir()
	tasks := filepath.Join(root, "tasks")
	if err := os.MkdirAll(tasks, 0o755); err != nil {
		t.Fatal(err)
	}
	writeTask(t, tasks, "alpha", "active", "opencode", "host-a")
	writeTask(t, tasks, "bravo", "active", "opencode", "host-a")

	var paused []string
	var killed []int
	cfg := Config{
		MainRoot: root,
		Host:     "host-a",
		User:     "tester",
		Pause: func(slug string) error {
			paused = append(paused, slug)
			return nil
		},
		SessionName: func(string, string) (string, error) {
			t.Fatal("session-name should not be called when pause succeeds")
			return "", nil
		},
		KillSession: func(string) error { return nil },
		FindOrphans: func(user string) ([]int, error) {
			if user != "tester" {
				t.Errorf("user = %q, want tester", user)
			}
			return []int{4242, 4243}, nil
		},
		Kill: func(pid int) error {
			killed = append(killed, pid)
			return nil
		},
	}
	res, err := Run(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(res.Paused, []string{"alpha", "bravo"}) {
		t.Errorf("paused = %v", res.Paused)
	}
	if !reflect.DeepEqual(killed, []int{4242, 4243}) {
		t.Errorf("killed = %v", killed)
	}
	if res.Killed != 2 || res.Orphans != 2 {
		t.Errorf("Killed=%d Orphans=%d", res.Killed, res.Orphans)
	}
	want := "opencode-fleet-stop: paused 2 rowers (slugs: alpha,bravo) killed 2 orphan procs"
	if got := res.Summary(); got != want {
		t.Errorf("summary = %q, want %q", got, want)
	}
}

func TestRun_PauseFailureFallsBackToSessionKill(t *testing.T) {
	prev := PostKillDelay
	PostKillDelay = 0
	defer func() { PostKillDelay = prev }()

	root := t.TempDir()
	tasks := filepath.Join(root, "tasks")
	if err := os.MkdirAll(tasks, 0o755); err != nil {
		t.Fatal(err)
	}
	writeTask(t, tasks, "alpha", "active", "opencode", "host-a")

	var killedSession string
	cfg := Config{
		MainRoot:    root,
		Host:        "host-a",
		User:        "tester",
		Pause:       func(string) error { return errors.New("inbox unread") },
		SessionName: func(_, slug string) (string, error) { return "spore/" + slug, nil },
		KillSession: func(s string) error { killedSession = s; return nil },
		FindOrphans: func(string) ([]int, error) { return nil, nil },
		Kill:        func(int) error { return nil },
	}
	res, err := Run(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Paused) != 0 {
		t.Errorf("Paused should be empty on pause failure, got %v", res.Paused)
	}
	if killedSession != "spore/alpha" {
		t.Errorf("killedSession = %q", killedSession)
	}
	want := "opencode-fleet-stop: paused 0 rowers (none) killed 0 orphan procs"
	if got := res.Summary(); got != want {
		t.Errorf("summary = %q, want %q", got, want)
	}
}

func TestRun_NoActiveSlugs_StillSweepsOrphans(t *testing.T) {
	prev := PostKillDelay
	PostKillDelay = 0
	defer func() { PostKillDelay = prev }()

	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "tasks"), 0o755); err != nil {
		t.Fatal(err)
	}

	cfg := Config{
		MainRoot: root,
		Host:     "host-a",
		User:     "tester",
		Pause:    func(string) error { return nil },
		SessionName: func(string, string) (string, error) {
			t.Fatal("unexpected SessionName call")
			return "", nil
		},
		KillSession: func(string) error { return nil },
		FindOrphans: func(string) ([]int, error) { return []int{1234}, nil },
		Kill:        func(int) error { return nil },
	}
	res, err := Run(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if res.Killed != 1 {
		t.Errorf("Killed = %d, want 1", res.Killed)
	}
}
