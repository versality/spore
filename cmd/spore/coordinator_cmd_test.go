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
