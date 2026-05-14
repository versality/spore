package codex

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestPreToolUse_StuckPriorRefused(t *testing.T) {
	dir := t.TempDir()
	stateDir := filepath.Join(dir, "coord")
	os.MkdirAll(stateDir, 0o700)
	tpath := writeCodexJSONL(t, dir, []string{
		`{"type":"session_meta","payload":{"id":"sess-pre"}}`,
		`{"type":"response_item","payload":{"type":"function_call","name":"exec","call_id":"call_X"}}`,
	})
	cfg := PreToolUseConfig{
		Inbox:               stateDir,
		CoordinatorStateDir: stateDir,
		Driver:              "codex",
	}
	payload := fmt.Sprintf(`{"session_id":"sess-pre","transcript_path":"%s","tool_name":"exec_command"}`, tpath)
	res := PreToolUse(cfg, strings.NewReader(payload))
	if res.ExitCode != 2 {
		t.Fatalf("exit = %d, want 2", res.ExitCode)
	}
	for _, want := range []string{
		"codex-stuck-toolcall-prior",
		"refusing exec_command",
		"call_X",
		"function_call exec",
	} {
		if !strings.Contains(res.Stderr, want) {
			t.Errorf("stderr missing %q: %s", want, res.Stderr)
		}
	}
}

func TestPreToolUse_CleanTranscriptAllowed(t *testing.T) {
	dir := t.TempDir()
	stateDir := filepath.Join(dir, "coord")
	os.MkdirAll(stateDir, 0o700)
	tpath := writeCodexJSONL(t, dir, []string{
		`{"type":"session_meta","payload":{"id":"sess-clean"}}`,
		`{"type":"response_item","payload":{"type":"function_call","name":"exec","call_id":"c1"}}`,
		`{"type":"response_item","payload":{"type":"function_call_output","call_id":"c1","output":"ok"}}`,
	})
	cfg := PreToolUseConfig{
		Inbox:               stateDir,
		CoordinatorStateDir: stateDir,
		Driver:              "codex",
	}
	payload := fmt.Sprintf(`{"session_id":"sess-clean","transcript_path":"%s","tool_name":"new"}`, tpath)
	res := PreToolUse(cfg, strings.NewReader(payload))
	if res.ExitCode != 0 {
		t.Fatalf("exit = %d, want 0: stderr=%q", res.ExitCode, res.Stderr)
	}
}

func TestPreToolUse_NotCoordinatorAllowed(t *testing.T) {
	dir := t.TempDir()
	stateDir := filepath.Join(dir, "coord")
	os.MkdirAll(stateDir, 0o700)
	tpath := writeCodexJSONL(t, dir, []string{
		`{"type":"response_item","payload":{"type":"function_call","name":"exec","call_id":"c1"}}`,
	})
	cfg := PreToolUseConfig{
		Inbox:               filepath.Join(dir, "elsewhere"),
		CoordinatorStateDir: stateDir,
		Driver:              "codex",
	}
	payload := fmt.Sprintf(`{"transcript_path":"%s","tool_name":"x"}`, tpath)
	res := PreToolUse(cfg, strings.NewReader(payload))
	if res.ExitCode != 0 {
		t.Fatalf("exit = %d, want 0 (non-coordinator skips)", res.ExitCode)
	}
}

func TestPreToolUse_ClaudeDriverAllowed(t *testing.T) {
	dir := t.TempDir()
	stateDir := filepath.Join(dir, "coord")
	os.MkdirAll(stateDir, 0o700)
	tpath := writeCodexJSONL(t, dir, []string{
		`{"type":"response_item","payload":{"type":"function_call","name":"exec","call_id":"c1"}}`,
	})
	cfg := PreToolUseConfig{
		Inbox:               stateDir,
		CoordinatorStateDir: stateDir,
		Driver:              "claude",
	}
	payload := fmt.Sprintf(`{"transcript_path":"%s","tool_name":"x"}`, tpath)
	res := PreToolUse(cfg, strings.NewReader(payload))
	if res.ExitCode != 0 {
		t.Fatalf("exit = %d, want 0 (non-codex driver skips)", res.ExitCode)
	}
}

func TestPreToolUse_MissingTranscriptPathAllowed(t *testing.T) {
	dir := t.TempDir()
	stateDir := filepath.Join(dir, "coord")
	os.MkdirAll(stateDir, 0o700)
	cfg := PreToolUseConfig{
		Inbox:               stateDir,
		CoordinatorStateDir: stateDir,
		Driver:              "codex",
	}
	res := PreToolUse(cfg, strings.NewReader(`{"tool_name":"x"}`))
	if res.ExitCode != 0 {
		t.Fatalf("exit = %d, want 0 (missing transcript path is best-effort allow)", res.ExitCode)
	}
}

func TestPreToolUse_MissingToolNameRenderedSafely(t *testing.T) {
	dir := t.TempDir()
	stateDir := filepath.Join(dir, "coord")
	os.MkdirAll(stateDir, 0o700)
	tpath := writeCodexJSONL(t, dir, []string{
		`{"type":"response_item","payload":{"type":"function_call","name":"exec","call_id":"c1"}}`,
	})
	cfg := PreToolUseConfig{
		Inbox:               stateDir,
		CoordinatorStateDir: stateDir,
		Driver:              "codex",
	}
	payload := fmt.Sprintf(`{"transcript_path":"%s"}`, tpath)
	res := PreToolUse(cfg, strings.NewReader(payload))
	if res.ExitCode != 2 {
		t.Fatalf("exit = %d, want 2", res.ExitCode)
	}
	if !strings.Contains(res.Stderr, "refusing <unknown>") {
		t.Errorf("stderr should render <unknown> for missing tool_name: %s", res.Stderr)
	}
}
