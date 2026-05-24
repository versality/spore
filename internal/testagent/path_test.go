package testagent

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestInstallPathHarnessRunsNamedClaudeAndCodex(t *testing.T) {
	h := InstallPathHarness(t, PathOptions{IncludeClaude: true, IncludeCodex: true})
	logPath := filepath.Join(t.TempDir(), "events.jsonl")

	for _, name := range []string{"claude", "codex"} {
		t.Run(name, func(t *testing.T) {
			cmd := exec.Command(filepath.Join(h.BinDir, name))
			cmd.Env = append(os.Environ(),
				"PATH="+h.PATH,
				EnvMode+"="+ModeWorkThenExit,
				EnvEventLog+"="+logPath,
			)
			if out, err := cmd.CombinedOutput(); err != nil {
				t.Fatalf("%s: %v: %s", name, err, out)
			}
		})
	}
	events := readEvents(t, logPath)
	starts := eventsByType(events, "start")
	if len(starts) != 2 {
		t.Fatalf("start event count = %d, want 2: %s", len(starts), eventTypes(events))
	}
	if starts[0].Provider != "claude" {
		t.Fatalf("first provider = %q, want claude", starts[0].Provider)
	}
	if starts[1].Provider != "codex" {
		t.Fatalf("second provider = %q, want codex", starts[1].Provider)
	}
}

func eventsByType(events []Event, typ string) []Event {
	var out []Event
	for _, event := range events {
		if event.Type == typ {
			out = append(out, event)
		}
	}
	return out
}

func TestInstallPathHarnessCanOmitCodex(t *testing.T) {
	h := InstallPathHarness(t, PathOptions{IncludeClaude: true})
	if _, err := os.Stat(filepath.Join(h.BinDir, "codex")); err == nil {
		t.Fatal("codex exists, want missing executable")
	}
}

func TestInstallPathHarnessAddsFakeTool(t *testing.T) {
	h := InstallPathHarness(t, PathOptions{
		FakeTools: map[string]string{
			"just": "#!/bin/sh\necho fake-just \"$@\"\n",
		},
	})
	cmd := exec.Command(filepath.Join(h.BinDir, "just"), "smoke")
	cmd.Env = append(os.Environ(), "PATH="+h.PATH)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("just: %v: %s", err, out)
	}
	if string(out) != "fake-just smoke\n" {
		t.Fatalf("just output = %q", out)
	}
}
