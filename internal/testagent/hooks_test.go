package testagent

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"testing"
)

func TestRunCodexExecutesRuntimeHooks(t *testing.T) {
	dir := t.TempDir()
	trustCodexProject(t, dir)
	hooksDir := filepath.Join(dir, ".codex")
	if err := os.MkdirAll(hooksDir, 0o755); err != nil {
		t.Fatal(err)
	}
	outPath := filepath.Join(dir, "hooks.out")
	hooks := `{"hooks":{"SessionStart":[{"hooks":[{"command":"printf session >> ` + outPath + `","timeout":10}]}],"PreToolUse":[{"hooks":[{"command":"printf pre >> ` + outPath + `","timeout":10}]}],"Stop":[{"hooks":[{"command":"printf stop >> ` + outPath + `","timeout":10}]}]}}`
	if err := os.WriteFile(filepath.Join(hooksDir, "hooks.json"), []byte(hooks), 0o644); err != nil {
		t.Fatal(err)
	}
	oldwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := os.Chdir(oldwd); err != nil {
			t.Fatal(err)
		}
	})
	logPath := filepath.Join(dir, "events.jsonl")
	t.Setenv(EnvMode, ModeOneTurn)
	t.Setenv(EnvEventLog, logPath)

	code := Run(context.Background(), Options{Provider: "codex", Argv: []string{"codex", "--dangerously-bypass-hook-trust"}, Now: fixedNow})
	if code != 0 {
		t.Fatalf("Run exit = %d, want 0", code)
	}
	body, err := os.ReadFile(outPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(body) != "sessionprestop" {
		t.Fatalf("hook output = %q", body)
	}
	events := readEvents(t, logPath)
	if countEvents(events, "hook") != 3 {
		t.Fatalf("hook count = %d, want 3: %s", countEvents(events, "hook"), eventTypes(events))
	}
}

func TestRunCodexInvalidHooksRecordsParseError(t *testing.T) {
	dir := t.TempDir()
	trustCodexProject(t, dir)
	if err := os.MkdirAll(filepath.Join(dir, ".codex"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, ".codex", "hooks.json"), []byte("{"), 0o644); err != nil {
		t.Fatal(err)
	}
	oldwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := os.Chdir(oldwd); err != nil {
			t.Fatal(err)
		}
	})
	logPath := filepath.Join(dir, "events.jsonl")
	t.Setenv(EnvMode, ModeOneTurn)
	t.Setenv(EnvEventLog, logPath)

	code := Run(context.Background(), Options{Provider: "codex", Argv: []string{"codex", "--dangerously-bypass-hook-trust"}, Now: fixedNow})
	if code != 0 {
		t.Fatalf("Run exit = %d, want 0", code)
	}
	if findEvent(readEvents(t, logPath), "hook-parse-error") == nil {
		t.Fatal("hook-parse-error event missing")
	}
}

func TestRunCodexHookReceivesDocumentedPayload(t *testing.T) {
	dir := t.TempDir()
	trustCodexProject(t, dir)
	hooksDir := filepath.Join(dir, ".codex")
	if err := os.MkdirAll(hooksDir, 0o755); err != nil {
		t.Fatal(err)
	}
	outPath := filepath.Join(dir, "payload.out")
	hooks := `{"hooks":{"Stop":[{"hooks":[{"command":"cat > ` + outPath + `","timeout":10}]}]}}`
	if err := os.WriteFile(filepath.Join(hooksDir, "hooks.json"), []byte(hooks), 0o644); err != nil {
		t.Fatal(err)
	}
	oldwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := os.Chdir(oldwd); err != nil {
			t.Fatal(err)
		}
	})
	t.Setenv(EnvMode, ModeOneTurn)
	t.Setenv(EnvEventLog, filepath.Join(dir, "events.jsonl"))

	code := Run(context.Background(), Options{Provider: "codex", Argv: []string{"codex", "--dangerously-bypass-hook-trust"}, Now: fixedNow})
	if code != 0 {
		t.Fatalf("Run exit = %d, want 0", code)
	}
	var payload map[string]any
	raw, err := os.ReadFile(outPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(raw, &payload); err != nil {
		t.Fatalf("payload json: %v: %s", err, raw)
	}
	assertPayloadString(t, payload, "hook_event_name", "Stop")
	assertPayloadString(t, payload, "session_id", "testagent-session")
	assertPayloadString(t, payload, "cwd", dir)
	assertPayloadString(t, payload, "permission_mode", "default")
	if payload["transcript_path"] == "" {
		t.Fatalf("payload transcript_path missing: %#v", payload)
	}
	if payload["stop_hook_active"] != false {
		t.Fatalf("payload stop_hook_active = %#v, want false", payload["stop_hook_active"])
	}
}

