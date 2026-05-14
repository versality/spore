package boot

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestProbePausedStatusSilentWhenClean(t *testing.T) {
	t.Setenv("WT_CFG", t.TempDir())

	rc, out := probePausedStatus(Config{})
	if rc != 0 || out != "" {
		t.Fatalf("clean: rc=%d out=%q, want 0 / empty", rc, out)
	}
}

func TestProbePausedStatusFlagsHits(t *testing.T) {
	root := t.TempDir()
	tasksDir := filepath.Join(root, "tasks")
	if err := os.MkdirAll(tasksDir, 0o755); err != nil {
		t.Fatal(err)
	}
	files := map[string]string{
		"a.md":   "---\nslug: a\nstatus: paused\n---\nbody\n",
		"b.md":   "---\nslug: b\nstatus: parked\n---\nbody\n",
		"c.md":   "---\nslug: c\nstatus: paused\n---\nbody\n",
		"README": "ignore me\n",
	}
	for name, body := range files {
		if err := os.WriteFile(filepath.Join(tasksDir, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	cfgDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(cfgDir, "projects"), []byte(root+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("WT_CFG", cfgDir)

	rc, out := probePausedStatus(Config{})
	if rc != 2 {
		t.Fatalf("rc=%d out=%q, want 2", rc, out)
	}
	for _, want := range []string{"a.md", "c.md", "migrate-status paused blocked"} {
		if !strings.Contains(out, want) {
			t.Errorf("out missing %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "b.md") {
		t.Errorf("non-paused file leaked into out:\n%s", out)
	}
}
