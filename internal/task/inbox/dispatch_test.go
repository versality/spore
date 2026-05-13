package inbox

import (
	"bytes"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"testing"
)

func writeEnvelope(t *testing.T, dir, name, body string) string {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatalf("write %s: %v", name, err)
	}
	return p
}

// makeHandler writes a tiny shell handler that records its argv into
// recordPath and exits with the configured rc.
func makeHandler(t *testing.T, recordPath string, rc int) string {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("shell handler unsupported on windows")
	}
	dir := t.TempDir()
	p := filepath.Join(dir, "handler.sh")
	script := "#!/bin/sh\nprintf '%s\\n' \"$1\" >> " + recordPath + "\n" +
		"exit " + itoa(rc) + "\n"
	if err := os.WriteFile(p, []byte(script), 0o755); err != nil {
		t.Fatalf("write handler: %v", err)
	}
	return p
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var b [20]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	s := string(b[i:])
	if neg {
		s = "-" + s
	}
	return s
}

func TestDispatchMatchesAndMoves(t *testing.T) {
	inbox := t.TempDir()
	writeEnvelope(t, inbox, "1.json", `{"msg":"foo-slug blocked-upstream-spore: waiting"}`)
	writeEnvelope(t, inbox, "2.json", `{"msg":"unrelated chatter"}`)
	writeEnvelope(t, inbox, "3.json", `{"body":"bar-slug blocked-upstream-spore: PR landed"}`)

	record := filepath.Join(t.TempDir(), "record")
	handler := makeHandler(t, record, 0)

	var log bytes.Buffer
	res, err := Dispatch(DispatchOptions{
		Dir:     inbox,
		Token:   regexp.MustCompile(`blocked-upstream-spore:`),
		Handler: handler,
		Log:     &log,
	})
	if err != nil {
		t.Fatalf("dispatch: %v", err)
	}
	if res.Scanned != 3 {
		t.Errorf("scanned = %d, want 3", res.Scanned)
	}
	if res.Matched != 2 {
		t.Errorf("matched = %d, want 2", res.Matched)
	}
	if res.Handled != 2 {
		t.Errorf("handled = %d, want 2", res.Handled)
	}

	for _, n := range []string{"1.json", "3.json"} {
		if _, err := os.Stat(filepath.Join(inbox, n)); !os.IsNotExist(err) {
			t.Errorf("%s should have been moved out of inbox top-level", n)
		}
		if _, err := os.Stat(filepath.Join(inbox, "read", n)); err != nil {
			t.Errorf("%s missing from read/: %v", n, err)
		}
	}
	if _, err := os.Stat(filepath.Join(inbox, "2.json")); err != nil {
		t.Errorf("unrelated envelope should still be in inbox: %v", err)
	}

	got, err := os.ReadFile(record)
	if err != nil {
		t.Fatalf("read record: %v", err)
	}
	for _, want := range []string{filepath.Join(inbox, "1.json"), filepath.Join(inbox, "3.json")} {
		if !bytes.Contains(got, []byte(want)) {
			t.Errorf("handler record missing %q; got %s", want, got)
		}
	}
}

func TestDispatchHandlerFailureLeavesEnvelope(t *testing.T) {
	inbox := t.TempDir()
	writeEnvelope(t, inbox, "1.json", `{"msg":"slug blocked-upstream-spore: x"}`)

	handler := makeHandler(t, filepath.Join(t.TempDir(), "ignored"), 1)
	res, err := Dispatch(DispatchOptions{
		Dir:     inbox,
		Token:   regexp.MustCompile(`blocked-upstream-spore:`),
		Handler: handler,
	})
	if err != nil {
		t.Fatalf("dispatch: %v", err)
	}
	if res.Matched != 1 || res.Handled != 0 || res.Failed != 1 {
		t.Errorf("counters = %+v, want matched=1 handled=0 failed=1", res)
	}
	if _, err := os.Stat(filepath.Join(inbox, "1.json")); err != nil {
		t.Errorf("envelope should remain at top-level after handler failure: %v", err)
	}
}

func TestDispatchMissingInboxIsNoop(t *testing.T) {
	res, err := Dispatch(DispatchOptions{
		Dir:     filepath.Join(t.TempDir(), "does-not-exist"),
		Token:   regexp.MustCompile(`x`),
		Handler: "/bin/true",
	})
	if err != nil {
		t.Fatalf("dispatch: %v", err)
	}
	if res.Scanned != 0 || res.Handled != 0 {
		t.Errorf("missing inbox should be noop, got %+v", res)
	}
}

func TestDispatchRawFallback(t *testing.T) {
	inbox := t.TempDir()
	// Envelope where neither msg nor body is set: regex falls back
	// to the raw JSON content.
	writeEnvelope(t, inbox, "1.json", `{"slug":"abc","kind":"blocked-upstream-spore"}`)
	handler := makeHandler(t, filepath.Join(t.TempDir(), "rec"), 0)
	res, err := Dispatch(DispatchOptions{
		Dir:     inbox,
		Token:   regexp.MustCompile(`blocked-upstream-spore`),
		Handler: handler,
	})
	if err != nil {
		t.Fatalf("dispatch: %v", err)
	}
	if res.Handled != 1 {
		t.Errorf("handled = %d, want 1", res.Handled)
	}
}

func TestDispatchRequiresToken(t *testing.T) {
	_, err := Dispatch(DispatchOptions{Dir: t.TempDir(), Handler: "/bin/true"})
	if err == nil {
		t.Fatal("expected error when token is nil")
	}
}