func TestRunCodexLinkedWorktreeReadsRootHooks(t *testing.T) {
	root := t.TempDir()
	worktree := filepath.Join(root, ".worktrees", "demo")
	runGitForHooks(t, root, "init", "-q", "-b", "main")
	if err := os.WriteFile(filepath.Join(root, "file.txt"), []byte("root\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runGitForHooks(t, root, "add", "file.txt")
	runGitForHooks(t, root, "-c", "user.email=t@example.com", "-c", "user.name=t", "commit", "-q", "-m", "init")
	runGitForHooks(t, root, "worktree", "add", "-q", "-b", "wt/demo", worktree)
	trustCodexProject(t, root)

	if err := os.MkdirAll(filepath.Join(root, ".codex"), 0o755); err != nil {
		t.Fatal(err)
	}
	outPath := filepath.Join(root, "hooks.out")
	hooks := `{"hooks":{"Stop":[{"hooks":[{"command":"printf root >> ` + outPath + `","timeout":10}]}]}}`
	if err := os.WriteFile(filepath.Join(root, ".codex", "hooks.json"), []byte(hooks), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(worktree, ".codex"), 0o755); err != nil {
		t.Fatal(err)
	}
	ignoredPath := filepath.Join(root, "ignored.out")
	ignored := `{"hooks":{"Stop":[{"hooks":[{"command":"printf ignored >> ` + ignoredPath + `","timeout":10}]}]}}`
	if err := os.WriteFile(filepath.Join(worktree, ".codex", "hooks.json"), []byte(ignored), 0o644); err != nil {
		t.Fatal(err)
	}
	withWorkingDir(t, worktree)
	t.Setenv(EnvMode, ModeOneTurn)
	t.Setenv(EnvEventLog, filepath.Join(root, "events.jsonl"))

	code := Run(context.Background(), Options{Provider: "codex", Argv: []string{"codex", "--dangerously-bypass-hook-trust"}, Now: fixedNow})
	if code != 0 {
		t.Fatalf("Run exit = %d, want 0", code)
	}
	body, err := os.ReadFile(outPath)
	if err != nil {
		t.Fatal(err)
	}
	if got := string(body); got != "root" {
		t.Fatalf("root hook output = %q", got)
	}
	if _, err := os.Stat(ignoredPath); !os.IsNotExist(err) {
		t.Fatalf("worktree hook ran or stat failed: %v", err)
	}
}

func TestRunCodexUntrustedProjectDoesNotDiscoverHooks(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, ".codex"), 0o755); err != nil {
		t.Fatal(err)
	}
	outPath := filepath.Join(dir, "hooks.out")
	hooks := `{"hooks":{"Stop":[{"hooks":[{"command":"printf stop >> ` + outPath + `","timeout":10}]}]}}`
	if err := os.WriteFile(filepath.Join(dir, ".codex", "hooks.json"), []byte(hooks), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("CODEX_HOME", t.TempDir())
	withWorkingDir(t, dir)
	t.Setenv(EnvMode, ModeOneTurn)
	t.Setenv(EnvEventLog, filepath.Join(dir, "events.jsonl"))

	code := Run(context.Background(), Options{Provider: "codex", Argv: []string{"codex", "--dangerously-bypass-hook-trust"}, Now: fixedNow})
	if code != 0 {
		t.Fatalf("Run exit = %d, want 0", code)
	}
	if _, err := os.Stat(outPath); !os.IsNotExist(err) {
		t.Fatalf("untrusted hook ran or stat failed: %v", err)
	}
}

func TestRunCodexRequiresHookTrustWithoutBypass(t *testing.T) {
	dir := t.TempDir()
	trustCodexProject(t, dir)
	if err := os.MkdirAll(filepath.Join(dir, ".codex"), 0o755); err != nil {
		t.Fatal(err)
	}
	outPath := filepath.Join(dir, "hooks.out")
	hooks := `{"hooks":{"Stop":[{"hooks":[{"command":"printf stop >> ` + outPath + `","timeout":10}]}]}}`
	if err := os.WriteFile(filepath.Join(dir, ".codex", "hooks.json"), []byte(hooks), 0o644); err != nil {
		t.Fatal(err)
	}
	withWorkingDir(t, dir)
	t.Setenv(EnvMode, ModeOneTurn)
	t.Setenv(EnvEventLog, filepath.Join(dir, "events.jsonl"))

	code := Run(context.Background(), Options{Provider: "codex", Argv: []string{"codex"}, Now: fixedNow})
	if code != 0 {
		t.Fatalf("Run exit = %d, want 0", code)
	}
	if _, err := os.Stat(outPath); !os.IsNotExist(err) {
		t.Fatalf("untrusted hook command ran or stat failed: %v", err)
	}
}

func assertPayloadString(t *testing.T, payload map[string]any, key, want string) {
	t.Helper()
	if got, _ := payload[key].(string); got != want {
		t.Fatalf("payload %s = %q, want %q: %#v", key, got, want, payload)
	}
}

func trustCodexProject(t *testing.T, root string) {
	t.Helper()
	codexHome := t.TempDir()
	t.Setenv("CODEX_HOME", codexHome)
	if err := os.WriteFile(filepath.Join(codexHome, "config.toml"), []byte("[projects."+strconv.Quote(filepath.Clean(root))+"]\ntrust_level = \"trusted\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
}

func withWorkingDir(t *testing.T, dir string) {
	t.Helper()
	oldwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := os.Chdir(oldwd); err != nil {
			t.Fatal(err)
		}
	})
}

func runGitForHooks(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}

func TestRunClaudeExecutesStopHooks(t *testing.T) {
	dir := t.TempDir()
	settingsDir := filepath.Join(dir, ".claude")
	if err := os.MkdirAll(settingsDir, 0o755); err != nil {
		t.Fatal(err)
	}
	outPath := filepath.Join(dir, "hooks.out")
	settings := `{"hooks":{"Stop":[{"hooks":[{"type":"command","command":"printf stop >> ` + outPath + `","timeout":10}]}]}}`
	if err := os.WriteFile(filepath.Join(settingsDir, "settings.local.json"), []byte(settings), 0o644); err != nil {
		t.Fatal(err)
	}
	oldwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := os.Chdir(oldwd); err != nil {
			t.Fatal(err)
		}
	})
	logPath := filepath.Join(dir, "events.jsonl")
	t.Setenv(EnvMode, ModeOneTurn)
	t.Setenv(EnvEventLog, logPath)

	code := Run(context.Background(), Options{Provider: "claude", Now: fixedNow})
	if code != 0 {
		t.Fatalf("Run exit = %d, want 0", code)
	}
	body, err := os.ReadFile(outPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(body) != "stop" {
		t.Fatalf("hook output = %q", body)
	}
	if countEvents(readEvents(t, logPath), "hook") != 1 {
		t.Fatal("hook event missing")
	}
}

