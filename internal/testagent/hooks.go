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

func runCodexHooks(ctx context.Context, rec recorder, event string) {
	path := filepath.Join(cwd(), ".codex", "hooks.json")
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
			runHookCommand(ctx, rec, "codex", event, hook)
		}
	}
}

func hookPayload(provider, event string) string {
	if provider == "claude" {
		return claudeHookPayload(event)
	}
	return codexHookPayload(event)
}

func codexHookPayload(event string) string {
	switch event {
	case "PreToolUse":
		return `{"tool_name":"Bash","tool_input":{"command":"true"}}` + "\n"
	case "SessionStart":
		return `{"source":"startup"}` + "\n"
	default:
		return `{"stop_hook_active":false}` + "\n"
	}
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
