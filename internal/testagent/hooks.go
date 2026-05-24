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
	Events map[string][]hookCommand `json:"events"`
}

type hookCommand struct {
	Command string   `json:"command"`
	Timeout int      `json:"timeout"`
	Kinds   []string `json:"kinds"`
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
	for _, hook := range cfg.Events[event] {
		if hook.Command == "" {
			_ = rec.event(Event{Type: "hook-error", Provider: "codex", Fields: map[string]string{"event": event}, Error: "empty hook command"})
			continue
		}
		timeout := time.Duration(hook.Timeout) * time.Second
		if timeout <= 0 {
			timeout = 10 * time.Second
		}
		hookCtx, cancel := context.WithTimeout(ctx, timeout)
		cmd := exec.CommandContext(hookCtx, "sh", "-c", hook.Command)
		cmd.Stdin = bytes.NewBufferString(codexHookPayload(event))
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
			_ = rec.event(Event{Type: "hook-error", Provider: "codex", Fields: fields, Error: fmt.Sprintf("%v", err)})
			continue
		}
		_ = rec.event(Event{Type: "hook", Provider: "codex", Fields: fields})
	}
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
