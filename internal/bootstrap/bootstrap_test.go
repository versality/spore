package bootstrap

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/versality/spore/internal/align"
)

// fixture builds a minimal git repo + per-test state dir and returns
// the project root and the spore state dir under it. HOME and
// XDG_STATE_HOME are pinned so align.Resolve writes inside the
// tempdir.
func fixture(t *testing.T) (root, stateDir string) {
	t.Helper()
	root = t.TempDir()
	stateRoot := filepath.Join(root, "state")
	t.Setenv("XDG_STATE_HOME", stateRoot)
	t.Setenv("HOME", filepath.Join(root, "home"))
	stateDir = filepath.Join(stateRoot, "spore", filepath.Base(root))
	return root, stateDir
}

// writeFile is a tiny helper that creates parents and writes data.
func writeFile(t *testing.T, p string, data []byte) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(p, data, 0o644); err != nil {
		t.Fatalf("write %s: %v", p, err)
	}
}

func TestRunRecordsPerStageState(t *testing.T) {
	root, stateDir := fixture(t)
	writeFile(t, filepath.Join(root, "flake.nix"), []byte("# flake\n"))

	res, err := Run(root, stateDir, DefaultStages(), Options{})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if res.Done {
		t.Fatalf("Done: true; want false (later stages should still block)")
	}
	if res.Current != "info-gathered" {
		t.Fatalf("Current=%q; want info-gathered (the new stage right after repo-mapped)", res.Current)
	}
	if !strings.Contains(res.Blocker, "info-gathered.json") {
		t.Fatalf("Blocker=%q; want a hint about info-gathered.json", res.Blocker)
	}

	st, err := Status(stateDir, DefaultStages())
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	want := map[string]string{
		"repo-mapped":        StatusCompleted,
		"info-gathered":      StatusFailed,
		"tests-pass":         StatusPending,
		"creds-wired":        StatusPending,
		"readme-followed":    StatusPending,
		"validation-green":   StatusPending,
		"pilot-aligned":      StatusPending,
		"worker-fleet-ready": StatusPending,
	}
	for _, r := range st {
		if want[r.Name] != r.Record.Status {
			t.Errorf("stage %s: status=%q want %q", r.Name, r.Record.Status, want[r.Name])
		}
	}

	repoRec := findRecord(t, st, "repo-mapped")
	if repoRec.StartedAt == "" || repoRec.CompletedAt == "" {
		t.Fatalf("repo-mapped record missing timestamps: %+v", repoRec)
	}
	if !strings.Contains(repoRec.Notes, "nix") {
		t.Errorf("repo-mapped notes=%q; want detected language", repoRec.Notes)
	}
}

