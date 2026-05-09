package main

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/versality/spore/internal/event"
)

func setupEventDir(t *testing.T) string {
	t.Helper()
	d := t.TempDir()
	t.Setenv("SPORE_EVENT_DIR", d)
	t.Setenv("SPORE_EVENT_MAX_BYTES", "")
	return d
}

func TestPublishWritesOneRow(t *testing.T) {
	dir := setupEventDir(t)
	code := runEventPublish([]string{
		"--source", "skyhelm",
		"--kind", "boot",
		"--level", "info",
		"--", "skyhelm boot",
	})
	if code != 0 {
		t.Fatalf("publish exit %d", code)
	}
	b, err := os.ReadFile(filepath.Join(dir, "events.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimRight(string(b), "\n"), "\n")
	if len(lines) != 1 {
		t.Fatalf("want 1 row, got %d (%q)", len(lines), b)
	}
	var ev event.Event
	if err := json.Unmarshal([]byte(lines[0]), &ev); err != nil {
		t.Fatal(err)
	}
	if ev.Source != "skyhelm" || ev.Kind != "boot" || ev.Level != "info" || ev.Message != "skyhelm boot" {
		t.Fatalf("event mismatch: %+v", ev)
	}
}

func TestPublishRejectsBadLevel(t *testing.T) {
	setupEventDir(t)
	code := runEventPublish([]string{
		"--source", "s", "--kind", "k", "--level", "debug", "--", "msg",
	})
	if code == 0 {
		t.Fatal("publish should reject level=debug")
	}
}

func TestPublishRejectsMissingSource(t *testing.T) {
	setupEventDir(t)
	code := runEventPublish([]string{
		"--kind", "k", "--level", "info", "--", "msg",
	})
	if code == 0 {
		t.Fatal("publish should reject missing source")
	}
}

func TestPublishWithSlugAndData(t *testing.T) {
	dir := setupEventDir(t)
	code := runEventPublish([]string{
		"--source", "wt-task", "--kind", "wt-task:done",
		"--level", "info", "--slug", "abc",
		"--data", `{"k":1}`,
		"--", "task done",
	})
	if code != 0 {
		t.Fatalf("publish exit %d", code)
	}
	b, _ := os.ReadFile(filepath.Join(dir, "events.jsonl"))
	var ev event.Event
	if err := json.Unmarshal(b[:len(b)-1], &ev); err != nil {
		t.Fatal(err)
	}
	if ev.Slug != "abc" {
		t.Fatalf("slug: %q", ev.Slug)
	}
	var data map[string]int
	if err := json.Unmarshal(ev.Data, &data); err != nil {
		t.Fatal(err)
	}
	if data["k"] != 1 {
		t.Fatalf("data: %+v", data)
	}
}

func TestParseWatchFilter(t *testing.T) {
	cases := []struct {
		expr  string
		want  event.Filter
		errOK bool
	}{
		{"", event.Filter{}, false},
		{"level=error", event.Filter{Level: "error"}, false},
		{"level=error AND source=systemd", event.Filter{Level: "error", Source: "systemd"}, false},
		{"  level = error  and  source = sky  ", event.Filter{Level: "error", Source: "sky"}, false},
		{"kind=k AND slug=s", event.Filter{Kind: "k", Slug: "s"}, false},
		{"badkey=x", event.Filter{}, true},
		{"missing-eq", event.Filter{}, true},
	}
	for _, c := range cases {
		t.Run(c.expr, func(t *testing.T) {
			got, err := parseWatchFilter(c.expr)
			if c.errOK {
				if err == nil {
					t.Fatalf("want error, got %+v", got)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != c.want {
				t.Fatalf("got %+v want %+v", got, c.want)
			}
		})
	}
}

func TestWatchExecRunsPerEvent(t *testing.T) {
	if _, err := exec.LookPath("sh"); err != nil {
		t.Skip("sh not available")
	}
	dir := setupEventDir(t)
	out := filepath.Join(dir, "hits.log")

	bin := buildSporeBinary(t)

	// Spawn `spore event watch` in the background. Filter on
	// level=error, exec appends one line per match.
	cmd := exec.Command(bin, "event", "watch",
		"--filter", "level=error",
		"--exec", "echo $SPORE_EVENT_KIND >> "+out)
	cmd.Env = append(os.Environ(), "SPORE_EVENT_DIR="+dir)
	cmd.Stdout = os.Stderr
	cmd.Stderr = os.Stderr
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if cmd.Process != nil {
			cmd.Process.Kill()
		}
		cmd.Wait()
	})

	// Wait until the watcher has had a chance to attach.
	time.Sleep(150 * time.Millisecond)

	// Publish 4 events (3 errors should fire the exec, 1 info should not).
	for i, ev := range []event.Event{
		{Source: "t", Level: "error", Kind: "t:e1", Message: "1"},
		{Source: "t", Level: "info", Kind: "t:i", Message: "2"},
		{Source: "t", Level: "error", Kind: "t:e2", Message: "3"},
		{Source: "t", Level: "error", Kind: "t:e3", Message: "4"},
	} {
		ev := ev
		if err := event.Append(&ev); err != nil {
			t.Fatalf("publish %d: %v", i, err)
		}
	}

	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		b, err := os.ReadFile(out)
		if err == nil {
			lines := strings.Split(strings.TrimRight(string(b), "\n"), "\n")
			if len(lines) >= 3 {
				if lines[0] != "t:e1" || lines[1] != "t:e2" || lines[2] != "t:e3" {
					t.Fatalf("watch output wrong: %v", lines)
				}
				return
			}
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatal("watch did not fire exec 3 times within deadline")
}

func TestTailFiltersAndLimits(t *testing.T) {
	setupEventDir(t)
	for _, lvl := range []string{"info", "warn", "error", "info", "error"} {
		if err := event.Append(&event.Event{Source: "s", Level: lvl, Kind: "k", Message: lvl}); err != nil {
			t.Fatal(err)
		}
	}

	r, w, _ := os.Pipe()
	stdout := os.Stdout
	os.Stdout = w
	t.Cleanup(func() { os.Stdout = stdout })

	code := runEventTail([]string{"--level", "error", "-n", "0"})
	w.Close()
	if code != 0 {
		t.Fatalf("tail exit %d", code)
	}
	b, _ := readAll(r)
	lines := strings.Split(strings.TrimRight(string(b), "\n"), "\n")
	if len(lines) != 2 {
		t.Fatalf("want 2 error rows, got %d: %q", len(lines), b)
	}
	for _, l := range lines {
		var ev event.Event
		if err := json.Unmarshal([]byte(l), &ev); err != nil {
			t.Fatal(err)
		}
		if ev.Level != "error" {
			t.Fatalf("expected level=error, got %s", ev.Level)
		}
	}
}

func readAll(r *os.File) ([]byte, error) {
	defer r.Close()
	b := make([]byte, 0, 1024)
	buf := make([]byte, 1024)
	for {
		n, err := r.Read(buf)
		if n > 0 {
			b = append(b, buf[:n]...)
		}
		if err != nil {
			if err.Error() == "EOF" {
				return b, nil
			}
			return b, nil
		}
	}
}

// buildSporeBinary compiles `cmd/spore` into a temp dir and returns the
// path. The test runner's working dir is cmd/spore, so the build walks
// up to the module root before invoking `go build`.
func buildSporeBinary(t *testing.T) string {
	t.Helper()
	d := t.TempDir()
	bin := filepath.Join(d, "spore")
	cmd := exec.Command("go", "build", "-o", bin, "./cmd/spore")
	cmd.Dir = moduleRoot(t)
	cmd.Env = os.Environ()
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("go build: %v\n%s", err, out)
	}
	return bin
}

func moduleRoot(t *testing.T) string {
	t.Helper()
	wd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	dir := wd
	for i := 0; i < 5; i++ {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		dir = filepath.Dir(dir)
	}
	t.Fatalf("go.mod not found from %s", wd)
	return ""
}
