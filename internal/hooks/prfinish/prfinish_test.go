package prfinish

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/versality/spore/internal/agentpane"
	"github.com/versality/spore/internal/hooks"
	"github.com/versality/spore/internal/hooks/wtgit"
	"github.com/versality/spore/internal/task/consumerclaim"
	"github.com/versality/spore/internal/task/frontmatter"
)

type repoFixture struct {
	projectRoot string
	worktree    string
	slug        string
}

func newRepoFixture(t *testing.T, slug string) repoFixture {
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

	run(t, root, "git", "branch", "wt/"+slug)
	worktree := filepath.Join(root, ".worktrees", slug)
	run(t, root, "git", "worktree", "add", "-q", worktree, "wt/"+slug)
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

type fakeGH struct {
	pr      PRState
	found   bool
	err     error
	runs    []RunSummary
	runsErr error
}

func (f fakeGH) ViewPR(string, string) (PRState, bool, error) {
	return f.pr, f.found, f.err
}

func (f fakeGH) ListRunsForCommit(string, string, string) ([]RunSummary, error) {
	return f.runs, f.runsErr
}

func TestRun(t *testing.T) {
	type testCase struct {
		name      string
		gh        fakeGH
		capture   agentpane.CaptureFunc
		dirty     bool
		midMerge  string
		noSlug    bool
		wantExit  int
		wantInMsg string
	}

	openMergeableGreen := fakeGH{
		found: true,
		pr: PRState{
			Number: 42, State: "OPEN", Mergeable: "MERGEABLE",
			Checks: []CheckRun{{Name: "Validate", Conclusion: "SUCCESS", Status: "COMPLETED"}},
		},
	}
	openConflicting := fakeGH{
		found: true,
		pr: PRState{
			Number: 42, State: "OPEN", Mergeable: "CONFLICTING",
			Checks: []CheckRun{{Name: "Validate", Conclusion: "SUCCESS", Status: "COMPLETED"}},
		},
	}
	openCIRed := fakeGH{
		found: true,
		pr: PRState{
			Number: 42, State: "OPEN", Mergeable: "MERGEABLE",
			Checks: []CheckRun{
				{Name: "Validate", Conclusion: "FAILURE", Status: "COMPLETED", URL: "https://example/run/1"},
				{Name: "Cover", Conclusion: "SUCCESS", Status: "COMPLETED"},
			},
		},
	}
	openCIPending := fakeGH{
		found: true,
		pr: PRState{
			Number: 42, State: "OPEN", Mergeable: "MERGEABLE",
			Checks: []CheckRun{{Name: "Validate", Conclusion: "", Status: "IN_PROGRESS"}},
		},
	}
	openMergeableUnknown := fakeGH{
		found: true,
		pr: PRState{
			Number: 42, State: "OPEN", Mergeable: "UNKNOWN",
			Checks: []CheckRun{{Name: "Validate", Conclusion: "SUCCESS", Status: "COMPLETED"}},
		},
	}
	merged := fakeGH{found: true, pr: PRState{Number: 42, State: "MERGED"}}
	closed := fakeGH{found: true, pr: PRState{Number: 42, State: "CLOSED"}}
	noPR := fakeGH{found: false}

	cases := []testCase{
		{name: "open mergeable green fires merge prompt", gh: openMergeableGreen, capture: claudeIdleCapture, wantExit: 2, wantInMsg: "gh pr merge 42"},
		{name: "open conflicting fires rebase prompt", gh: openConflicting, capture: claudeIdleCapture, wantExit: 2, wantInMsg: "merge conflicts"},
		{name: "open CI red fires fix prompt with job", gh: openCIRed, capture: claudeIdleCapture, wantExit: 2, wantInMsg: "Validate (failure)"},
		{name: "open CI red includes URL", gh: openCIRed, capture: claudeIdleCapture, wantExit: 2, wantInMsg: "https://example/run/1"},
		{name: "open CI pending is no-op", gh: openCIPending, capture: claudeIdleCapture, wantExit: 0},
		{name: "open mergeable=UNKNOWN is no-op", gh: openMergeableUnknown, capture: claudeIdleCapture, wantExit: 0},
		{name: "merged is no-op", gh: merged, capture: claudeIdleCapture, wantExit: 0},
		{name: "closed is no-op", gh: closed, capture: claudeIdleCapture, wantExit: 0},
		{name: "no PR is no-op", gh: noPR, capture: claudeIdleCapture, wantExit: 0},
		{name: "running pane is no-op", gh: openMergeableGreen, capture: claudeRunningCapture, wantExit: 0},
		{name: "dirty worktree is no-op", gh: openMergeableGreen, capture: claudeIdleCapture, dirty: true, wantExit: 0},
		{name: "MERGE_HEAD is no-op", gh: openMergeableGreen, capture: claudeIdleCapture, midMerge: "MERGE_HEAD", wantExit: 0},
		{name: "rebase-merge is no-op", gh: openMergeableGreen, capture: claudeIdleCapture, midMerge: "rebase-merge", wantExit: 0},
		{name: "missing slug is no-op", gh: openMergeableGreen, capture: claudeIdleCapture, noSlug: true, wantExit: 0},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			fix := newRepoFixture(t, "demo")
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
					if err := os.WriteFile(target, []byte("x\n"), 0o644); err != nil {
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
				GH:          tc.gh,
				// Direct-push sub-tree must not fire for the PR-flow
				// cases; force IsAncestor=false so a no-PR row stays
				// no-op here. Dedicated TestRunDirectPush below covers
				// the in-main branch.
				HeadSHA:    func(string) (string, error) { return "deadbeef", nil },
				IsAncestor: func(string, string, string) (bool, error) { return false, nil },
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
		})
	}
}

