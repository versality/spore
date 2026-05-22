package wtmergemechanical

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/versality/spore/internal/agentpane"
	"github.com/versality/spore/internal/hooks"
	"github.com/versality/spore/internal/hooks/wtgit"
)

// repoFixture is a temp git repo with a main branch, a wt/<slug>
// branch ahead by `extraCommits` commits, and a worktree checked out
// at .worktrees/<slug>. Returns (projectRoot, worktree).
type repoFixture struct {
	projectRoot string
	worktree    string
	slug        string
}

func newRepoFixture(t *testing.T, slug string, extraCommits int) repoFixture {
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

	branch := "wt/" + slug
	run(t, root, "git", "branch", branch)
	worktree := filepath.Join(root, ".worktrees", slug)
	run(t, root, "git", "worktree", "add", "-q", worktree, branch)
	for i := 0; i < extraCommits; i++ {
		name := fmt.Sprintf("file-%d", i)
		if err := os.WriteFile(filepath.Join(worktree, name), []byte("x\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		run(t, worktree, "git", "add", name)
		run(t, worktree, "git", "commit", "-q", "-m", "add "+name)
	}
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
		extra     int    // unmerged commits on the branch
		dirty     bool   // dirty working tree?
		midMerge  string // create this file inside the git-dir to simulate state
		capture   agentpane.CaptureFunc
		noSlug    bool
		wantExit  int
		wantInMsg string
	}{
		{
			name:      "idle + clean + unmerged fires exit 2",
			extra:     1,
			capture:   claudeIdleCapture,
			wantExit:  2,
			wantInMsg: "1 unmerged commit(s)",
		},
		{
			name:      "idle + clean + 3 unmerged shows N in message",
			extra:     3,
			capture:   claudeIdleCapture,
			wantExit:  2,
			wantInMsg: "3 unmerged commit(s)",
		},
		{
			name:     "idle + clean + no unmerged is no-op",
			extra:    0,
			capture:  claudeIdleCapture,
			wantExit: 0,
		},
		{
			name:     "running pane is no-op",
			extra:    1,
			capture:  claudeRunningCapture,
			wantExit: 0,
		},
		{
			name:     "dirty working tree is no-op",
			extra:    1,
			dirty:    true,
			capture:  claudeIdleCapture,
			wantExit: 0,
		},
		{
			name:     "MERGE_HEAD in git-dir is no-op",
			extra:    1,
			midMerge: "MERGE_HEAD",
			capture:  claudeIdleCapture,
			wantExit: 0,
		},
		{
			name:     "rebase-merge dir is no-op",
			extra:    1,
			midMerge: "rebase-merge",
			capture:  claudeIdleCapture,
			wantExit: 0,
		},
		{
			name:     "rebase-apply dir is no-op",
			extra:    1,
			midMerge: "rebase-apply",
			capture:  claudeIdleCapture,
			wantExit: 0,
		},
		{
			name:     "missing SPORE_TASK_SLUG is no-op",
			extra:    1,
			capture:  claudeIdleCapture,
			noSlug:   true,
			wantExit: 0,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			fix := newRepoFixture(t, "demo", tc.extra)

			if tc.dirty {
				if err := os.WriteFile(filepath.Join(fix.worktree, "dirty"), []byte("u\n"), 0o644); err != nil {
					t.Fatal(err)
				}
			}
			if tc.midMerge != "" {
				gd, err := wtgit.GitDir(fix.worktree)
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
			if tc.wantExit == 2 && !strings.Contains(got.Stderr, "wt/"+fix.slug) {
				t.Errorf("Stderr = %q, want branch name", got.Stderr)
			}
		})
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

func TestMainCheckoutFromWorktreeRelativeGitDir(t *testing.T) {
	main := filepath.Join(t.TempDir(), "project")
	worktree := filepath.Join(main, ".worktrees", "demo")
	gitDir := filepath.Join(main, ".git", "worktrees", "demo")
	if err := os.MkdirAll(worktree, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(gitDir, 0o755); err != nil {
		t.Fatal(err)
	}
	rel, err := filepath.Rel(worktree, gitDir)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(worktree, ".git"), []byte("gitdir: "+rel+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	got, ok := wtgit.MainCheckoutFromWorktree(worktree)
	if !ok {
		t.Fatal("mainCheckoutFromWorktree returned ok=false")
	}
	want, err := filepath.EvalSymlinks(main)
	if err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Fatalf("mainCheckoutFromWorktree = %q, want %q", got, want)
	}
}
