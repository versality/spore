package codex

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestStop_StuckToolcall_FiresExit2(t *testing.T) {
	dir := t.TempDir()
	stateDir := filepath.Join(dir, "coord")
	os.MkdirAll(stateDir, 0o700)
	tpath := writeCodexJSONL(t, dir, []string{
		`{"type":"session_meta","payload":{"id":"sess-stuck"}}`,
		`{"type":"response_item","payload":{"type":"function_call","name":"exec_command","call_id":"call_A"}}`,
		`{"type":"token_count","last_token_usage":{"total_tokens":1000}}`,
	})
	cfg := StopConfig{
		Inbox:               stateDir,
		CoordinatorStateDir: stateDir,
		Driver:              "codex",
		Now:                 fixedNow("2026-05-14T12:00:00Z"),
	}
	payload := fmt.Sprintf(`{"session_id":"sess-stuck","transcript_path":"%s"}`, tpath)
	res := Stop(cfg, strings.NewReader(payload))
	if res.ExitCode != 2 {
		t.Fatalf("exit = %d, want 2", res.ExitCode)
	}
	if !strings.Contains(res.Stderr, "STUCK TOOLCALL") {
		t.Errorf("stderr missing stuck banner: %q", res.Stderr)
	}
	if !strings.Contains(res.Stderr, "call_A") {
		t.Errorf("stderr missing call_id: %q", res.Stderr)
	}
	if !strings.Contains(res.Stderr, "exec_command") {
		t.Errorf("stderr missing tool name: %q", res.Stderr)
	}

	body, err := os.ReadFile(filepath.Join(stateDir, "codex-stuck-toolcalls.jsonl"))
	if err != nil {
		t.Fatalf("ledger read: %v", err)
	}
	s := string(body)
	for _, want := range []string{
		`"event":"stuck-toolcall"`,
		`"session_id":"sess-stuck"`,
		`"call_id":"call_A"`,
		`"tool_name":"exec_command"`,
		`"kind":"function_call"`,
	} {
		if !strings.Contains(s, want) {
			t.Errorf("ledger missing %s: %s", want, s)
		}
	}
}

func TestStop_StuckToolcall_CleanTranscript_NoFire(t *testing.T) {
	dir := t.TempDir()
	stateDir := filepath.Join(dir, "coord")
	os.MkdirAll(stateDir, 0o700)
	tpath := writeCodexJSONL(t, dir, []string{
		`{"type":"session_meta","payload":{"id":"sess-clean"}}`,
		`{"type":"response_item","payload":{"type":"function_call","name":"exec","call_id":"c1"}}`,
		`{"type":"response_item","payload":{"type":"function_call_output","call_id":"c1","output":"ok"}}`,
		`{"type":"token_count","last_token_usage":{"total_tokens":900}}`,
	})
	cfg := StopConfig{
		Inbox:               stateDir,
		CoordinatorStateDir: stateDir,
		Driver:              "codex",
		Now:                 fixedNow("2026-05-14T12:00:00Z"),
	}
	payload := fmt.Sprintf(`{"session_id":"sess-clean","transcript_path":"%s"}`, tpath)
	res := Stop(cfg, strings.NewReader(payload))
	if res.ExitCode != 0 {
		t.Fatalf("exit = %d, want 0: stderr=%q", res.ExitCode, res.Stderr)
	}
	if _, err := os.Stat(filepath.Join(stateDir, "codex-stuck-toolcalls.jsonl")); err == nil {
		t.Errorf("stuck-toolcalls ledger should not exist for clean transcript")
	}
}

func TestStop_StuckToolcall_NotCoordinator_NoFire(t *testing.T) {
	dir := t.TempDir()
	stateDir := filepath.Join(dir, "coord")
	os.MkdirAll(stateDir, 0o700)
	tpath := writeCodexJSONL(t, dir, []string{
		`{"type":"session_meta","payload":{"id":"sess-x"}}`,
		`{"type":"response_item","payload":{"type":"function_call","name":"exec","call_id":"c1"}}`,
	})
	cfg := StopConfig{
		Inbox:               filepath.Join(dir, "elsewhere"),
		CoordinatorStateDir: stateDir,
		Driver:              "codex",
		Now:                 fixedNow("2026-05-14T12:00:00Z"),
	}
	payload := fmt.Sprintf(`{"session_id":"sess-x","transcript_path":"%s"}`, tpath)
	res := Stop(cfg, strings.NewReader(payload))
	if res.ExitCode != 0 {
		t.Fatalf("exit = %d, want 0 (non-coordinator skips check)", res.ExitCode)
	}
	if _, err := os.Stat(filepath.Join(stateDir, "codex-stuck-toolcalls.jsonl")); err == nil {
		t.Errorf("stuck-toolcalls ledger should not exist for non-coordinator session")
	}
}

