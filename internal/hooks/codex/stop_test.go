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

func TestStop_Chain_TimeoutBlocks(t *testing.T) {
	cfg := StopConfig{
		CommandTimeout: 200 * time.Millisecond,
		Chain: []ChainHook{
			{Argv: []string{"sh", "-c", "sleep 5"}},
			{Argv: []string{"sh", "-c", "exit 0"}},
		},
	}
	res := Stop(cfg, strings.NewReader(`{}`))
	if res.ExitCode != 2 {
		t.Fatalf("exit = %d, want lifecycle-blocking exit 2", res.ExitCode)
	}
	if !strings.Contains(res.Stderr, "timed out") {
		t.Errorf("expected stderr note about timeout: %q", res.Stderr)
	}
}

func TestStop_Chain_NonZeroNon2Blocks(t *testing.T) {
	cfg := StopConfig{
		Chain: []ChainHook{
			{Argv: []string{"sh", "-c", "exit 1"}},
			{Argv: []string{"sh", "-c", "exit 0"}},
		},
	}
	res := Stop(cfg, strings.NewReader(`{}`))
	if res.ExitCode != 2 {
		t.Fatalf("exit = %d, want lifecycle-blocking exit 2", res.ExitCode)
	}
	if !strings.Contains(res.Stderr, "exited 1") {
		t.Errorf("expected stderr note about rc=1: %q", res.Stderr)
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

func TestStop_SnapshotState_PreservesOperatorQuestionsAndTail(t *testing.T) {
	dir := t.TempDir()
	stateDir := filepath.Join(dir, "coord")
	if err := os.MkdirAll(stateDir, 0o700); err != nil {
		t.Fatal(err)
	}
	existing := "# coordinator state - last updated 2026-04-01T00:00:00Z\n" +
		"\n## Operator questions\n\n- still waiting on rollout window\n" +
		"\n## Recent events\n\n- 2026-04-30T10:00:00Z spawned task-x\n- 2026-05-01T11:00:00Z reaped task-y\n" +
		"\n## Directives\n\nStand down at 22:00.\n"
	if err := os.WriteFile(filepath.Join(stateDir, "state.md"), []byte(existing), 0o600); err != nil {
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
		t.Fatalf("exit = %d, want 2", res.ExitCode)
	}
	body, err := os.ReadFile(filepath.Join(stateDir, "state.md"))
	if err != nil {
		t.Fatal(err)
	}
	got := string(body)
	for _, want := range []string{
		"## Operator questions",
		"still waiting on rollout window",
		"2026-04-30T10:00:00Z spawned task-x",
		"2026-05-01T11:00:00Z reaped task-y",
		"## Directives",
		"Stand down at 22:00.",
		"2026-05-10T12:00:00Z codex-context-monitor: auto-snapshotted state before hard wrap prompt; ctx=200000",
		"# coordinator state - last updated 2026-05-10T12:00:00Z",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("merged state.md missing %q\n---\n%s", want, got)
		}
	}
	if strings.Contains(got, "last updated 2026-04-01T00:00:00Z") {
		t.Errorf("heading timestamp not refreshed:\n%s", got)
	}
}

func TestStop_SnapshotState_NoPriorFileWritesFresh(t *testing.T) {
	dir := t.TempDir()
	stateDir := filepath.Join(dir, "coord")
	os.MkdirAll(stateDir, 0o700)
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
	res := Stop(cfg, strings.NewReader(fmt.Sprintf(`{"session_id":"sess-1","transcript_path":"%s"}`, tpath)))
	if res.ExitCode != 2 {
		t.Fatalf("exit = %d", res.ExitCode)
	}
	body, _ := os.ReadFile(filepath.Join(stateDir, "state.md"))
	if !strings.Contains(string(body), "## Recent events") {
		t.Errorf("fresh state.md missing Recent events: %s", body)
	}
}

func TestStop_WorkerStopErrors_LogsTimeout(t *testing.T) {
	dir := t.TempDir()
	wtState := filepath.Join(dir, "wt-state")
	inbox := filepath.Join(wtState, "feature-x", "inbox")
	if err := os.MkdirAll(inbox, 0o700); err != nil {
		t.Fatal(err)
	}
	cfg := StopConfig{
		Inbox:          inbox,
		WorkerStateDir: wtState,
		CommandTimeout: 200 * time.Millisecond,
		Now:            fixedNow("2026-05-10T12:00:00Z"),
		Chain: []ChainHook{
			{Argv: []string{"sh", "-c", "sleep 5"}},
		},
	}
	res := Stop(cfg, strings.NewReader(`{}`))
	if res.ExitCode != 2 {
		t.Fatalf("exit = %d, want lifecycle-blocking exit 2", res.ExitCode)
	}
	if !strings.Contains(res.Stderr, "timed out") {
		t.Fatalf("stderr = %q, want timeout continuation prompt", res.Stderr)
	}
	body, err := os.ReadFile(filepath.Join(wtState, "worker-stop-errors.jsonl"))
	if err != nil {
		t.Fatalf("ledger missing: %v", err)
	}
	got := string(body)
	if !strings.Contains(got, `"kind":"timeout"`) {
		t.Errorf("ledger missing timeout kind: %s", got)
	}
	if !strings.Contains(got, `"slug":"feature-x"`) {
		t.Errorf("ledger slug = %s, want feature-x", got)
	}
}

func TestStop_WorkerStopErrors_LogsNonZeroExit(t *testing.T) {
	dir := t.TempDir()
	wtState := filepath.Join(dir, "wt-state")
	inbox := filepath.Join(wtState, "feature-x", "inbox")
	os.MkdirAll(inbox, 0o700)
	cfg := StopConfig{
		Inbox:          inbox,
		WorkerStateDir: wtState,
		Now:            fixedNow("2026-05-10T12:00:00Z"),
		Chain: []ChainHook{
			{Argv: []string{"sh", "-c", "echo nope >&2; exit 7"}},
		},
	}
	res := Stop(cfg, strings.NewReader(`{}`))
	if res.ExitCode != 2 {
		t.Fatalf("exit = %d, want lifecycle-blocking exit 2", res.ExitCode)
	}
	if !strings.Contains(res.Stderr, "exited 7") {
		t.Fatalf("stderr = %q, want nonzero continuation prompt", res.Stderr)
	}
	body, _ := os.ReadFile(filepath.Join(wtState, "worker-stop-errors.jsonl"))
	got := string(body)
	if !strings.Contains(got, `"kind":"exit"`) || !strings.Contains(got, `"rc":7`) {
		t.Errorf("ledger row wrong: %s", got)
	}
	if !strings.Contains(got, "nope") {
		t.Errorf("ledger missing child stderr: %s", got)
	}
}

func TestStop_WorkerStopErrors_NotWrittenOnClean(t *testing.T) {
	dir := t.TempDir()
	wtState := filepath.Join(dir, "wt-state")
	inbox := filepath.Join(wtState, "feature-x", "inbox")
	os.MkdirAll(inbox, 0o700)
	cfg := StopConfig{
		Inbox:          inbox,
		WorkerStateDir: wtState,
		Chain: []ChainHook{
			{Argv: []string{"sh", "-c", "exit 0"}},
		},
	}
	Stop(cfg, strings.NewReader(`{}`))
	if _, err := os.Stat(filepath.Join(wtState, "worker-stop-errors.jsonl")); err == nil {
		t.Errorf("ledger should be absent on clean chain run")
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
