package main

import (
	"bytes"
	"encoding/json"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/versality/spore/internal/lints"
)

func TestReorderLintArgsAllowsFlagsAfterName(t *testing.T) {
	got := reorderLintArgs([]string{"claude-drift", "--render-cmd", "printf ok", "--root", "repo"})
	want := []string{"--render-cmd", "printf ok", "--root", "repo", "claude-drift"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v want %v", got, want)
	}
}

func TestReorderLintArgsLeavesBoolFlagsValueless(t *testing.T) {
	got := reorderLintArgs([]string{"--list"})
	want := []string{"--list"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v want %v", got, want)
	}
}

func TestEmitJSONSchemaAndFingerprintDefault(t *testing.T) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	issue := lints.Issue{Path: "foo.go", Line: 7, Message: "msg"}
	if err := emitJSON(enc, "emdash", issue, false); err != nil {
		t.Fatalf("emitJSON: %v", err)
	}
	var got jsonFinding
	if err := json.Unmarshal(buf.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v (raw %q)", err, buf.String())
	}
	if got.Lint != "emdash" || got.Path != "foo.go" || got.Line != 7 || got.Message != "msg" {
		t.Fatalf("unexpected fields: %+v", got)
	}
	if got.Severity != "error" {
		t.Fatalf("default severity should be error when not warn-only, got %q", got.Severity)
	}
	if got.Fingerprint != lints.Fingerprint("emdash", "foo.go", 7, "msg") {
		t.Fatalf("fingerprint default not computed from tuple: %q", got.Fingerprint)
	}
	if got.Ts == "" {
		t.Fatalf("ts must be populated")
	}
}

func TestEmitJSONWarnOnlySeverity(t *testing.T) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	issue := lints.Issue{Path: "x", Line: 1, Message: "y"}
	if err := emitJSON(enc, "task-evidence", issue, true); err != nil {
		t.Fatalf("emitJSON: %v", err)
	}
	var got jsonFinding
	if err := json.Unmarshal(buf.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got.Severity != "warn" {
		t.Fatalf("warn-only must yield warn severity, got %q", got.Severity)
	}
}

func TestEmitJSONPreservesExplicitFields(t *testing.T) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	issue := lints.Issue{
		Path:        "a",
		Line:        2,
		Message:     "b",
		Severity:    "info",
		Fingerprint: "v9:deadbeef",
	}
	if err := emitJSON(enc, "l", issue, false); err != nil {
		t.Fatalf("emitJSON: %v", err)
	}
	var got jsonFinding
	if err := json.Unmarshal(buf.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got.Severity != "info" || got.Fingerprint != "v9:deadbeef" {
		t.Fatalf("explicit fields not preserved: %+v", got)
	}
}

func TestRunLintJSONEndToEnd(t *testing.T) {
	root := t.TempDir()
	mustWrite(t, filepath.Join(root, "rule.md"), "this has an em\u2014dash inside\n")
	gitInit(t, root)

	var code int
	stdout, stderr := captureStdoutStderr(t, func() {
		code = runLint([]string{"emdash", "--root", root, "--json"})
	})
	if code != 1 {
		t.Fatalf("expected exit 1 (issues found), got %d\nstdout: %s\nstderr: %s", code, stdout, stderr)
	}

	lines := strings.Split(strings.TrimSpace(stdout), "\n")
	if len(lines) == 0 || lines[0] == "" {
		t.Fatalf("expected at least one JSONL row, got %q", stdout)
	}
	var row jsonFinding
	if err := json.Unmarshal([]byte(lines[0]), &row); err != nil {
		t.Fatalf("first row not valid JSON: %v (%q)", err, lines[0])
	}
	if row.Lint != "emdash" {
		t.Fatalf("expected lint=emdash, got %q", row.Lint)
	}
	if !strings.HasPrefix(row.Fingerprint, lints.FingerprintVersion+":") {
		t.Fatalf("fingerprint missing version prefix: %q", row.Fingerprint)
	}
}

func mustWrite(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
}

func gitInit(t *testing.T, root string) {
	t.Helper()
	for _, args := range [][]string{
		{"init", "-q"},
		{"-c", "user.email=t@example.com", "-c", "user.name=t", "add", "-A"},
		{"-c", "user.email=t@example.com", "-c", "user.name=t", "commit", "-q", "-m", "init"},
	} {
		cmd := exec.Command("git", append([]string{"-C", root}, args...)...)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v (%s)", args, err, out)
		}
	}
}

func captureStdoutStderr(t *testing.T, fn func()) (string, string) {
	t.Helper()
	origOut, origErr := os.Stdout, os.Stderr
	rOut, wOut, _ := os.Pipe()
	rErr, wErr, _ := os.Pipe()
	os.Stdout, os.Stderr = wOut, wErr
	defer func() {
		os.Stdout, os.Stderr = origOut, origErr
	}()
	var outBuf, errBuf bytes.Buffer
	outDone := make(chan struct{})
	errDone := make(chan struct{})
	go func() {
		_, _ = io.Copy(&outBuf, rOut)
		close(outDone)
	}()
	go func() {
		_, _ = io.Copy(&errBuf, rErr)
		close(errDone)
	}()
	fn()
	wOut.Close()
	wErr.Close()
	<-outDone
	<-errDone
	return outBuf.String(), errBuf.String()
}
