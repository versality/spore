package pushpending

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/versality/spore/internal/agentpane"
	"github.com/versality/spore/internal/hooks"
)

type repoFixture struct {
	projectRoot string
	worktree    string
	slug        string
}

// newRepoFixture builds a temp repo with a "main" branch, a bare
// "origin" remote that holds the post-seed main, then advances local
// main by `unpushedCommits` commits (mimicking a freshly-merged but
// not-yet-pushed state). A wt/<slug> branch + worktree exist for the
// hook's pane-side checks.
func newRepoFixture(t *testing.T, slug string, unpushedCommits int) repoFixture {
	t.Helper()
	root := t.TempDir()
	run(t, root, "git", "init", "-q", "-b", "main")
	run(t, root, "git", "config", "user.email", "test@example.com")
	run(t, root, "git", "config", "user.name", "test")
	if err := os.WriteFile(filepath.Join(root, "README"), []byte("seed\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	run(t, root, "git", "add", "README")
	run(t, root, "git", "commit", "-q", "-m", "seed")

	// Bare origin remote with the seeded main.
	originDir := filepath.Join(t.TempDir(), "origin.git")
	run(t, root, "git", "init", "-q", "--bare", originDir)
	run(t, root, "git", "remote", "add", "origin", originDir)
	run(t, root, "git", "push", "-q", "origin", "main")
	run(t, root, "git", "fetch", "-q", "origin")

	// Advance local main without pushing.
	for i := 0; i < unpushedCommits; i++ {
		name := fmt.Sprintf("main-extra-%d", i)
		if err := os.WriteFile(filepath.Join(root, name), []byte("x\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		run(t, root, "git", "add", name)
		run(t, root, "git", "commit", "-q", "-m", "advance "+name)
	}

	// wt/<slug> branch + worktree (empty, parity with the worker setup).
	branch := "wt/" + slug
	run(t, root, "git", "branch", branch)
	worktree := filepath.Join(root, ".worktrees", slug)
	run(t, root, "git", "worktree", "add", "-q", worktree, branch)

	return repoFixture{projectRoot: root, worktree: worktree, slug: slug}
}

func run(t *testing.T, dir string, name string, args ...string) {
	t.Helper()
	cmd := exec.Command(name, args...)
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("%s %v in %s: %v: %s", name, args, dir, err, strings.TrimSpace(string(out)))
	}
}

func claudeIdleCapture(string) (string, error) {
	separator := strings.Repeat("─", 60)
	mode := "  -- INSERT -- ⏵⏵ bypass permissions on (shift+tab to cycle)         8000 tokens"
	return strings.Join([]string{
		"● Read(/x)",
		"  ⎿  Read 1 line",
		"",
		separator,
		"❯ ",
		separator,
		mode,
	}, "\n"), nil
}

func claudeRunningCapture(string) (string, error) {
	separator := strings.Repeat("─", 60)
	mode := "  -- INSERT -- ⏵⏵ bypass permissions on (shift+tab to cycle)         8000 tokens"
	return strings.Join([]string{
		"✶ Cogitating… (53s · ↓ 2.2k tokens · thought for 4s)",
		"",
		separator,
		"❯ ",
		separator,
		mode,
	}, "\n"), nil
}

func TestRun(t *testing.T) {
	cases := []struct {
		name      string
		unpushed  int
		dirty     bool
		midMerge  string
		capture   agentpane.CaptureFunc
		noSlug    bool
		wantExit  int
		wantInMsg string
	}{
		{
			name:      "idle + clean + unpushed fires exit 2",
			unpushed:  1,
			capture:   claudeIdleCapture,
			wantExit:  2,
			wantInMsg: "1 commit(s)",
		},
		{
			name:      "idle + clean + 3 unpushed shows N in message",
			unpushed:  3,
			capture:   claudeIdleCapture,
			wantExit:  2,
			wantInMsg: "3 commit(s)",
		},
		{
			name:     "idle + clean + zero unpushed is no-op",
			unpushed: 0,
			capture:  claudeIdleCapture,
			wantExit: 0,
		},
		{
			name:     "running pane is no-op",
			unpushed: 1,
			capture:  claudeRunningCapture,
			wantExit: 0,
		},
		{
			name:     "dirty worktree is no-op",
			unpushed: 1,
			dirty:    true,
			capture:  claudeIdleCapture,
			wantExit: 0,
		},
		{
			name:     "MERGE_HEAD on worktree is no-op",
			unpushed: 1,
			midMerge: "MERGE_HEAD",
			capture:  claudeIdleCapture,
			wantExit: 0,
		},
		{
			name:     "rebase-merge dir is no-op",
			unpushed: 1,
			midMerge: "rebase-merge",
			capture:  claudeIdleCapture,
			wantExit: 0,
		},
		{
			name:     "rebase-apply dir is no-op",
			unpushed: 1,
			midMerge: "rebase-apply",
			capture:  claudeIdleCapture,
			wantExit: 0,
		},
		{
			name:     "missing SPORE_TASK_SLUG is no-op",
			unpushed: 1,
			capture:  claudeIdleCapture,
			noSlug:   true,
			wantExit: 0,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			fix := newRepoFixture(t, "demo", tc.unpushed)

			if tc.dirty {
				if err := os.WriteFile(filepath.Join(fix.worktree, "dirty"), []byte("u\n"), 0o644); err != nil {
					t.Fatal(err)
				}
			}
			if tc.midMerge != "" {
				gd, err := gitDir(fix.worktree)
				if err != nil {
					t.Fatalf("gitDir: %v", err)
				}
				target := filepath.Join(gd, tc.midMerge)
				if strings.HasPrefix(tc.midMerge, "rebase-") {
					if err := os.MkdirAll(target, 0o755); err != nil {
						t.Fatal(err)
					}
				} else {
					if err := os.WriteFile(target, []byte("deadbeef\n"), 0o644); err != nil {
						t.Fatal(err)
					}
				}
			}

			env := map[string]string{
				"SPORE_TASK_SLUG":    fix.slug,
				"SPORE_PROJECT_ROOT": fix.projectRoot,
			}
			if tc.noSlug {
				delete(env, "SPORE_TASK_SLUG")
			}
			deps := Deps{
				LookupEnv:   func(k string) (string, bool) { v, ok := env[k]; return v, ok },
				Capture:     tc.capture,
				SessionName: func(string, string) string { return "test-session" },
			}
			req := hooks.Request{HookEventName: "Stop", CWD: fix.worktree}
			got := Run(req, deps)
			if got.ExitCode != tc.wantExit {
				t.Fatalf("ExitCode = %d, want %d (stderr=%q)", got.ExitCode, tc.wantExit, got.Stderr)
			}
			if tc.wantInMsg != "" && !strings.Contains(got.Stderr, tc.wantInMsg) {
				t.Errorf("Stderr = %q, want substring %q", got.Stderr, tc.wantInMsg)
			}
			if tc.wantExit == 0 && got.Stderr != "" {
				t.Errorf("Stderr = %q on exit 0, want empty", got.Stderr)
			}
			if tc.wantExit == 2 && !strings.Contains(got.Stderr, "origin/main") {
				t.Errorf("Stderr = %q, want mention of origin/main", got.Stderr)
			}
		})
	}
}

func TestRunNoRemoteIsNoOp(t *testing.T) {
	// Repo with no `origin` remote at all; the count helper should
	// return 0 without error, and the hook should exit 0.
	root := t.TempDir()
	run(t, root, "git", "init", "-q", "-b", "main")
	run(t, root, "git", "config", "user.email", "test@example.com")
	run(t, root, "git", "config", "user.name", "test")
	if err := os.WriteFile(filepath.Join(root, "README"), []byte("seed\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	run(t, root, "git", "add", "README")
	run(t, root, "git", "commit", "-q", "-m", "seed")

	slug := "demo"
	run(t, root, "git", "branch", "wt/"+slug)
	worktree := filepath.Join(root, ".worktrees", slug)
	run(t, root, "git", "worktree", "add", "-q", worktree, "wt/"+slug)

	deps := Deps{
		LookupEnv: func(k string) (string, bool) {
			return map[string]string{"SPORE_TASK_SLUG": slug, "SPORE_PROJECT_ROOT": root}[k], true
		},
		Capture:     claudeIdleCapture,
		SessionName: func(string, string) string { return "test-session" },
	}
	got := Run(hooks.Request{CWD: worktree}, deps)
	if got.ExitCode != 0 {
		t.Fatalf("want exit 0 with no remote, got %d (stderr=%q)", got.ExitCode, got.Stderr)
	}
}

func TestRunEmptySessionNameIsNoOp(t *testing.T) {
	fix := newRepoFixture(t, "demo", 1)
	deps := Deps{
		LookupEnv: func(k string) (string, bool) {
			return map[string]string{"SPORE_TASK_SLUG": "demo", "SPORE_PROJECT_ROOT": fix.projectRoot}[k], true
		},
		Capture:     claudeIdleCapture,
		SessionName: func(string, string) string { return "" },
	}
	got := Run(hooks.Request{CWD: fix.worktree}, deps)
	if got.ExitCode != 0 {
		t.Fatalf("want exit 0, got %d", got.ExitCode)
	}
}

func TestUnpushedMainCommits(t *testing.T) {
	fix := newRepoFixture(t, "demo", 2)
	n, err := UnpushedMainCommits(fix.projectRoot)
	if err != nil {
		t.Fatalf("UnpushedMainCommits: %v", err)
	}
	if n != 2 {
		t.Fatalf("UnpushedMainCommits = %d, want 2", n)
	}
}