func TestRunDirectPush(t *testing.T) {
	const sha = "abcdef1234567890abcdef1234567890abcdef12"

	runsSuccess := []RunSummary{
		{DatabaseID: 1, Name: "CI", Status: "COMPLETED", Conclusion: "SUCCESS", URL: "https://example/run/1", HeadSHA: sha},
	}
	runsPending := []RunSummary{
		{DatabaseID: 1, Name: "CI", Status: "IN_PROGRESS", HeadSHA: sha},
	}
	runsQueued := []RunSummary{
		{DatabaseID: 1, Name: "CI", Status: "QUEUED", HeadSHA: sha},
	}
	runsFailure := []RunSummary{
		{DatabaseID: 1, Name: "CI", Status: "COMPLETED", Conclusion: "FAILURE", URL: "https://example/run/1", HeadSHA: sha},
	}
	runsCancelled := []RunSummary{
		{DatabaseID: 1, Name: "CI", Status: "COMPLETED", Conclusion: "CANCELLED", URL: "https://example/run/1", HeadSHA: sha},
	}
	runsTimedOut := []RunSummary{
		{DatabaseID: 1, Name: "CI", Status: "COMPLETED", Conclusion: "TIMED_OUT", URL: "https://example/run/1", HeadSHA: sha},
	}
	runsMixed := []RunSummary{
		{DatabaseID: 1, Name: "Cover", Status: "COMPLETED", Conclusion: "SUCCESS", HeadSHA: sha},
		{DatabaseID: 2, Name: "CI", Status: "COMPLETED", Conclusion: "FAILURE", URL: "https://example/run/2", HeadSHA: sha},
	}
	runsSkipped := []RunSummary{
		{DatabaseID: 1, Name: "CI", Status: "COMPLETED", Conclusion: "SKIPPED", HeadSHA: sha},
	}

	type tc struct {
		name       string
		runs       []RunSummary
		runsErr    error
		inMain     bool
		headSHAErr error
		wantExit   int
		wantInMsg  string
	}
	cases := []tc{
		{name: "no PR + sha not in origin/main is no-op", inMain: false, runs: runsFailure, wantExit: 0},
		{name: "no PR + sha in main, no runs scheduled yet", inMain: true, runs: nil, wantExit: 0},
		{name: "no PR + sha in main, run in_progress", inMain: true, runs: runsPending, wantExit: 0},
		{name: "no PR + sha in main, run queued", inMain: true, runs: runsQueued, wantExit: 0},
		{name: "no PR + sha in main, all success", inMain: true, runs: runsSuccess, wantExit: 0},
		{name: "no PR + sha in main, skipped counts as fine", inMain: true, runs: runsSkipped, wantExit: 0},
		{name: "no PR + sha in main, failure fires fix prompt", inMain: true, runs: runsFailure, wantExit: 2, wantInMsg: "CI failed for abcdef1"},
		{name: "no PR + sha in main, failure URL is in stderr", inMain: true, runs: runsFailure, wantExit: 2, wantInMsg: "https://example/run/1"},
		{name: "no PR + sha in main, cancelled fires prompt", inMain: true, runs: runsCancelled, wantExit: 2, wantInMsg: "cancelled"},
		{name: "no PR + sha in main, timed_out fires prompt", inMain: true, runs: runsTimedOut, wantExit: 2, wantInMsg: "timed_out"},
		{name: "no PR + sha in main, mixed has any failure -> exit 2", inMain: true, runs: runsMixed, wantExit: 2, wantInMsg: "CI (failure)"},
		{name: "no PR + sha in main, runsErr degrades silent", inMain: true, runsErr: os.ErrNotExist, wantExit: 0},
		{name: "no PR + HeadSHA error degrades silent", inMain: true, runs: runsFailure, headSHAErr: os.ErrNotExist, wantExit: 0},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			fix := newRepoFixture(t, "demo")
			env := map[string]string{
				"SPORE_TASK_SLUG":    fix.slug,
				"SPORE_PROJECT_ROOT": fix.projectRoot,
			}
			deps := Deps{
				LookupEnv:   func(k string) (string, bool) { v, ok := env[k]; return v, ok },
				Capture:     claudeIdleCapture,
				SessionName: func(string, string) string { return "test-session" },
				GH:          fakeGH{found: false, runs: c.runs, runsErr: c.runsErr},
				HeadSHA: func(string) (string, error) {
					if c.headSHAErr != nil {
						return "", c.headSHAErr
					}
					return sha, nil
				},
				IsAncestor: func(string, string, string) (bool, error) { return c.inMain, nil },
			}
			got := Run(hooks.Request{HookEventName: "Stop", CWD: fix.worktree}, deps)
			if got.ExitCode != c.wantExit {
				t.Fatalf("ExitCode = %d, want %d (stderr=%q)", got.ExitCode, c.wantExit, got.Stderr)
			}
			if c.wantInMsg != "" && !strings.Contains(got.Stderr, c.wantInMsg) {
				t.Errorf("Stderr = %q, want substring %q", got.Stderr, c.wantInMsg)
			}
			if c.wantExit == 0 && got.Stderr != "" {
				t.Errorf("Stderr = %q on exit 0, want empty", got.Stderr)
			}
		})
	}
}