func TestRunClaudeHookReceivesClaudePayload(t *testing.T) {
	dir := t.TempDir()
	settingsDir := filepath.Join(dir, ".claude")
	if err := os.MkdirAll(settingsDir, 0o755); err != nil {
		t.Fatal(err)
	}
	outPath := filepath.Join(dir, "payload.out")
	settings := `{"hooks":{"Stop":[{"hooks":[{"type":"command","command":"cat > ` + outPath + `","timeout":10}]}]}}`
	if err := os.WriteFile(filepath.Join(settingsDir, "settings.local.json"), []byte(settings), 0o644); err != nil {
		t.Fatal(err)
	}
	oldwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := os.Chdir(oldwd); err != nil {
			t.Fatal(err)
		}
	})
	logPath := filepath.Join(dir, "events.jsonl")
	t.Setenv(EnvMode, ModeOneTurn)
	t.Setenv(EnvEventLog, logPath)

	code := Run(context.Background(), Options{Provider: "claude", Now: fixedNow})
	if code != 0 {
		t.Fatalf("Run exit = %d, want 0", code)
	}
	body, err := os.ReadFile(outPath)
	if err != nil {
		t.Fatal(err)
	}
	want := `{"hook_event_name":"Stop","stop_hook_active":false}` + "\n"
	if string(body) != want {
		t.Fatalf("payload = %q, want %q", body, want)
	}
}

func TestRunClaudeInvalidSettingsRecordsParseError(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, ".claude"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, ".claude", "settings.local.json"), []byte("{"), 0o644); err != nil {
		t.Fatal(err)
	}
	oldwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := os.Chdir(oldwd); err != nil {
			t.Fatal(err)
		}
	})
	logPath := filepath.Join(dir, "events.jsonl")
	t.Setenv(EnvMode, ModeOneTurn)
	t.Setenv(EnvEventLog, logPath)

	code := Run(context.Background(), Options{Provider: "claude", Now: fixedNow})
	if code != 0 {
		t.Fatalf("Run exit = %d, want 0", code)
	}
	if findEvent(readEvents(t, logPath), "hook-parse-error") == nil {
		t.Fatal("hook-parse-error event missing")
	}
}

func countEvents(events []Event, typ string) int {
	var n int
	for _, event := range events {
		if event.Type == typ {
			n++
		}
	}
	return n
}
