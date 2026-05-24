package testpath

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestInstallDiscoversFakeTools(t *testing.T) {
	h := Install(t, Options{
		FakeTools: map[string]string{
			"codex":  "#!/bin/sh\necho codex\n",
			"claude": "#!/bin/sh\necho claude\n",
			"spore":  "#!/bin/sh\necho spore\n",
		},
	})
	h.Apply(t)
	for _, tool := range []string{"codex", "claude", "spore"} {
		path, err := exec.LookPath(tool)
		if err != nil {
			t.Fatalf("look path %s: %v", tool, err)
		}
		if filepath.Dir(path) != h.BinDir {
			t.Fatalf("%s path = %s, want under %s", tool, path, h.BinDir)
		}
	}
}

func TestInstallCanIntentionallyOmitTool(t *testing.T) {
	h := Install(t, Options{
		FakeTools: map[string]string{
			"git":  "#!/bin/sh\nexit 0\n",
			"tmux": "#!/bin/sh\nexit 0\n",
		},
	})
	t.Setenv("PATH", h.BinDir)
	if _, err := exec.LookPath("codex"); err == nil {
		t.Fatal("codex found, want intentionally missing")
	}
	if _, err := exec.LookPath("git"); err != nil {
		t.Fatalf("git missing: %v", err)
	}
}

func TestInstallCanIncludeRealTool(t *testing.T) {
	if _, err := exec.LookPath("sh"); err != nil {
		t.Skip("sh not on PATH")
	}
	h := Install(t, Options{RealTools: []string{"sh"}})
	t.Setenv("PATH", h.BinDir)
	cmd := exec.Command("sh", "-c", "printf ok")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("sh: %v: %s", err, out)
	}
	if string(out) != "ok" {
		t.Fatalf("out = %q", out)
	}
}

func TestHarnessPathStartsWithBinDir(t *testing.T) {
	h := Install(t, Options{})
	if got := os.Getenv("PATH"); got == h.PATH {
		t.Fatal("Install mutated PATH before Apply")
	}
	h.Apply(t)
	if got := os.Getenv("PATH"); got != h.PATH {
		t.Fatalf("PATH = %q, want %q", got, h.PATH)
	}
}
