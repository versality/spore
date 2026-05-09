package main

import (
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/versality/spore/internal/event"
	"github.com/versality/spore/internal/shrink"
)

func writeFile(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func captureStdout(t *testing.T) (restore func() string) {
	t.Helper()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	orig := os.Stdout
	os.Stdout = w
	return func() string {
		_ = w.Close()
		os.Stdout = orig
		out, _ := io.ReadAll(r)
		_ = r.Close()
		return string(out)
	}
}

func TestShrinkProbePublishesAndPrints(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "harness", "a.sh"), "echo a\necho b\n")
	writeFile(t, filepath.Join(root, "harness", "b.sh"), "echo c\n")

	eventDir := t.TempDir()
	t.Setenv("SPORE_EVENT_DIR", eventDir)

	read := captureStdout(t)
	code := runShrink([]string{"probe", "--repo-root", root, "--wt-state-dir", filepath.Join(root, "missing")})
	out := read()
	if code != 0 {
		t.Fatalf("runShrink probe exit %d, stdout: %s", code, out)
	}

	var snap shrink.Snapshot
	if err := json.Unmarshal([]byte(strings.TrimSpace(out)), &snap); err != nil {
		t.Fatalf("decode stdout: %v (raw=%q)", err, out)
	}
	if snap.BashFiles != 2 || snap.BashLoc != 3 {
		t.Fatalf("snapshot mismatch: %+v", snap)
	}

	raw, err := os.ReadFile(filepath.Join(eventDir, "events.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimRight(string(raw), "\n"), "\n")
	if len(lines) != 1 {
		t.Fatalf("want 1 event row, got %d (%q)", len(lines), raw)
	}
	var ev event.Event
	if err := json.Unmarshal([]byte(lines[0]), &ev); err != nil {
		t.Fatal(err)
	}
	if ev.Source != "repo-shrink-probe" || ev.Kind != "shrink-probe" || ev.Level != event.LevelInfo {
		t.Errorf("event header mismatch: %+v", ev)
	}
	if len(ev.Data) == 0 {
		t.Fatal("event data is empty")
	}
	var inner shrink.Snapshot
	if err := json.Unmarshal(ev.Data, &inner); err != nil {
		t.Fatalf("decode event data: %v", err)
	}
	if inner.BashFiles != 2 || inner.BashLoc != 3 {
		t.Errorf("event data snapshot mismatch: %+v", inner)
	}
}

func TestShrinkProbeNoPublishSkipsEventBus(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "harness", "a.sh"), "echo a\n")

	eventDir := t.TempDir()
	t.Setenv("SPORE_EVENT_DIR", eventDir)

	read := captureStdout(t)
	code := runShrink([]string{"probe", "--repo-root", root, "--no-publish"})
	out := read()
	if code != 0 {
		t.Fatalf("runShrink probe exit %d, stdout: %s", code, out)
	}
	if _, err := os.Stat(filepath.Join(eventDir, "events.jsonl")); !os.IsNotExist(err) {
		t.Fatalf("--no-publish wrote to event bus: %v", err)
	}
	if !strings.Contains(out, `"bash_files":1`) {
		t.Errorf("stdout missing bash_files=1: %q", out)
	}
}

func TestShrinkUsageOnUnknownSub(t *testing.T) {
	code := runShrink([]string{"frobnicate"})
	if code != 2 {
		t.Errorf("unknown sub exit = %d, want 2", code)
	}
}