func TestRunSkipMarksStagesComplete(t *testing.T) {
	root, stateDir := fixture(t)
	writeFile(t, filepath.Join(root, "flake.nix"), []byte("# flake\n"))
	writeFile(t, filepath.Join(root, "README.md"), []byte("# proj\n"))

	res, err := Run(root, stateDir, DefaultStages(), Options{
		Skip: []string{"info-gathered", "tests-pass", "creds-wired", "readme-followed", "validation-green", "pilot-aligned", "worker-fleet-ready"},
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !res.Done {
		t.Fatalf("Done: false; want true after skipping all blocking stages. blocker=%q", res.Blocker)
	}
	if res.Current != "worker-fleet-ready" {
		t.Fatalf("Current=%q; want worker-fleet-ready", res.Current)
	}
	wantSkipped := []string{"info-gathered", "tests-pass", "creds-wired", "readme-followed", "validation-green", "pilot-aligned", "worker-fleet-ready"}
	if got := strings.Join(res.Skipped, ","); got != strings.Join(wantSkipped, ",") {
		t.Errorf("Skipped=%v; want %v", res.Skipped, wantSkipped)
	}
}

func TestRunIsAdditive(t *testing.T) {
	root, stateDir := fixture(t)
	writeFile(t, filepath.Join(root, "flake.nix"), []byte("# flake\n"))

	if _, err := Run(root, stateDir, DefaultStages(), Options{}); err != nil {
		t.Fatalf("Run #1: %v", err)
	}
	rec1 := findStageOnDisk(t, stateDir, "repo-mapped")
	if rec1.Status != StatusCompleted {
		t.Fatalf("repo-mapped: %s; want completed", rec1.Status)
	}
	first := rec1.CompletedAt

	if _, err := Run(root, stateDir, DefaultStages(), Options{}); err != nil {
		t.Fatalf("Run #2: %v", err)
	}
	rec2 := findStageOnDisk(t, stateDir, "repo-mapped")
	if rec2.CompletedAt != first {
		t.Errorf("repo-mapped re-fired (CompletedAt changed): %s -> %s", first, rec2.CompletedAt)
	}
}

func TestResetWipesState(t *testing.T) {
	root, stateDir := fixture(t)
	writeFile(t, filepath.Join(root, "flake.nix"), []byte("# flake\n"))
	if _, err := Run(root, stateDir, DefaultStages(), Options{}); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if _, err := os.Stat(filepath.Join(stateDir, "bootstrap.json")); err != nil {
		t.Fatalf("bootstrap.json should exist before reset: %v", err)
	}
	if err := Reset(stateDir); err != nil {
		t.Fatalf("Reset: %v", err)
	}
	if _, err := os.Stat(filepath.Join(stateDir, "bootstrap.json")); !os.IsNotExist(err) {
		t.Fatalf("bootstrap.json should be gone: stat err=%v", err)
	}
	if err := Reset(stateDir); err != nil {
		t.Fatalf("Reset (idempotent): %v", err)
	}
}

func TestRunRejectsUnknownSkip(t *testing.T) {
	root, stateDir := fixture(t)
	writeFile(t, filepath.Join(root, "flake.nix"), []byte("# flake\n"))
	_, err := Run(root, stateDir, DefaultStages(), Options{Skip: []string{"no-such-stage"}})
	if err == nil || !strings.Contains(err.Error(), "no-such-stage") {
		t.Fatalf("Run with bad --skip: err=%v; want rejection", err)
	}
}

func TestRunSmokeFromFreshToWorkerFleetReady(t *testing.T) {
	// Walks the entire stage graph in a tempdir-only project: write
	// the markers each detector expects, fill the alignment + info
	// + readme sentinels, then assert Done at worker-fleet-ready.
	root, stateDir := fixture(t)
	writeFile(t, filepath.Join(root, "flake.nix"), []byte("# flake\n"))
	writeFile(t, filepath.Join(root, "justfile"), []byte("check:\n\ttrue\n"))
	writeFile(t, filepath.Join(root, "README.md"), []byte("# project\n\nTo use: run `just check`.\n"))
	writeFile(t, filepath.Join(root, "CLAUDE.md"), []byte("# CLAUDE\n\nSecrets live in `.envrc`.\n"))
	writeFile(t, filepath.Join(root, ".envrc"), []byte("export FOO=bar\n"))

	// Init a git repo so spore lints can list files.
	mustGitInit(t, root)

	// info-gathered sentinel: minimal valid JSON.
	ig := InfoGathered{
		Tickets:   InfoSurface{Tool: "none", Decision: "use spore tasks"},
		Knowledge: InfoSurface{Tool: "none", Decision: "use docs/todo + spore docs/list.md"},
	}
	writeJSON(t, filepath.Join(stateDir, "info-gathered.json"), ig)

	// readme-followed sentinel.
	rf := ReadmeFollowed{
		ReadmePath: filepath.Join(root, "README.md"),
		Items: []ReadmeFollowItem{
			{Step: "run `just check`", Status: readmeStatusOK},
		},
	}
	writeJSON(t, filepath.Join(stateDir, "readme-followed.json"), rf)

	// alignment criteria + flip.
	p, err := align.Resolve(root)
	if err != nil {
		t.Fatalf("align.Resolve: %v", err)
	}
	for i := 0; i < 10; i++ {
		body := "pref"
		if i < 3 {
			body = "[promoted] pref"
		}
		if err := align.Note(p, body); err != nil {
			t.Fatalf("align.Note: %v", err)
		}
	}
	if err := align.Flip(p); err != nil {
		t.Fatalf("align.Flip: %v", err)
	}

	res, err := Run(root, stateDir, DefaultStages(), Options{})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !res.Done {
		t.Fatalf("not Done: %+v", res)
	}
	if res.Current != "worker-fleet-ready" {
		t.Fatalf("Current=%q; want worker-fleet-ready", res.Current)
	}

	// Subsequent run is a no-op (every stage already completed).
	res2, err := Run(root, stateDir, DefaultStages(), Options{})
	if err != nil {
		t.Fatalf("Run #2: %v", err)
	}
	if len(res2.Advanced) != 0 {
		t.Errorf("Advanced=%v on second run; want empty (additive)", res2.Advanced)
	}
}

func findRecord(t *testing.T, rs []NamedRecord, name string) StageRecord {
	t.Helper()
	for _, r := range rs {
		if r.Name == name {
			return r.Record
		}
	}
	t.Fatalf("no record for stage %q", name)
	return StageRecord{}
}

func findStageOnDisk(t *testing.T, stateDir, name string) StageRecord {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(stateDir, "bootstrap.json"))
	if err != nil {
		t.Fatalf("read state: %v", err)
	}
	var s State
	if err := json.Unmarshal(b, &s); err != nil {
		t.Fatalf("parse state: %v", err)
	}
	return s.Stages[name]
}

func mustGitInit(t *testing.T, root string) {
	t.Helper()
	run := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-C", root}, args...)...)
		cmd.Env = append(os.Environ(),
			"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t",
			"GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t",
		)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	run("init", "-q", "-b", "main")
	run("config", "commit.gpgsign", "false")
	run("add", "-A")
	run("commit", "-q", "--allow-empty", "-m", "initial")
}

func writeJSON(t *testing.T, path string, v any) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if err := os.WriteFile(path, b, 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}
