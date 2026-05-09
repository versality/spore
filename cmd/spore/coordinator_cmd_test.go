package main

import (
	"bytes"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	spore "github.com/versality/spore"
)

func TestJoinRoleConsumerBlankLine(t *testing.T) {
	cases := []struct {
		name     string
		role     string
		consumer string
		want     string
	}{
		{"role no trailing newline", "role", "consumer", "role\n\nconsumer"},
		{"role single trailing newline", "role\n", "consumer\n", "role\n\nconsumer\n"},
		{"role double trailing newline", "role\n\n", "consumer\n", "role\n\nconsumer\n"},
		{"role triple trailing newline", "role\n\n\n", "consumer", "role\n\nconsumer"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := string(joinRoleConsumer([]byte(tc.role), []byte(tc.consumer)))
			if got != tc.want {
				t.Fatalf("joinRoleConsumer(%q, %q) = %q, want %q", tc.role, tc.consumer, got, tc.want)
			}
		})
	}
}

// captureRoleBrief invokes runCoordinatorRoleBrief with stdout / stderr
// redirected to in-memory pipes so the test can assert exit code + output
// without coupling to the global os.Stdout file descriptor.
func captureRoleBrief(t *testing.T, args []string) (code int, stdout, stderr string) {
	t.Helper()
	origOut, origErr := os.Stdout, os.Stderr
	t.Cleanup(func() { os.Stdout, os.Stderr = origOut, origErr })

	outR, outW, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe stdout: %v", err)
	}
	errR, errW, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe stderr: %v", err)
	}
	os.Stdout, os.Stderr = outW, errW

	done := make(chan [2]string, 1)
	go func() {
		var ob, eb bytes.Buffer
		_, _ = io.Copy(&ob, outR)
		_, _ = io.Copy(&eb, errR)
		done <- [2]string{ob.String(), eb.String()}
	}()

	code = runCoordinatorRoleBrief(args)
	outW.Close()
	errW.Close()
	got := <-done
	return code, got[0], got[1]
}

func TestRoleBriefRoleOnly(t *testing.T) {
	dir := t.TempDir()
	role := filepath.Join(dir, "role.md")
	if err := os.WriteFile(role, []byte("ROLE BODY\n"), 0o600); err != nil {
		t.Fatalf("write role: %v", err)
	}
	code, out, errOut := captureRoleBrief(t, []string{"--role", role})
	if code != 0 {
		t.Fatalf("exit=%d stderr=%q", code, errOut)
	}
	if out != "ROLE BODY\n" {
		t.Fatalf("stdout=%q want %q", out, "ROLE BODY\n")
	}
}

func TestRoleBriefConsumerOnlyUsesEmbeddedRole(t *testing.T) {
	dir := t.TempDir()
	consumer := filepath.Join(dir, "consumer.md")
	if err := os.WriteFile(consumer, []byte("EXTRAS\n"), 0o600); err != nil {
		t.Fatalf("write consumer: %v", err)
	}
	code, out, errOut := captureRoleBrief(t, []string{"--consumer", consumer})
	if code != 0 {
		t.Fatalf("exit=%d stderr=%q", code, errOut)
	}
	wantPrefix := strings.TrimRight(string(spore.BundledCoordinatorRole), "\n") + "\n\n"
	if !strings.HasPrefix(out, wantPrefix) {
		t.Fatalf("stdout missing role prefix:\nhead=%q", head(out, 200))
	}
	if !strings.HasSuffix(out, "EXTRAS\n") {
		t.Fatalf("stdout missing consumer tail:\ntail=%q", tail(out, 80))
	}
}

func TestRoleBriefBothFlags(t *testing.T) {
	dir := t.TempDir()
	role := filepath.Join(dir, "role.md")
	consumer := filepath.Join(dir, "consumer.md")
	if err := os.WriteFile(role, []byte("R\n"), 0o600); err != nil {
		t.Fatalf("write role: %v", err)
	}
	if err := os.WriteFile(consumer, []byte("C\n"), 0o600); err != nil {
		t.Fatalf("write consumer: %v", err)
	}
	code, out, errOut := captureRoleBrief(t, []string{"--role", role, "--consumer", consumer})
	if code != 0 {
		t.Fatalf("exit=%d stderr=%q", code, errOut)
	}
	if out != "R\n\nC\n" {
		t.Fatalf("stdout=%q want %q", out, "R\n\nC\n")
	}
}

func TestRoleBriefMissingRole(t *testing.T) {
	dir := t.TempDir()
	missing := filepath.Join(dir, "no-such-role.md")
	code, out, errOut := captureRoleBrief(t, []string{"--role", missing})
	if code != 1 {
		t.Fatalf("exit=%d want 1; stdout=%q stderr=%q", code, out, errOut)
	}
	if out != "" {
		t.Fatalf("stdout=%q want empty", out)
	}
	if !strings.Contains(errOut, "no-such-role.md") {
		t.Fatalf("stderr missing path: %q", errOut)
	}
}

