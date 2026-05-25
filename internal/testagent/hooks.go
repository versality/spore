package testagent

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

type codexHooksFile struct {
	Hooks map[string][]claudeHookGroup `json:"hooks"`
}

type hookCommand struct {
	Command string   `json:"command"`
	Timeout int      `json:"timeout"`
	Kinds   []string `json:"kinds"`
}

type claudeSettingsFile struct {
	Hooks map[string][]claudeHookGroup `json:"hooks"`
}

type claudeHookGroup struct {
	Hooks []hookCommand `json:"hooks"`
}

func runCodexHooks(ctx context.Context, rec recorder, event string, argv []string) {
	root := codexHookRoot(cwd())
	path := filepath.Join(root, ".codex", "hooks.json")
	if !codexProjectTrusted(root) {
		_ = rec.event(Event{Type: "hook-warning", Provider: "codex", Fields: map[string]string{"event": event, "path": path, "root": root}, Error: "project not trusted"})
		return
	}
	body, err := os.ReadFile(path)
	if err != nil {
		_ = rec.event(Event{Type: "hook-warning", Provider: "codex", Fields: map[string]string{"event": event, "path": path}, Error: err.Error()})
		return
	}
	var cfg codexHooksFile
	if err := json.Unmarshal(body, &cfg); err != nil {
		_ = rec.event(Event{Type: "hook-parse-error", Provider: "codex", Fields: map[string]string{"event": event, "path": path}, Error: err.Error()})
		return
	}
	for _, group := range cfg.Hooks[event] {
		for _, hook := range group.Hooks {
			if !codexBypassHookTrust(argv) && os.Getenv("SPORE_FAKE_CODEX_TRUSTED_HOOKS") != "1" {
				_ = rec.event(Event{Type: "hook-warning", Provider: "codex", Fields: map[string]string{"event": event, "path": path, "command": hook.Command}, Error: "hook command not trusted"})
				continue
			}
			runHookCommand(ctx, rec, "codex", event, hook)
		}
	}
}

func codexHookRoot(wd string) string {
	gitPath := filepath.Join(wd, ".git")
	info, err := os.Stat(gitPath)
	if err == nil && info.IsDir() {
		return wd
	}
	body, err := os.ReadFile(gitPath)
	if err != nil {
		return wd
	}
	line := strings.TrimSpace(string(body))
	const prefix = "gitdir:"
	if !strings.HasPrefix(line, prefix) {
		return wd
	}
	gitDir := strings.TrimSpace(strings.TrimPrefix(line, prefix))
	if !filepath.IsAbs(gitDir) {
		gitDir = filepath.Clean(filepath.Join(wd, gitDir))
	}
	commonDir := filepath.Join(gitDir, "commondir")
	commonRaw, err := os.ReadFile(commonDir)
	if err != nil {
		return wd
	}
	common := strings.TrimSpace(string(commonRaw))
	if !filepath.IsAbs(common) {
		common = filepath.Clean(filepath.Join(gitDir, common))
	}
	if filepath.Base(common) == ".git" {
		return filepath.Dir(common)
	}
	return wd
}

func codexProjectTrusted(root string) bool {
	root = filepath.Clean(root)
	configPath := filepath.Join(codexHome(), "config.toml")
	body, err := os.ReadFile(configPath)
	if err != nil {
		return false
	}
	section := ""
	for _, raw := range strings.Split(string(body), "\n") {
		line := strings.TrimSpace(raw)
		if strings.HasPrefix(line, "[projects.") && strings.HasSuffix(line, "]") {
			section = strings.TrimSuffix(strings.TrimPrefix(line, "[projects."), "]")
			section = strings.Trim(section, `"`)
			continue
		}
		if section == root && strings.HasPrefix(line, "trust_level") && strings.Contains(line, `"trusted"`) {
			return true
		}
	}
	return false
}

func codexHome() string {
	if v := os.Getenv("CODEX_HOME"); v != "" {
		return v
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ".codex"
	}
	return filepath.Join(home, ".codex")
}

func codexBypassHookTrust(argv []string) bool {
	for _, arg := range argv {
		if arg == "--dangerously-bypass-hook-trust" {
			return true
		}
	}
	return false
}

func hookPayload(provider, event string) string {
	if provider == "claude" {
		return claudeHookPayload(event)
	}
	return codexHookPayload(event)
}

func codexHookPayload(event string) string {
	base := map[string]any{
		"hook_event_name": event,
		"session_id":      "testagent-session",
		"cwd":             cwd(),
		"transcript_path": filepath.Join(os.TempDir(), "testagent-transcript-testagent-session.jsonl"),
		"permission_mode": "default",
	}
	switch event {
	case "PreToolUse":
		base["tool_name"] = "Bash"
		base["tool_input"] = map[string]string{"command": "true"}
	case "SessionStart":
		base["source"] = "startup"
	default:
		base["stop_hook_active"] = false
		base["last_assistant_message"] = "done"
	}
	return jsonLine(base)
}

func claudeHookPayload(event string) string {
	switch event {
	case "PreToolUse":
		return `{"hook_event_name":"PreToolUse","tool_name":"Bash","tool_input":{"command":"true"}}` + "\n"
	case "SessionStart":
		return `{"hook_event_name":"SessionStart","source":"startup"}` + "\n"
	default:
		return `{"hook_event_name":"Stop","stop_hook_active":false}` + "\n"
	}
}

func jsonLine(v any) string {
	body, err := json.Marshal(v)
	if err != nil {
		return "{}\n"
	}
	return string(body) + "\n"
}

func runClaudeHooks(ctx context.Context, rec recorder, event string) {
	path := filepath.Join(cwd(), ".claude", "settings.local.json")
	body, err := os.ReadFile(path)
	if err != nil {
		_ = rec.event(Event{Type: "hook-warning", Provider: "claude", Fields: map[string]string{"event": event, "path": path}, Error: err.Error()})
		return
	}
	var cfg claudeSettingsFile
	if err := json.Unmarshal(body, &cfg); err != nil {
		_ = rec.event(Event{Type: "hook-parse-error", Provider: "claude", Fields: map[string]string{"event": event, "path": path}, Error: err.Error()})
		return
	}
	for _, group := range cfg.Hooks[event] {
		for _, hook := range group.Hooks {
			runHookCommand(ctx, rec, "claude", event, hook)
		}
	}
}

func runHookCommand(ctx context.Context, rec recorder, provider, event string, hook hookCommand) {
	if hook.Command == "" {
		_ = rec.event(Event{Type: "hook-error", Provider: provider, Fields: map[string]string{"event": event}, Error: "empty hook command"})
		return
	}
	timeout := time.Duration(hook.Timeout) * time.Second
	if timeout <= 0 {
		timeout = 10 * time.Second
	}
	hookCtx, cancel := context.WithTimeout(ctx, timeout)
	cmd := exec.CommandContext(hookCtx, "sh", "-c", hook.Command)
	cmd.Stdin = bytes.NewBufferString(hookPayload(provider, event))
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	cancel()
	fields := map[string]string{
		"event":    event,
		"command":  hook.Command,
		"stdout":   stdout.String(),
		"stderr":   stderr.String(),
		"timedOut": strconv.FormatBool(hookCtx.Err() == context.DeadlineExceeded),
	}
	if err != nil {
		_ = rec.event(Event{Type: "hook-error", Provider: provider, Fields: fields, Error: fmt.Sprintf("%v", err)})
		return
	}
	_ = rec.event(Event{Type: "hook", Provider: provider, Fields: fields})
}