func TestStop_StuckToolcall_ClaudeDriver_NoFire(t *testing.T) {
	dir := t.TempDir()
	stateDir := filepath.Join(dir, "coord")
	os.MkdirAll(stateDir, 0o700)
	tpath := writeCodexJSONL(t, dir, []string{
		`{"type":"session_meta","payload":{"id":"sess-y"}}`,
		`{"type":"response_item","payload":{"type":"function_call","name":"exec","call_id":"c1"}}`,
	})
	cfg := StopConfig{
		Inbox:               stateDir,
		CoordinatorStateDir: stateDir,
		Driver:              "claude",
		Now:                 fixedNow("2026-05-14T12:00:00Z"),
	}
	payload := fmt.Sprintf(`{"session_id":"sess-y","transcript_path":"%s"}`, tpath)
	res := Stop(cfg, strings.NewReader(payload))
	if res.ExitCode != 0 {
		t.Fatalf("exit = %d, want 0 (non-codex driver skips check)", res.ExitCode)
	}
}

func TestStop_StuckToolcall_ContextMonitorTakesPriority(t *testing.T) {
	dir := t.TempDir()
	stateDir := filepath.Join(dir, "coord")
	os.MkdirAll(stateDir, 0o700)
	tpath := writeCodexJSONL(t, dir, []string{
		`{"type":"session_meta","payload":{"id":"sess-both"}}`,
		`{"type":"response_item","payload":{"type":"function_call","name":"exec","call_id":"call_A"}}`,
		`{"type":"token_count","last_token_usage":{"total_tokens":200000}}`,
	})
	cfg := StopConfig{
		Inbox:               stateDir,
		CoordinatorStateDir: stateDir,
		Driver:              "codex",
		Now:                 fixedNow("2026-05-14T12:00:00Z"),
	}
	payload := fmt.Sprintf(`{"session_id":"sess-both","transcript_path":"%s"}`, tpath)
	res := Stop(cfg, strings.NewReader(payload))
	if res.ExitCode != 2 {
		t.Fatalf("exit = %d, want 2", res.ExitCode)
	}
	if !strings.Contains(res.Stderr, "hard cap") {
		t.Errorf("context monitor should fire first, got: %q", res.Stderr)
	}
	if strings.Contains(res.Stderr, "STUCK TOOLCALL") {
		t.Errorf("stuck-toolcall message should not appear when context monitor exits first: %q", res.Stderr)
	}
}

func TestStop_StuckToolcall_MultipleCallsListed(t *testing.T) {
	dir := t.TempDir()
	stateDir := filepath.Join(dir, "coord")
	os.MkdirAll(stateDir, 0o700)
	tpath := writeCodexJSONL(t, dir, []string{
		`{"type":"session_meta","payload":{"id":"sess-multi"}}`,
		`{"type":"response_item","payload":{"type":"function_call","name":"first","call_id":"c1"}}`,
		`{"type":"response_item","payload":{"type":"custom_tool_call","name":"apply_patch","call_id":"c2"}}`,
		`{"type":"response_item","payload":{"type":"function_call_output","call_id":"c1","output":"ok"}}`,
		`{"type":"response_item","payload":{"type":"function_call","name":"third","call_id":"c3"}}`,
	})
	cfg := StopConfig{
		Inbox:               stateDir,
		CoordinatorStateDir: stateDir,
		Driver:              "codex",
		Now:                 fixedNow("2026-05-14T12:00:00Z"),
	}
	payload := fmt.Sprintf(`{"session_id":"sess-multi","transcript_path":"%s"}`, tpath)
	res := Stop(cfg, strings.NewReader(payload))
	if res.ExitCode != 2 {
		t.Fatalf("exit = %d, want 2", res.ExitCode)
	}
	if !strings.Contains(res.Stderr, "2 unfinalized") {
		t.Errorf("stderr should report 2 unfinalized: %q", res.Stderr)
	}
	if !strings.Contains(res.Stderr, "c2") || !strings.Contains(res.Stderr, "c3") {
		t.Errorf("stderr missing both stuck call_ids: %q", res.Stderr)
	}
	body, _ := os.ReadFile(filepath.Join(stateDir, "codex-stuck-toolcalls.jsonl"))
	lines := strings.Count(string(body), "\n")
	if lines != 2 {
		t.Errorf("ledger should have 2 rows, got %d: %s", lines, body)
	}
}
