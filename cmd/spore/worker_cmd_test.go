package main

import (
	"bytes"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// captureWorkerTokenMonitor runs runWorkerTokenMonitor with stdin
// piped from input and stdout/stderr captured. It mirrors the helper
// pattern used by other cmd-level tests so the test never touches the
// real os.Stdout / os.Stderr.
func captureWorkerTokenMonitor(t *testing.T, input string) (code int, stderr string) {
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

	code = runWorkerTokenMonitor(nil)

	outW.Close()
	errW.Close()
	io.Copy(io.Discard, outR)
	var errBuf bytes.Buffer
	io.Copy(&errBuf, errR)
	return code, errBuf.String()
}

func TestRunWorkerTokenMonitorSkipNoInbox(t *testing.T) {
	t.Setenv("SPORE_TASK_INBOX", "")
	code, _ := captureWorkerTokenMonitor(t, `{"session_id":"s","transcript_path":""}`)
	if code != 0 {
		t.Fatalf("expected exit 0 with no inbox, got %d", code)
	}
}

func TestRunWorkerTokenMonitorWrap(t *testing.T) {
	dir := t.TempDir()
	transcriptFile := filepath.Join(dir, "session.jsonl")
	line := `{"role":"assistant","usage":{"input_tokens":190000,"cache_creation_input_tokens":0,"cache_read_input_tokens":0}}`
	if err := os.WriteFile(transcriptFile, []byte(line+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	t.Setenv("SPORE_TASK_INBOX", filepath.Join(dir, "workers", "wrapme", "inbox"))
	t.Setenv("SPORE_COORDINATOR_STATE_DIR", filepath.Join(dir, "coord"))
	t.Setenv("SPORE_ACCOUNT_TIER", "max")

	payload := `{"session_id":"sid","transcript_path":"` + transcriptFile + `"}`
	code, stderr := captureWorkerTokenMonitor(t, payload)
	if code != 2 {
		t.Fatalf("expected exit 2 on wrap, got %d (stderr=%q)", code, stderr)
	}
	if !strings.Contains(stderr, "WORKER TOKEN MONITOR") {
		t.Errorf("expected WORKER TOKEN MONITOR in stderr, got %q", stderr)
	}
	if !strings.Contains(stderr, "wrapme") {
		t.Errorf("expected slug in stderr, got %q", stderr)
	}
}

func TestRunWorkerTokenMonitorOk(t *testing.T) {
	dir := t.TempDir()
	transcriptFile := filepath.Join(dir, "session.jsonl")
	line := `{"role":"assistant","usage":{"input_tokens":50000,"cache_creation_input_tokens":0,"cache_read_input_tokens":0}}`
	if err := os.WriteFile(transcriptFile, []byte(line+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	t.Setenv("SPORE_TASK_INBOX", filepath.Join(dir, "workers", "ok", "inbox"))
	t.Setenv("SPORE_COORDINATOR_STATE_DIR", filepath.Join(dir, "coord"))
	t.Setenv("SPORE_ACCOUNT_TIER", "max")

	payload := `{"session_id":"sid","transcript_path":"` + transcriptFile + `"}`
	code, stderr := captureWorkerTokenMonitor(t, payload)
	if code != 0 {
		t.Fatalf("expected exit 0 below cap, got %d (stderr=%q)", code, stderr)
	}
}

// TestRunWorkerExitKindTellEnvelope is the acceptance "a clean wrap
// shows one tell envelope reaching coordinator inbox" check. The CLI
// shells out to `wt task tell <dest> <body>`; we stub `wt` with a
// script that captures argv to a file so the assertion runs without
// touching the real operator inbox.
func TestRunWorkerExitKindTellEnvelope(t *testing.T) {
	dir := t.TempDir()
	marker := filepath.Join(dir, "clean-exit")
	if err := os.WriteFile(marker, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	capture := filepath.Join(dir, "wt-args")
	stub := filepath.Join(dir, "wt")
	script := "#!/bin/sh\nprintf '%s\\n' \"$@\" > " + capture + "\n"
	if err := os.WriteFile(stub, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))

	origOut := os.Stdout
	t.Cleanup(func() { os.Stdout = origOut })
	devnull, err := os.OpenFile(os.DevNull, os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer devnull.Close()
	os.Stdout = devnull

	// Clean wrap: marker present, rc=129 (lifecycle SIGHUP shape).
	args := []string{
		"--rc=129",
		"--marker=" + marker,
		"--slug=spore-worker-exit-kind",
		"--tell-coordinator",
	}
	if rc := runWorkerExitKind(args); rc != 0 {
		t.Fatalf("runWorkerExitKind rc=%d, want 0", rc)
	}

	body, err := os.ReadFile(capture)
	if err != nil {
		t.Fatalf("read capture: %v", err)
	}
	got := strings.Split(strings.TrimRight(string(body), "\n"), "\n")
	want := []string{"task", "tell", "coordinator", "spore-worker-exit-kind exit kind=lifecycle rc=129"}
	if len(got) != len(want) {
		t.Fatalf("argv = %q, want %q", got, want)
	}
	for i, w := range want {
		if got[i] != w {
			t.Errorf("argv[%d] = %q, want %q", i, got[i], w)
		}
	}
}

func TestRunWorkerDispatch(t *testing.T) {
	if code := runWorker(nil); code != 2 {
		t.Errorf("runWorker(nil) = %d, want 2", code)
	}
	if code := runWorker([]string{"bogus"}); code != 2 {
		t.Errorf("runWorker(bogus) = %d, want 2", code)
	}
}
