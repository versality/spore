package testagent

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
)

type PathOptions struct {
	IncludeClaude bool
	IncludeCodex  bool
	RealTools     []string
	FakeTools     map[string]string
}

type PathHarness struct {
	BinDir string
	PATH   string
}

func InstallPathHarness(t testing.TB, opts PathOptions) PathHarness {
	t.Helper()
	binDir := filepath.Join(t.TempDir(), "bin")
	if err := os.MkdirAll(binDir, 0o755); err != nil {
		t.Fatal(err)
	}
	fakeAgent := filepath.Join(binDir, "fake-agent")
	cmd := exec.Command("go", "build", "-o", fakeAgent, "./internal/testagent/cmd/fake-agent")
	cmd.Dir = repoRoot(t)
	cmd.Env = append(os.Environ(), "GOCACHE="+filepath.Join(t.TempDir(), "gocache"))
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("build fake agent: %v: %s", err, out)
	}
	if opts.IncludeClaude {
		writeWrapper(t, filepath.Join(binDir, "claude"), fakeAgent, "claude")
	}
	if opts.IncludeCodex {
		writeWrapper(t, filepath.Join(binDir, "codex"), fakeAgent, "codex")
	}
	for _, tool := range opts.RealTools {
		path, err := exec.LookPath(tool)
		if err != nil {
			t.Fatalf("look path %s: %v", tool, err)
		}
		writeExecScript(t, filepath.Join(binDir, tool), fmt.Sprintf("#!/bin/sh\nexec %q \"$@\"\n", path))
	}
	for name, body := range opts.FakeTools {
		writeExecScript(t, filepath.Join(binDir, name), body)
	}
	path := binDir + string(os.PathListSeparator) + os.Getenv("PATH")
	return PathHarness{BinDir: binDir, PATH: path}
}

func writeWrapper(t testing.TB, path, fakeAgent, provider string) {
	t.Helper()
	writeExecScript(t, path, fmt.Sprintf("#!/bin/sh\nSPORE_FAKE_AGENT_PROVIDER=%s exec %q \"$@\"\n", provider, fakeAgent))
}

func writeExecScript(t testing.TB, path, body string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
}

func repoRoot(t testing.TB) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime caller failed")
	}
	return filepath.Clean(filepath.Join(filepath.Dir(file), "..", ".."))
}
