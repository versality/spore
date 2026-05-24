package testpath

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

type Harness struct {
	BinDir string
	PATH   string
}

type Options struct {
	RealTools []string
	FakeTools map[string]string
}

func Install(t testing.TB, opts Options) Harness {
	t.Helper()
	binDir := filepath.Join(t.TempDir(), "bin")
	if err := os.MkdirAll(binDir, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, tool := range opts.RealTools {
		path, err := exec.LookPath(tool)
		if err != nil {
			t.Fatalf("look path %s: %v", tool, err)
		}
		WriteExecutable(t, filepath.Join(binDir, tool), fmt.Sprintf("#!/bin/sh\nexec %q \"$@\"\n", path))
	}
	for name, body := range opts.FakeTools {
		WriteExecutable(t, filepath.Join(binDir, name), body)
	}
	return Harness{
		BinDir: binDir,
		PATH:   binDir + string(os.PathListSeparator) + os.Getenv("PATH"),
	}
}

func (h Harness) Apply(t testing.TB) {
	t.Helper()
	t.Setenv("PATH", h.PATH)
}

func WriteExecutable(t testing.TB, path, body string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
}