func TestRoleBriefMissingConsumer(t *testing.T) {
	dir := t.TempDir()
	role := filepath.Join(dir, "role.md")
	if err := os.WriteFile(role, []byte("R\n"), 0o600); err != nil {
		t.Fatalf("write role: %v", err)
	}
	missing := filepath.Join(dir, "no-such-consumer.md")
	code, out, errOut := captureRoleBrief(t, []string{"--role", role, "--consumer", missing})
	if code != 1 {
		t.Fatalf("exit=%d want 1; stdout=%q stderr=%q", code, out, errOut)
	}
	if out != "" {
		t.Fatalf("stdout=%q want empty", out)
	}
	if !strings.Contains(errOut, "no-such-consumer.md") {
		t.Fatalf("stderr missing path: %q", errOut)
	}
}

func head(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n]
}

func tail(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[len(s)-n:]
}

// captureCoordinatorTokenMonitor mirrors the worker-side helper: pipes
// `input` through stdin and captures stderr, so the test can assert exit
// code + reminder body without touching the real fds.
func captureCoordinatorTokenMonitor(t *testing.T, input string) (code int, stderr string) {
	t.Helper()

	origIn, origOut, origErr := os.Stdin, os.Stdout, os.Stderr
	t.Cleanup(func() { os.Stdin, os.Stdout, os.Stderr = origIn, origOut, origErr })

	inR, inW, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe stdin: %v", err)
	}
	if _, err := inW.Write([]byte(input)); err != nil {
		t.Fatalf("write stdin: %v", err)
	}
	inW.Close()
	os.Stdin = inR

	outR, outW, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe stdout: %v", err)
	}
	errR, errW, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe stderr: %v", err)
	}
	os.Stdout, os.Stderr = outW, errW

	code = runCoordinatorTokenMonitor(nil)

	outW.Close()
	errW.Close()
	io.Copy(io.Discard, outR)
	var errBuf bytes.Buffer
	io.Copy(&errBuf, errR)
	return code, errBuf.String()
}

func TestRunCoordinatorTokenMonitorSkipNoInbox(t *testing.T) {
	t.Setenv("SPORE_TASK_INBOX", "")
	code, stderr := captureCoordinatorTokenMonitor(t, `{"session_id":"s","transcript_path":""}`)
	if code != 0 {
		t.Fatalf("expected exit 0 with no inbox, got %d (stderr=%q)", code, stderr)
	}
	if stderr != "" {
		t.Errorf("expected empty stderr on skip, got %q", stderr)
	}
}

