package lints

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func techDebtTaskBody(slug, status string) string {
	return "---\nslug: " + slug + "\ntitle: t\nstatus: " + status + "\nproject: nix-config\nagent: claude\n---\n\nbody\n"
}

const rulingsHeader = "| hash | category | area | decision | reason | date | report-ref |\n|---|---|---|---|---|---|---|\n"

func TestTechDebtRulings(t *testing.T) {
	rulings := rulingsHeader +
		"| aaaaaaaa | sec | t | fix | r | d | x |\n" +
		"| bbbbbbbb | sec | t | ignore | r | d | x |\n"
	root := newTestRepo(t, map[string]string{
		"tasks/tech-debt-sec-aaaaaaaa.md": techDebtTaskBody("tech-debt-sec-aaaaaaaa", "done"),
		"tasks/tech-debt-sec-bbbbbbbb.md": techDebtTaskBody("tech-debt-sec-bbbbbbbb", "done"),
		"tasks/tech-debt-sec-cccccccc.md": techDebtTaskBody("tech-debt-sec-cccccccc", "done"),
		"tasks/tech-debt-sweep-impl.md":   techDebtTaskBody("tech-debt-sweep-impl", "done"),
		"tasks/tech-debt-sec-dddddddd.md": techDebtTaskBody("tech-debt-sec-dddddddd", "active"),
		"harness/tech-debt-rulings.md":    rulings,
	})
	issues, err := TechDebtRulings{}.Run(root)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	got := map[string]bool{}
	for _, i := range issues {
		got[i.Path] = true
	}
	if !got["tasks/tech-debt-sec-cccccccc.md"] {
		t.Errorf("missing hit on cccccccc; got %v", got)
	}
	if got["tasks/tech-debt-sec-aaaaaaaa.md"] || got["tasks/tech-debt-sec-bbbbbbbb.md"] {
		t.Errorf("ruled hashes should be skipped; got %v", got)
	}
	if got["tasks/tech-debt-sweep-impl.md"] {
		t.Errorf("umbrella task without 8-hex suffix should be skipped")
	}
	if got["tasks/tech-debt-sec-dddddddd.md"] {
		t.Errorf("non-done task should be skipped")
	}
}

func TestTaskDoneZeroCommits(t *testing.T) {
	rulings := rulingsHeader +
		"| aaaaaaaa | sec | t | fix | substantive | d | x |\n" +
		"| bbbbbbbb | sec | t | fix | bookkeeping-only | d | x |\n" +
		"| cccccccc | sec | t | ignore | exempt | d | x |\n"

	root := newTestRepo(t, map[string]string{
		"tasks/tech-debt-sec-aaaaaaaa.md": techDebtTaskBody("tech-debt-sec-aaaaaaaa", "done"),
		"tasks/tech-debt-sec-bbbbbbbb.md": techDebtTaskBody("tech-debt-sec-bbbbbbbb", "done"),
		"tasks/tech-debt-sec-cccccccc.md": techDebtTaskBody("tech-debt-sec-cccccccc", "done"),
		"harness/tech-debt-rulings.md":    rulings,
	})

	// Add a substantive commit for aaaaaaaa.
	if err := os.WriteFile(filepath.Join(root, "src.txt"), []byte("real fix\n"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	gitCmd := func(args ...string) {
		c := exec.Command("git", append([]string{"-C", root}, args...)...)
		c.Env = append(os.Environ(),
			"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t",
			"GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t",
		)
		if out, err := c.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	gitCmd("add", "src.txt")
	gitCmd("commit", "-q", "-m", "fix: substantive\n\nTask: tech-debt-sec-aaaaaaaa")

	// Add a bookkeeping-only commit for bbbbbbbb (touches only the task md).
	if err := os.WriteFile(filepath.Join(root, "tasks/tech-debt-sec-bbbbbbbb.md"),
		[]byte(techDebtTaskBody("tech-debt-sec-bbbbbbbb", "done")+"updated\n"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	gitCmd("add", "tasks/tech-debt-sec-bbbbbbbb.md")
	gitCmd("commit", "-q", "-m", "bookkeeping\n\nTask: tech-debt-sec-bbbbbbbb")

	issues, err := TaskDoneZeroCommits{}.Run(root)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	got := map[string]bool{}
	for _, i := range issues {
		got[i.Path] = true
	}
	if !got["tasks/tech-debt-sec-bbbbbbbb.md"] {
		t.Errorf("expected bookkeeping-only task hit; got %v", got)
	}
	if got["tasks/tech-debt-sec-aaaaaaaa.md"] {
		t.Errorf("substantive commit should clear aaaaaaaa")
	}
	if got["tasks/tech-debt-sec-cccccccc.md"] {
		t.Errorf("ignore decision exempt; got hit")
	}
}
