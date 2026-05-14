package inject

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const sampleConfig = `{
  "events": {
    "Stop": [
      {"command": "coord-only", "kinds": ["coordinator"]},
      {"command": "worker-only", "kinds": ["worker"]},
      {"command": "everywhere"}
    ]
  }
}`

func writeSourceTree(t *testing.T, projectRoot, hooks, extras string) {
	t.Helper()
	dir := filepath.Join(projectRoot, "configs", "claude")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if hooks != "" {
		if err := os.WriteFile(filepath.Join(dir, "hooks-config.json"), []byte(hooks), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if extras != "" {
		if err := os.WriteFile(filepath.Join(dir, "settings-extras.json"), []byte(extras), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

func readSettings(t *testing.T, path string) map[string]any {
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

// commandsForEvent returns the flat list of hook commands for the
// named event in the rendered settings.json, in encounter order.
func commandsForEvent(m map[string]any, event string) []string {
	hooks, ok := m["hooks"].(map[string]any)
	if !ok {
		return nil
	}
	groups, ok := hooks[event].([]any)
	if !ok {
		return nil
	}
	var out []string
	for _, g := range groups {
		gm, ok := g.(map[string]any)
		if !ok {
			continue
		}
		entries, ok := gm["hooks"].([]any)
		if !ok {
			continue
		}
		for _, e := range entries {
			em, ok := e.(map[string]any)
			if !ok {
				continue
			}
			if cmd, ok := em["command"].(string); ok {
				out = append(out, cmd)
			}
		}
	}
	return out
}

func TestInjectCoordinatorFiltersKinds(t *testing.T) {
	root := t.TempDir()
	target := t.TempDir()
	writeSourceTree(t, root, sampleConfig, "")

	path, wrote, err := Inject(root, target, "coordinator")
	if err != nil {
		t.Fatalf("Inject: %v", err)
	}
	if !wrote {
		t.Fatalf("first inject reported no write")
	}
	want := filepath.Join(target, ".claude/settings.local.json")
	if path != want {
		t.Errorf("path = %q, want %q", path, want)
	}
	got := commandsForEvent(readSettings(t, path), "Stop")
	wantCmds := []string{"coord-only", "everywhere"}
	if !equalStrings(got, wantCmds) {
		t.Errorf("coordinator Stop commands = %v, want %v", got, wantCmds)
	}
}

func TestInjectWorkerFiltersKinds(t *testing.T) {
	root := t.TempDir()
	target := t.TempDir()
	writeSourceTree(t, root, sampleConfig, "")

	if _, _, err := Inject(root, target, "worker"); err != nil {
		t.Fatalf("Inject: %v", err)
	}
	settings := readSettings(t, filepath.Join(target, ".claude/settings.local.json"))
	got := commandsForEvent(settings, "Stop")
	wantCmds := []string{"worker-only", "everywhere"}
	if !equalStrings(got, wantCmds) {
		t.Errorf("worker Stop commands = %v, want %v", got, wantCmds)
	}
	// The kinds field must not leak into the rendered file.
	if strings.Contains(string(mustReadFile(t, filepath.Join(target, ".claude/settings.local.json"))), "kinds") {
		t.Errorf("rendered settings.local.json contains 'kinds' field; should be stripped")
	}
}

func TestInjectNoConfigIsNoop(t *testing.T) {
	root := t.TempDir()
	target := t.TempDir()

	path, wrote, err := Inject(root, target, "coordinator")
	if err != nil {
		t.Fatalf("Inject: %v", err)
	}
	if wrote || path != "" {
		t.Errorf("Inject without hooks-config.json should be no-op, got path=%q wrote=%v", path, wrote)
	}
	if _, err := os.Stat(filepath.Join(target, ".claude/settings.local.json")); !os.IsNotExist(err) {
		t.Errorf("expected no .claude/settings.local.json, got err=%v", err)
	}
}

func TestInjectSkipEnv(t *testing.T) {
	root := t.TempDir()
	target := t.TempDir()
	writeSourceTree(t, root, sampleConfig, "")

	getenv := func(k string) string {
		if k == SkipEnv {
			return "1"
		}
		return ""
	}
	path, wrote, err := InjectWithEnv(root, target, "coordinator", getenv)
	if err != nil {
		t.Fatalf("Inject: %v", err)
	}
	if wrote || path != "" {
		t.Errorf("SkipEnv should suppress write, got path=%q wrote=%v", path, wrote)
	}
	if _, err := os.Stat(filepath.Join(target, ".claude/settings.local.json")); !os.IsNotExist(err) {
		t.Errorf("expected no settings.local.json under SkipEnv, err=%v", err)
	}
}

func TestInjectIdempotent(t *testing.T) {
	root := t.TempDir()
	target := t.TempDir()
	writeSourceTree(t, root, sampleConfig, "")

	if _, wrote, err := Inject(root, target, "worker"); err != nil || !wrote {
		t.Fatalf("first Inject: err=%v wrote=%v", err, wrote)
	}
	_, wrote, err := Inject(root, target, "worker")
	if err != nil {
		t.Fatalf("second Inject: %v", err)
	}
	if wrote {
		t.Errorf("second Inject reported wrote=true; expected false (identical content)")
	}
}

func TestInjectMergesExtras(t *testing.T) {
	root := t.TempDir()
	target := t.TempDir()
	extras := `{"statusLine": {"type": "command", "command": "echo hi"}}`
	writeSourceTree(t, root, sampleConfig, extras)

	if _, _, err := Inject(root, target, "worker"); err != nil {
		t.Fatalf("Inject: %v", err)
	}
	settings := readSettings(t, filepath.Join(target, ".claude/settings.local.json"))
	sl, ok := settings["statusLine"].(map[string]any)
	if !ok {
		t.Fatalf("statusLine missing from merged settings: %v", settings)
	}
	if sl["command"] != "echo hi" {
		t.Errorf("statusLine.command = %v, want %q", sl["command"], "echo hi")
	}
	if cmds := commandsForEvent(settings, "Stop"); !equalStrings(cmds, []string{"worker-only", "everywhere"}) {
		t.Errorf("Stop commands lost during extras merge: %v", cmds)
	}
}

func TestInjectHooksConfigEnvOverride(t *testing.T) {
	root := t.TempDir()
	target := t.TempDir()
	custom := filepath.Join(t.TempDir(), "alt.json")
	if err := os.WriteFile(custom, []byte(sampleConfig), 0o644); err != nil {
		t.Fatal(err)
	}

	getenv := func(k string) string {
		if k == HooksConfigEnv {
			return custom
		}
		return ""
	}
	if _, _, err := InjectWithEnv(root, target, "coordinator", getenv); err != nil {
		t.Fatalf("Inject: %v", err)
	}
	got := commandsForEvent(readSettings(t, filepath.Join(target, ".claude/settings.local.json")), "Stop")
	if !equalStrings(got, []string{"coord-only", "everywhere"}) {
		t.Errorf("override path not honoured, got %v", got)
	}
}

func mustReadFile(t *testing.T, path string) []byte {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
