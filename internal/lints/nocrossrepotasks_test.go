package lints

import (
	"os/exec"
	"testing"
)

func ncrtFixture() (map[string]string, NoCrossRepoTasks) {
	cfg := NoCrossRepoTasks{
		ForbiddenSlugs: map[string]string{
			"spore-":    "~/projects/spore",
			"marketer-": "~/projects/marketer",
		},
		ForbiddenPaths: map[string]string{
			"~/projects/spore":              "~/projects/spore",
			"/home/user/projects/spore":     "~/projects/spore",
			"github.com/versality/spore":    "~/projects/spore",
			"~/projects/marketer":           "~/projects/marketer",
			"/home/user/projects/marketer":  "~/projects/marketer",
			"github.com/versality/marketer": "~/projects/marketer",
		},
		SlugAllowlist: map[string]bool{
			"spore-allowed": true,
		},
	}
	return map[string]string{}, cfg
}

func writeNCRTTask(slug, status, body string) string {
	return "---\nslug: " + slug + "\ntitle: t\nstatus: " + status + "\nproject: nix-config\nagent: claude\n---\n\n" + body
}

func TestNoCrossRepoTasks_WriteTargets(t *testing.T) {
	_, cfg := ncrtFixture()
	root := newTestRepo(t, map[string]string{
		"tasks/cross-repo-write.md":    writeNCRTTask("cross-repo-write", "active", "# Files\n\n- ~/projects/spore/foo.txt\n"),
		"tasks/cross-repo-readonly.md": writeNCRTTask("cross-repo-readonly", "active", "# Files\n\n- READ-ONLY: `~/projects/spore/foo.txt` (auditing only)\n"),
		"tasks/cross-repo-gh.md":       writeNCRTTask("cross-repo-gh", "active", "# Files\n\n- `github.com/versality/spore/dispatcher/foo.go` (extend)\n"),
		"tasks/cross-repo-backtick.md": writeNCRTTask("cross-repo-backtick", "active", "# Files\n\n- `/home/user/projects/spore/runner/main.go` (rewrite)\n"),
		"tasks/marketer-write-task.md": writeNCRTTask("marketer-write-task", "active", "# Files\n\n- `/home/user/projects/marketer/app.js` (rewrite)\n"),
		"tasks/cross-repo-done.md":     writeNCRTTask("cross-repo-done", "done", "# Files\n\n- ~/projects/spore/foo.txt\n"),
		"tasks/cross-repo-paused.md":   writeNCRTTask("cross-repo-paused", "paused", "# Files\n\n- ~/projects/spore/foo.txt\n"),
		"tasks/cross-repo-blocked.md":  writeNCRTTask("cross-repo-blocked", "blocked", "# Files\n\n- ~/projects/spore/foo.txt\n"),
		"tasks/cross-repo-prose.md":    writeNCRTTask("cross-repo-prose", "active", "Some body text mentioning ~/projects/spore as background.\n\n# Files\n\n- harness/foo.sh\n"),
	})
	issues, err := cfg.Run(root)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	hits := map[string][]string{}
	for _, i := range issues {
		hits[i.Path] = append(hits[i.Path], i.Message)
	}
	expectHit := []string{
		"tasks/cross-repo-write.md",
		"tasks/cross-repo-gh.md",
		"tasks/cross-repo-backtick.md",
		"tasks/marketer-write-task.md",
	}
	expectClean := []string{
		"tasks/cross-repo-readonly.md",
		"tasks/cross-repo-done.md",
		"tasks/cross-repo-paused.md",
		"tasks/cross-repo-blocked.md",
		"tasks/cross-repo-prose.md",
	}
	for _, p := range expectHit {
		if len(hits[p]) == 0 {
			t.Errorf("expected write-target hit on %s; got %v", p, hits)
		}
	}
	for _, p := range expectClean {
		// Allow slug-prefix hits for spore-* paths; we want no path hits, but cross-repo-paused etc. don't have spore- prefix.
		// So they should produce zero hits entirely.
		if hits[p] != nil {
			// marketer-write-task has marketer- slug prefix too -> this would also fire. But we expect that.
			t.Errorf("unexpected hit on %s: %v", p, hits[p])
		}
	}
}

func TestNoCrossRepoTasks_SlugPrefix(t *testing.T) {
	_, cfg := ncrtFixture()
	root := newTestRepo(t, map[string]string{
		"tasks/spore-fixture.md":    writeNCRTTask("spore-fixture", "active", "# Files\n\n- harness/local.sh\n"),
		"tasks/marketer-fixture.md": writeNCRTTask("marketer-fixture", "active", "# Files\n\n- harness/local.sh\n"),
		"tasks/spore-allowed.md":    writeNCRTTask("spore-allowed", "active", "# Files\n\n- harness/local.sh\n"),
	})
	issues, err := cfg.Run(root)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	got := map[string]bool{}
	for _, i := range issues {
		got[i.Path] = true
	}
	if !got["tasks/spore-fixture.md"] {
		t.Errorf("expected slug-prefix hit on spore-fixture, got %v", got)
	}
	if !got["tasks/marketer-fixture.md"] {
		t.Errorf("expected slug-prefix hit on marketer-fixture, got %v", got)
	}
	if got["tasks/spore-allowed.md"] {
		t.Errorf("allowlisted slug should be skipped")
	}
}

func TestNoCrossRepoTasks_OwnSlugScoping(t *testing.T) {
	_, cfg := ncrtFixture()
	root := newTestRepo(t, map[string]string{
		"tasks/cross-repo-paused-self.md": writeNCRTTask("cross-repo-paused-self", "paused", "# Files\n\n- ~/projects/spore/foo.txt\n"),
	})
	// Switch branch to wt/cross-repo-paused-self.
	cmd := exec.Command("git", "-C", root, "checkout", "-b", "wt/cross-repo-paused-self")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git checkout: %v\n%s", err, out)
	}
	issues, err := cfg.Run(root)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(issues) == 0 {
		t.Fatalf("expected hit on own-slug paused; got none")
	}
}

func TestNoCrossRepoTasks_NoTasksDir(t *testing.T) {
	_, cfg := ncrtFixture()
	root := newTestRepo(t, map[string]string{"README.md": "x\n"})
	issues, err := cfg.Run(root)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(issues) != 0 {
		t.Fatalf("clean repo should produce no issues, got %v", issues)
	}
}
