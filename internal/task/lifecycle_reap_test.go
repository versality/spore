package task

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestDoneKillsAllMatchingSlugSessions(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skipf("git not available: %v", err)
	}
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skipf("tmux not available: %v", err)
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
	slug := "demo"
	project := filepath.Base(repo)
	// Recorded session: matches the wt-style "<icon> <project>/<slug> [tag]"
	// shape the operator's idle haiku rower hit. Two sister sessions
	// drift the tier tag (haiku vs opus) and the spore-style prefix;
	// Done must reap all three even though only one is in frontmatter.
	recorded := "X " + project + "/" + slug + " [haiku]"
	drifted := "X " + project + "/" + slug + " [opus]"
	sporeStyle := "spore/" + project + "/" + slug
	body := "---\nstatus: active\nslug: demo\ntitle: Demo\nsession: " + recorded + "\n---\nbody\n"
	taskPath := filepath.Join(tasksDir, slug+".md")
	if err := os.WriteFile(taskPath, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}

	for _, name := range []string{recorded, drifted, sporeStyle} {
		if out, err := exec.Command("tmux", "new-session", "-d", "-s", name, "sleep 30").CombinedOutput(); err != nil {
			t.Fatalf("tmux new-session %q: %v: %s", name, err, out)
		}
		t.Cleanup(func() { _ = exec.Command("tmux", "kill-session", "-t", name).Run() })
	}

	if err := Done(tasksDir, slug, true); err != nil {
		t.Fatalf("Done: %v", err)
	}

	for _, name := range []string{recorded, drifted, sporeStyle} {
		if err := exec.Command("tmux", "has-session", "-t", name).Run(); err == nil {
			t.Errorf("session %q still alive after Done; broad-match reap missed it", name)
		}
	}
}

func TestDoneLeavesUnrelatedSessionsAlone(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skipf("git not available: %v", err)
	}
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skipf("tmux not available: %v", err)
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
	slug := "foo"
	project := filepath.Base(repo)
	body := "---\nstatus: active\nslug: foo\ntitle: Foo\n---\nbody\n"
	if err := os.WriteFile(filepath.Join(tasksDir, slug+".md"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}

	// Sister slug "foo-bar": substring match would catch it; the
	// space-or-end slug-end boundary protects against that.
	sister := "X " + project + "/foo-bar [opus]"
	otherProject := "X other-project/foo"
	for _, name := range []string{sister, otherProject} {
		if out, err := exec.Command("tmux", "new-session", "-d", "-s", name, "sleep 30").CombinedOutput(); err != nil {
			t.Fatalf("tmux new-session %q: %v: %s", name, err, out)
		}
		t.Cleanup(func() { _ = exec.Command("tmux", "kill-session", "-t", name).Run() })
	}

	if err := Done(tasksDir, slug, true); err != nil {
		t.Fatalf("Done: %v", err)
	}

	for _, name := range []string{sister, otherProject} {
		if err := exec.Command("tmux", "has-session", "-t", name).Run(); err != nil {
			t.Errorf("unrelated session %q killed by reap: %v", name, err)
		}
	}
}

func TestPauseLeavesActivelyUsedSessionAlive(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skipf("git not available: %v", err)
	}
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skipf("tmux not available: %v", err)
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
	slug := "demo"
	project := filepath.Base(repo)
	session := "X " + project + "/" + slug + " [opus]"
	body := "---\nstatus: active\nslug: demo\ntitle: Demo\nsession: " + session + "\n---\nbody\n"
	if err := os.WriteFile(filepath.Join(tasksDir, slug+".md"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}

	if out, err := exec.Command("tmux", "new-session", "-d", "-s", session, "sleep 30").CombinedOutput(); err != nil {
		t.Fatalf("tmux new-session: %v: %s", err, out)
	}
	t.Cleanup(func() { _ = exec.Command("tmux", "kill-session", "-t", session).Run() })

	// Default 5min idle threshold; the session was just created so
	// activity is fresh. Pause must NOT reap it.
	if err := Pause(tasksDir, slug); err != nil {
		t.Fatalf("Pause: %v", err)
	}
	if err := exec.Command("tmux", "has-session", "-t", session).Run(); err != nil {
		t.Errorf("Pause reaped a fresh session: %v", err)
	}
}

func TestPauseReapsIdleSession(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skipf("git not available: %v", err)
	}
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skipf("tmux not available: %v", err)
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
	slug := "demo"
	project := filepath.Base(repo)
	session := "X " + project + "/" + slug + " [opus]"
	body := "---\nstatus: active\nslug: demo\ntitle: Demo\nsession: " + session + "\n---\nbody\n"
	if err := os.WriteFile(filepath.Join(tasksDir, slug+".md"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}

	if out, err := exec.Command("tmux", "new-session", "-d", "-s", session, "sleep 30").CombinedOutput(); err != nil {
		t.Fatalf("tmux new-session: %v: %s", err, out)
	}
	t.Cleanup(func() { _ = exec.Command("tmux", "kill-session", "-t", session).Run() })

	// Threshold 0 forces every matching session to count as "stale
	// enough"; mirrors the >5min idle case without sleeping the test.
	t.Setenv("SPORE_IDLE_REAP_SECS", "0")

	if err := Pause(tasksDir, slug); err != nil {
		t.Fatalf("Pause: %v", err)
	}
	if err := exec.Command("tmux", "has-session", "-t", session).Run(); err == nil {
		t.Errorf("Pause did not reap idle session under threshold=0")
	}
}
