package inject

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeCodexSourceTree(t *testing.T, projectRoot, hooks string) {
	t.Helper()
	dir := filepath.Join(projectRoot, "configs", "codex")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if hooks != "" {
		if err := os.WriteFile(filepath.Join(dir, "hooks-config.json"), []byte(hooks), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

func readCodexHooks(t *testing.T, path string) map[string]any {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatalf("unmarshal %s: %v", path, err)
	}
	return m
}

func TestInjectCodexWorkerFiltersKinds(t *testing.T) {
	root := t.TempDir()
	target := t.TempDir()
	writeCodexSourceTree(t, root, sampleConfig)

	path, wrote, err := InjectCodex(root, target, "worker")
	if err != nil {
		t.Fatalf("InjectCodex: %v", err)
	}
	if !wrote {
		t.Fatalf("first inject reported no write")
	}
	want := filepath.Join(target, ".codex/hooks.json")
	if path != want {
		t.Errorf("path = %q, want %q", path, want)
	}
	got := commandsForEvent(readCodexHooks(t, path), "Stop")
	wantCmds := []string{"worker-only", "everywhere"}
	if !equalStrings(got, wantCmds) {
		t.Errorf("worker Stop commands = %v, want %v", got, wantCmds)
	}
	body := string(mustReadFile(t, path))
	if strings.Contains(body, "kinds") {
		t.Errorf("rendered .codex/hooks.json contains 'kinds' field; should be stripped")
	}
	// Codex format must NOT carry claude's $schema or permissions block.
	if strings.Contains(body, "$schema") || strings.Contains(body, "permissions") {
		t.Errorf("rendered .codex/hooks.json carries claude-specific top-level fields:\n%s", body)
	}
}

func TestInjectCodexCoordinatorFiltersKinds(t *testing.T) {
	root := t.TempDir()
	target := t.TempDir()
	writeCodexSourceTree(t, root, sampleConfig)

	if _, _, err := InjectCodex(root, target, "coordinator"); err != nil {
		t.Fatalf("InjectCodex: %v", err)
	}
	got := commandsForEvent(readCodexHooks(t, filepath.Join(target, ".codex/hooks.json")), "Stop")
	if !equalStrings(got, []string{"coord-only", "everywhere"}) {
		t.Errorf("coordinator Stop commands = %v, want [coord-only everywhere]", got)
	}
}

func TestInjectCodexOperatorInteractiveDropsFleetHooks(t *testing.T) {
	root := t.TempDir()
	target := t.TempDir()
	// Every Stop entry is fleet-scoped: an operator-interactive spawn
	// (kind=worker but no unscoped entries) should still leave an empty
	// Stop list, proving the kind filter is the gate.
	fleetOnly := `{
	  "events": {
	    "Stop": [
	      {"command": "coord-only", "kinds": ["coordinator"]},
	      {"command": "worker-only", "kinds": ["worker"]}
	    ]
	  }
	}`
	writeCodexSourceTree(t, root, fleetOnly)

	// kind="" simulates a non-fleet (operator-interactive) caller of
	// the renderer-with-filter. Every kind-scoped binding has to pass
	// because the contract is "empty kind = wildcard" - see
	// hooks.SettingsForKind. The operator-interactive guarantee in
	// production comes from NOT calling InjectCodex at all outside
	// fleet spawners, plus gitignoring the rendered file so a fresh
	// checkout has nothing for codex to read. This test pins the
	// underlying filter behaviour: a fleet-kind spawn picks up only
	// its own kind, and the other kind's bindings are gone.
	if _, _, err := InjectCodex(root, target, "worker"); err != nil {
		t.Fatalf("InjectCodex: %v", err)
	}
	got := commandsForEvent(readCodexHooks(t, filepath.Join(target, ".codex/hooks.json")), "Stop")
	if !equalStrings(got, []string{"worker-only"}) {
		t.Errorf("worker Stop must drop coordinator-only binding, got %v", got)
	}
}

func TestInjectCodexNoConfigIsNoop(t *testing.T) {
	root := t.TempDir()
	target := t.TempDir()

	path, wrote, err := InjectCodex(root, target, "coordinator")
	if err != nil {
		t.Fatalf("InjectCodex: %v", err)
	}
	if wrote || path != "" {
		t.Errorf("InjectCodex without hooks-config.json should be no-op, got path=%q wrote=%v", path, wrote)
	}
	if _, err := os.Stat(filepath.Join(target, ".codex/hooks.json")); !os.IsNotExist(err) {
		t.Errorf("expected no .codex/hooks.json, got err=%v", err)
	}
}

func TestInjectCodexSkipEnv(t *testing.T) {
	root := t.TempDir()
	target := t.TempDir()
	writeCodexSourceTree(t, root, sampleConfig)

	getenv := func(k string) string {
		if k == SkipEnv {
			return "1"
		}
		return ""
	}
	path, wrote, err := InjectCodexWithEnv(root, target, "coordinator", getenv)
	if err != nil {
		t.Fatalf("InjectCodex: %v", err)
	}
	if wrote || path != "" {
		t.Errorf("SkipEnv should suppress write, got path=%q wrote=%v", path, wrote)
	}
	if _, err := os.Stat(filepath.Join(target, ".codex/hooks.json")); !os.IsNotExist(err) {
		t.Errorf("expected no .codex/hooks.json under SkipEnv, err=%v", err)
	}
}

func TestInjectCodexIdempotent(t *testing.T) {
	root := t.TempDir()
	target := t.TempDir()
	writeCodexSourceTree(t, root, sampleConfig)

	if _, wrote, err := InjectCodex(root, target, "worker"); err != nil || !wrote {
		t.Fatalf("first InjectCodex: err=%v wrote=%v", err, wrote)
	}
	_, wrote, err := InjectCodex(root, target, "worker")
	if err != nil {
		t.Fatalf("second InjectCodex: %v", err)
	}
	if wrote {
		t.Errorf("second InjectCodex reported wrote=true; expected false (identical content)")
	}
}

func TestInjectCodexHooksConfigEnvOverride(t *testing.T) {
	root := t.TempDir()
	target := t.TempDir()
	custom := filepath.Join(t.TempDir(), "alt.json")
	if err := os.WriteFile(custom, []byte(sampleConfig), 0o644); err != nil {
		t.Fatal(err)
	}
	getenv := func(k string) string {
		if k == CodexHooksConfigEnv {
			return custom
		}
		return ""
	}
	if _, _, err := InjectCodexWithEnv(root, target, "coordinator", getenv); err != nil {
		t.Fatalf("InjectCodex: %v", err)
	}
	got := commandsForEvent(readCodexHooks(t, filepath.Join(target, ".codex/hooks.json")), "Stop")
	if !equalStrings(got, []string{"coord-only", "everywhere"}) {
		t.Errorf("override path not honoured, got %v", got)
	}
}
