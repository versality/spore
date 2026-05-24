package testagent

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
)

func runCompletionMode(ctx context.Context, rec recorder, provider, mode string) int {
	touch(os.Getenv(EnvReadyFile))
	_ = rec.event(Event{Type: "ready", Provider: provider, Mode: mode})
	switch mode {
	case ModeEvidence:
		return writeEvidence(rec, provider, mode)
	case ModeCommitChange:
		if code := writeEvidence(rec, provider, mode); code != 0 {
			return code
		}
		return commitChange(ctx, rec, provider, mode)
	case ModeRequestMerge:
		if code := writeEvidence(rec, provider, mode); code != 0 {
			return code
		}
		if code := commitChange(ctx, rec, provider, mode); code != 0 {
			return code
		}
		return writeMarker(rec, provider, mode, ".wt/request-operator-merge")
	case ModeSelfDone:
		if code := writeEvidence(rec, provider, mode); code != 0 {
			return code
		}
		return runCommand(ctx, rec, provider, mode, "self-done", "spore task done")
	default:
		return 2
	}
}

func writeEvidence(rec recorder, provider, mode string) int {
	if err := os.MkdirAll(".wt", 0o755); err != nil {
		_ = rec.event(Event{Type: "evidence-error", Provider: provider, Mode: mode, Error: err.Error()})
		return 2
	}
	path := filepath.Join(".wt", "fake-agent-evidence.md")
	body := []byte("fake agent evidence\n")
	if err := os.WriteFile(path, body, 0o644); err != nil {
		_ = rec.event(Event{Type: "evidence-error", Provider: provider, Mode: mode, Error: err.Error(), Fields: map[string]string{"path": path}})
		return 2
	}
	_ = rec.event(Event{Type: "evidence", Provider: provider, Mode: mode, Fields: map[string]string{"path": path}})
	return 0
}

func commitChange(ctx context.Context, rec recorder, provider, mode string) int {
	if err := os.WriteFile("fake-agent-change.txt", []byte("fake agent change\n"), 0o644); err != nil {
		_ = rec.event(Event{Type: "commit-error", Provider: provider, Mode: mode, Error: err.Error()})
		return 2
	}
	if code := runCommand(ctx, rec, provider, mode, "git-add", "git add fake-agent-change.txt .wt/fake-agent-evidence.md"); code != 0 {
		return code
	}
	return runCommand(ctx, rec, provider, mode, "git-commit", "git commit -m 'testagent: fake worker change'")
}

func writeMarker(rec recorder, provider, mode, path string) int {
	if err := os.WriteFile(path, []byte("ready for operator merge\n"), 0o644); err != nil {
		_ = rec.event(Event{Type: "completion-error", Provider: provider, Mode: mode, Error: err.Error(), Fields: map[string]string{"path": path}})
		return 2
	}
	_ = rec.event(Event{Type: "request-operator-merge", Provider: provider, Mode: mode, Fields: map[string]string{"path": path}})
	return 0
}

func runCommand(ctx context.Context, rec recorder, provider, mode, typ, command string) int {
	cmd := exec.CommandContext(ctx, "sh", "-c", command)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	fields := map[string]string{
		"command": command,
		"stdout":  stdout.String(),
		"stderr":  stderr.String(),
	}
	if err != nil {
		_ = rec.event(Event{Type: typ + "-error", Provider: provider, Mode: mode, Error: err.Error(), Fields: fields})
		return 2
	}
	_ = rec.event(Event{Type: typ, Provider: provider, Mode: mode, Fields: fields})
	return 0
}