func TestRunMergedWithStaleClaim(t *testing.T) {
	fix := newRepoFixture(t, "demo")
	consumerDir := t.TempDir()
	// Create the obsoleted file in the consumer so the claim is unresolved.
	if err := os.WriteFile(filepath.Join(consumerDir, "old.sh"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}

	deps := Deps{
		LookupEnv: func(k string) (string, bool) {
			return map[string]string{"SPORE_TASK_SLUG": "demo", "SPORE_PROJECT_ROOT": fix.projectRoot}[k], true
		},
		Capture:     claudeIdleCapture,
		SessionName: func(string, string) string { return "test-session" },
		GH:          fakeGH{found: true, pr: PRState{Number: 7, State: "MERGED"}},
		Consumer: consumerclaim.Deps{
			LookupEnv: func(k string) (string, bool) {
				if strings.HasPrefix(k, "SPORE_CONSUMER_") {
					return consumerDir, true
				}
				return "", false
			},
			HomeDir: func() (string, error) { return "/no/home", nil },
			Stat:    os.Stat,
		},
		ReadTaskMeta: func(string, string) (frontmatter.Meta, error) {
			return frontmatter.Meta{
				ConsumerClaims: []string{"nix-config:path:old.sh"},
			}, nil
		},
	}
	got := Run(hooks.Request{CWD: fix.worktree}, deps)
	if got.ExitCode != 2 {
		t.Fatalf("want exit 2, got %d (stderr=%q)", got.ExitCode, got.Stderr)
	}
	if !strings.Contains(got.Stderr, "PR #7 is merged") {
		t.Errorf("Stderr = %q, want mention of PR #7 merged", got.Stderr)
	}
	if !strings.Contains(got.Stderr, "nix-config:path:old.sh") {
		t.Errorf("Stderr = %q, want claim spec", got.Stderr)
	}
}

func TestRunMergedAllResolved(t *testing.T) {
	fix := newRepoFixture(t, "demo")
	consumerDir := t.TempDir() // empty: claim is resolved.

	deps := Deps{
		LookupEnv: func(k string) (string, bool) {
			return map[string]string{"SPORE_TASK_SLUG": "demo", "SPORE_PROJECT_ROOT": fix.projectRoot}[k], true
		},
		Capture:     claudeIdleCapture,
		SessionName: func(string, string) string { return "test-session" },
		GH:          fakeGH{found: true, pr: PRState{Number: 7, State: "MERGED"}},
		Consumer: consumerclaim.Deps{
			LookupEnv: func(k string) (string, bool) {
				if strings.HasPrefix(k, "SPORE_CONSUMER_") {
					return consumerDir, true
				}
				return "", false
			},
			HomeDir: func() (string, error) { return "/no/home", nil },
			Stat:    os.Stat,
		},
		ReadTaskMeta: func(string, string) (frontmatter.Meta, error) {
			return frontmatter.Meta{
				ConsumerClaims: []string{"nix-config:path:nope.sh"},
			}, nil
		},
	}
	got := Run(hooks.Request{CWD: fix.worktree}, deps)
	if got.ExitCode != 0 {
		t.Fatalf("want exit 0 (all claims resolved), got %d (stderr=%q)", got.ExitCode, got.Stderr)
	}
}

func TestRunMergedNoClaims(t *testing.T) {
	fix := newRepoFixture(t, "demo")
	deps := Deps{
		LookupEnv: func(k string) (string, bool) {
			return map[string]string{"SPORE_TASK_SLUG": "demo", "SPORE_PROJECT_ROOT": fix.projectRoot}[k], true
		},
		Capture:      claudeIdleCapture,
		SessionName:  func(string, string) string { return "test-session" },
		GH:           fakeGH{found: true, pr: PRState{Number: 7, State: "MERGED"}},
		ReadTaskMeta: func(string, string) (frontmatter.Meta, error) { return frontmatter.Meta{}, nil },
	}
	got := Run(hooks.Request{CWD: fix.worktree}, deps)
	if got.ExitCode != 0 {
		t.Fatalf("want exit 0, got %d (stderr=%q)", got.ExitCode, got.Stderr)
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

func TestRunGHError(t *testing.T) {
	fix := newRepoFixture(t, "demo")
	deps := Deps{
		LookupEnv: func(k string) (string, bool) {
			return map[string]string{"SPORE_TASK_SLUG": "demo", "SPORE_PROJECT_ROOT": fix.projectRoot}[k], true
		},
		Capture:     claudeIdleCapture,
		SessionName: func(string, string) string { return "test-session" },
		GH:          fakeGH{err: os.ErrNotExist},
	}
	got := Run(hooks.Request{CWD: fix.worktree}, deps)
	if got.ExitCode != 0 {
		t.Fatalf("want exit 0 on gh error, got %d (stderr=%q)", got.ExitCode, got.Stderr)
	}
}

// Parse-shape tests for the wire format live in internal/gh now.
