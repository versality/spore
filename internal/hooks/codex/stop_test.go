package codex

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func writeCodexJSONL(t *testing.T, dir string, lines []string) string {
	t.Helper()
	path := filepath.Join(dir, "rollout.jsonl")
	body := ""
	for _, l := range lines {
		body += l + "\n"
	}
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func fixedNow(ts string) func() time.Time {
	t, _ := time.Parse(time.RFC3339, ts)
	return func() time.Time { return t }
}

func TestStop_ContextHardCap(t *testing.T) {
	dir := t.TempDir()
	stateDir := filepath.Join(dir, "coord")
	if err := os.MkdirAll(stateDir, 0o700); err != nil {
		t.Fatal(err)
	}
	tpath := writeCodexJSONL(t, dir, []string{
		`{"type":"session_meta","payload":{"id":"sess-1"}}`,
		`{"type":"token_count","last_token_usage":{"total_tokens":200000}}`,
	})
	cfg := StopConfig{
		Inbox:               stateDir,
		CoordinatorStateDir: stateDir,
		Driver:              "codex",
		Now:                 fixedNow("2026-05-10T12:00:00Z"),
	}
	payload := fmt.Sprintf(`{"session_id":"sess-1","transcript_path":"%s"}`, tpath)
	res := Stop(cfg, strings.NewReader(payload))
	if res.ExitCode != 2 {
		t.Fatalf("exit = %d, want 2 (hard)", res.ExitCode)
	}
	if !strings.Contains(res.Stderr, "hard cap") {
		t.Errorf("stderr missing hard wrap msg: %q", res.Stderr)
	}
	body, _ := os.ReadFile(filepath.Join(stateDir, "codex-context-monitor.jsonl"))
	if !strings.Contains(string(body), `"hard_fired":true`) {
		t.Errorf("ledger missing hard_fired: %s", body)
	}
	if _, err := os.Stat(filepath.Join(stateDir, "state.md")); err != nil {
		t.Errorf("state.md not snapshotted: %v", err)
	}
}

func TestStop_ContextSoftCap_FiresOnce(t *testing.T) {
	dir := t.TempDir()
	stateDir := filepath.Join(dir, "coord")
	os.MkdirAll(stateDir, 0o700)
	tpath := writeCodexJSONL(t, dir, []string{
		`{"type":"session_meta","payload":{"id":"sess-1"}}`,
		`{"type":"token_count","last_token_usage":{"total_tokens":160000}}`,
	})
	cfg := StopConfig{
		Inbox:               stateDir,
		CoordinatorStateDir: stateDir,
		Driver:              "codex",
		Now:                 fixedNow("2026-05-10T12:00:00Z"),
	}
	payload := fmt.Sprintf(`{"session_id":"sess-1","transcript_path":"%s"}`, tpath)

	res1 := Stop(cfg, strings.NewReader(payload))
	if res1.ExitCode != 2 {
		t.Fatalf("first call exit = %d, want 2 (soft)", res1.ExitCode)
	}
	if !strings.Contains(res1.Stderr, "soft warn") {
		t.Errorf("first call missing soft msg: %q", res1.Stderr)
	}

	res2 := Stop(cfg, strings.NewReader(payload))
	if res2.ExitCode != 0 {
		t.Fatalf("second call exit = %d, want 0 (marker dedupe)", res2.ExitCode)
	}
}

func TestStop_NotCoordinator_NoMonitor(t *testing.T) {
	dir := t.TempDir()
	stateDir := filepath.Join(dir, "coord")
	os.MkdirAll(stateDir, 0o700)
	cfg := StopConfig{
		Inbox:               filepath.Join(dir, "elsewhere"),
		CoordinatorStateDir: stateDir,
		Driver:              "codex",
	}
	res := Stop(cfg, strings.NewReader(`{"session_id":"x"}`))
	if res.ExitCode != 0 {
		t.Fatalf("exit = %d, want 0", res.ExitCode)
	}
	if _, err := os.Stat(filepath.Join(stateDir, "codex-context-monitor.jsonl")); err == nil {
		t.Errorf("ledger should not be created for non-coordinator session")
	}
}

func TestStop_ClaudeDriver_SkipsContextMonitor(t *testing.T) {
	dir := t.TempDir()
	stateDir := filepath.Join(dir, "coord")
	os.MkdirAll(stateDir, 0o700)
	tpath := writeCodexJSONL(t, dir, []string{
		`{"type":"session_meta","payload":{"id":"s"}}`,
		`{"type":"token_count","last_token_usage":{"total_tokens":300000}}`,
	})
	cfg := StopConfig{
		Inbox:               stateDir,
		CoordinatorStateDir: stateDir,
		Driver:              "claude",
	}
	payload := fmt.Sprintf(`{"session_id":"s","transcript_path":"%s"}`, tpath)
	res := Stop(cfg, strings.NewReader(payload))
	if res.ExitCode != 0 {
		t.Fatalf("exit = %d", res.ExitCode)
	}
}

func TestStop_SessionMismatch_Skips(t *testing.T) {
	dir := t.TempDir()
	stateDir := filepath.Join(dir, "coord")
	os.MkdirAll(stateDir, 0o700)
	tpath := writeCodexJSONL(t, dir, []string{
		`{"type":"session_meta","payload":{"id":"transcript-sid"}}`,
		`{"type":"token_count","last_token_usage":{"total_tokens":200000}}`,
	})
	cfg := StopConfig{
		Inbox:               stateDir,
		CoordinatorStateDir: stateDir,
		Driver:              "codex",
	}
	res := Stop(cfg, strings.NewReader(fmt.Sprintf(`{"session_id":"different","transcript_path":"%s"}`, tpath)))
	if res.ExitCode != 0 {
		t.Fatalf("exit = %d, want 0 (skip on mismatch)", res.ExitCode)
	}
	body, _ := os.ReadFile(filepath.Join(stateDir, "codex-context-monitor.jsonl"))
	if !strings.Contains(string(body), `"reason":"session-mismatch"`) {
		t.Errorf("expected session-mismatch skip ledger: %s", body)
	}
}

func TestStop_DrainsWorkerInbox(t *testing.T) {
	dir := t.TempDir()
	wtState := filepath.Join(dir, "wt-state")
	inbox := filepath.Join(wtState, "feature-x", "inbox")
	if err := os.MkdirAll(inbox, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(inbox, "msg-1.json"), []byte(`{"body":"hi"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(inbox, "msg-2.json"), []byte(`{"body":"there"}`), 0o600); err != nil {
		t.Fatal(err)
	}

	cfg := StopConfig{
		Inbox:          inbox,
		WorkerStateDir: wtState,
	}
	res := Stop(cfg, strings.NewReader(`{}`))
	if res.ExitCode != 2 {
		t.Fatalf("exit = %d, want 2", res.ExitCode)
	}
	if !strings.Contains(res.Stderr, "2 message") {
		t.Errorf("stderr should mention 2 messages: %q", res.Stderr)
	}
	for _, n := range []string{"msg-1.json", "msg-2.json"} {
		if _, err := os.Stat(filepath.Join(inbox, "read", n)); err != nil {
			t.Errorf("expected %s in read/: %v", n, err)
		}
		if _, err := os.Stat(filepath.Join(inbox, n)); err == nil {
			t.Errorf("expected %s removed from inbox/", n)
		}
	}
}

func TestStop_NotWorkerInbox_SkipsDrain(t *testing.T) {
	dir := t.TempDir()
	wtState := filepath.Join(dir, "wt-state")
	os.MkdirAll(wtState, 0o700)
	otherInbox := filepath.Join(dir, "other-inbox")
	os.MkdirAll(otherInbox, 0o700)
	os.WriteFile(filepath.Join(otherInbox, "x.json"), []byte("{}"), 0o600)

	cfg := StopConfig{
		Inbox:          otherInbox,
		WorkerStateDir: wtState,
	}
	res := Stop(cfg, strings.NewReader(`{}`))
	if res.ExitCode != 0 {
		t.Fatalf("exit = %d, want 0 (no drain on non-worker inbox)", res.ExitCode)
	}
	if _, err := os.Stat(filepath.Join(otherInbox, "x.json")); err != nil {
		t.Errorf("file should not have moved")
	}
}

func TestStop_Chain_PropagatesExit2(t *testing.T) {
	cfg := StopConfig{
		Chain: []ChainHook{
			{Argv: []string{"sh", "-c", "echo wrap-msg >&2; exit 2"}},
		},
	}
	res := Stop(cfg, strings.NewReader(`{}`))
	if res.ExitCode != 2 {
		t.Fatalf("exit = %d, want 2", res.ExitCode)
	}
	if !strings.Contains(res.Stderr, "wrap-msg") {
		t.Errorf("expected sub-hook output: %q", res.Stderr)
	}
}

func TestStop_Chain_TimeoutContinues(t *testing.T) {
	cfg := StopConfig{
		CommandTimeout: 200 * time.Millisecond,
		Chain: []ChainHook{
			{Argv: []string{"sh", "-c", "sleep 5"}},
			{Argv: []string{"sh", "-c", "exit 0"}},
		},
	}
	res := Stop(cfg, strings.NewReader(`{}`))
	if res.ExitCode != 0 {
		t.Fatalf("exit = %d, want 0 (timeout should not propagate)", res.ExitCode)
	}
	if !strings.Contains(res.Stderr, "exited") && !strings.Contains(res.Stderr, "timed out") {
		t.Errorf("expected stderr note about non-zero exit: %q", res.Stderr)
	}
}

func TestStop_Chain_NonZeroNon2_Continues(t *testing.T) {
	cfg := StopConfig{
		Chain: []ChainHook{
			{Argv: []string{"sh", "-c", "exit 1"}},
			{Argv: []string{"sh", "-c", "exit 0"}},
		},
	}
	res := Stop(cfg, strings.NewReader(`{}`))
	if res.ExitCode != 0 {
		t.Fatalf("exit = %d, want 0 (rc=1 should not propagate)", res.ExitCode)
	}
}

func TestStop_Chain_PipesPayload(t *testing.T) {
	dir := t.TempDir()
	out := filepath.Join(dir, "pipe.txt")
	cfg := StopConfig{
		Chain: []ChainHook{
			{Argv: []string{"sh", "-c", "cat > " + out}},
		},
	}
	res := Stop(cfg, strings.NewReader(`{"hello":"world"}`))
	if res.ExitCode != 0 {
		t.Fatalf("exit = %d", res.ExitCode)
	}
	got, _ := os.ReadFile(out)
	if string(got) != `{"hello":"world"}` {
		t.Errorf("piped payload = %q", got)
	}
}

func TestStop_MissingTranscript_SkipsWithLedger(t *testing.T) {
	dir := t.TempDir()
	stateDir := filepath.Join(dir, "coord")
	os.MkdirAll(stateDir, 0o700)
	cfg := StopConfig{
		Inbox:               stateDir,
		CoordinatorStateDir: stateDir,
		Driver:              "codex",
	}
	res := Stop(cfg, strings.NewReader(`{"session_id":"s"}`))
	if res.ExitCode != 0 {
		t.Fatalf("exit = %d", res.ExitCode)
	}
	body, _ := os.ReadFile(filepath.Join(stateDir, "codex-context-monitor.jsonl"))
	if !strings.Contains(string(body), `"reason":"missing-transcript-path"`) {
		t.Errorf("missing skip ledger: %s", body)
	}
}