// Regression: prior to spore commit 50443d7, the coordinator inbox
// gate read $SKYBOT_INBOX. After the rename, only $SPORE_TASK_INBOX is
// honoured. Pin that contract: setting only the legacy env still
// produces a silent skip (no ledger row, no fire). Catches a future
// reintroduction of the legacy alias.
func TestRunCoordinatorTokenMonitorIgnoresLegacyAlias(t *testing.T) {
	dir := t.TempDir()
	stateDir := filepath.Join(dir, "coord")
	t.Setenv("SPORE_COORDINATOR_STATE_DIR", stateDir)
	t.Setenv("SKYBOT_INBOX", filepath.Join(stateDir, "proj", "inbox"))
	t.Setenv("SPORE_TASK_INBOX", "")

	transcriptFile := filepath.Join(dir, "session.jsonl")
	line := `{"role":"assistant","usage":{"input_tokens":160000,"cache_creation_input_tokens":0,"cache_read_input_tokens":0}}`
	if err := os.WriteFile(transcriptFile, []byte(line+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	payload := `{"session_id":"sid","transcript_path":"` + transcriptFile + `"}`
	code, stderr := captureCoordinatorTokenMonitor(t, payload)
	if code != 0 {
		t.Fatalf("expected exit 0 (silent skip) when only SKYBOT_INBOX set, got %d (stderr=%q)", code, stderr)
	}
	if stderr != "" {
		t.Errorf("expected empty stderr, got %q", stderr)
	}
	ledger := filepath.Join(stateDir, "token-monitor.jsonl")
	if _, err := os.Stat(ledger); err == nil {
		t.Errorf("expected no ledger when only legacy SKYBOT_INBOX is set; ledger exists at %s", ledger)
	}
}

func TestRunCoordinatorTokenMonitorSoftFires(t *testing.T) {
	dir := t.TempDir()
	stateDir := filepath.Join(dir, "coord")
	inbox := filepath.Join(stateDir, "proj", "inbox")
	t.Setenv("SPORE_COORDINATOR_STATE_DIR", stateDir)
	t.Setenv("SPORE_TASK_INBOX", inbox)

	transcriptFile := filepath.Join(dir, "session.jsonl")
	line := `{"role":"assistant","usage":{"input_tokens":160000,"cache_creation_input_tokens":0,"cache_read_input_tokens":0}}`
	if err := os.WriteFile(transcriptFile, []byte(line+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	payload := `{"session_id":"sid-soft","transcript_path":"` + transcriptFile + `"}`
	code, stderr := captureCoordinatorTokenMonitor(t, payload)
	if code != 2 {
		t.Fatalf("expected exit 2 on soft, got %d (stderr=%q)", code, stderr)
	}
	if !strings.Contains(stderr, "COORDINATOR TOKEN MONITOR (soft)") {
		t.Errorf("expected soft reminder in stderr, got %q", stderr)
	}
	ledger := filepath.Join(stateDir, "token-monitor.jsonl")
	body, err := os.ReadFile(ledger)
	if err != nil {
		t.Fatalf("expected ledger row, got read err %v", err)
	}
	if !strings.Contains(string(body), `"soft_fired":true`) {
		t.Errorf("expected soft_fired:true in ledger, got %q", string(body))
	}
	if !strings.Contains(string(body), `"session_id":"sid-soft"`) {
		t.Errorf("expected sid-soft in ledger, got %q", string(body))
	}
}

func TestRunCoordinatorTokenMonitorOkWritesLedger(t *testing.T) {
	dir := t.TempDir()
	stateDir := filepath.Join(dir, "coord")
	inbox := filepath.Join(stateDir, "proj", "inbox")
	t.Setenv("SPORE_COORDINATOR_STATE_DIR", stateDir)
	t.Setenv("SPORE_TASK_INBOX", inbox)

	transcriptFile := filepath.Join(dir, "session.jsonl")
	line := `{"role":"assistant","usage":{"input_tokens":50000,"cache_creation_input_tokens":0,"cache_read_input_tokens":0}}`
	if err := os.WriteFile(transcriptFile, []byte(line+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	payload := `{"session_id":"sid-ok","transcript_path":"` + transcriptFile + `"}`
	code, stderr := captureCoordinatorTokenMonitor(t, payload)
	if code != 0 {
		t.Fatalf("expected exit 0 below cap, got %d (stderr=%q)", code, stderr)
	}
	ledger := filepath.Join(stateDir, "token-monitor.jsonl")
	body, err := os.ReadFile(ledger)
	if err != nil {
		t.Fatalf("expected ledger row at every Stop, got read err %v", err)
	}
	if !strings.Contains(string(body), `"soft_fired":false`) || !strings.Contains(string(body), `"hard_fired":false`) {
		t.Errorf("expected soft_fired:false hard_fired:false in ok-band row, got %q", string(body))
	}
	if !strings.Contains(string(body), `"session_id":"sid-ok"`) {
		t.Errorf("expected sid-ok in ledger, got %q", string(body))
	}
}

// TestRunCoordinatorTokenMonitorHardFires is the stop-hook integration
// test for the hard-cap path: a transcript above hard cap fires a
// hard reminder regardless of soft-marker state, exits 2, and lands a
// hard_fired=true ledger row. Closes the gap between the soft-cap +
// ok-band tests above and the worker-side TokenMonitorWrap coverage.
func TestRunCoordinatorTokenMonitorHardFires(t *testing.T) {
	dir := t.TempDir()
	stateDir := filepath.Join(dir, "coord")
	inbox := filepath.Join(stateDir, "proj", "inbox")
	t.Setenv("SPORE_COORDINATOR_STATE_DIR", stateDir)
	t.Setenv("SPORE_TASK_INBOX", inbox)

	transcriptFile := filepath.Join(dir, "session.jsonl")
	line := `{"role":"assistant","usage":{"input_tokens":195000,"cache_creation_input_tokens":0,"cache_read_input_tokens":0}}`
	if err := os.WriteFile(transcriptFile, []byte(line+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	payload := `{"session_id":"sid-hard","transcript_path":"` + transcriptFile + `"}`
	code, stderr := captureCoordinatorTokenMonitor(t, payload)
	if code != 2 {
		t.Fatalf("expected exit 2 on hard, got %d (stderr=%q)", code, stderr)
	}
	if !strings.Contains(stderr, "COORDINATOR TOKEN MONITOR (hard)") {
		t.Errorf("expected hard reminder in stderr, got %q", stderr)
	}
	if !strings.Contains(stderr, "tmux kill-session") {
		t.Errorf("expected wrap-up instruction in stderr, got %q", stderr)
	}
	ledger := filepath.Join(stateDir, "token-monitor.jsonl")
	body, err := os.ReadFile(ledger)
	if err != nil {
		t.Fatalf("expected ledger row, got read err %v", err)
	}
	if !strings.Contains(string(body), `"hard_fired":true`) {
		t.Errorf("expected hard_fired:true in ledger, got %q", string(body))
	}
	if !strings.Contains(string(body), `"session_id":"sid-hard"`) {
		t.Errorf("expected sid-hard in ledger, got %q", string(body))
	}
}
