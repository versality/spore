package secret

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestResolveRecipientsDedupesAndStripsComments(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "recipients.txt")
	contents := `# header
age1aaa
age1bbb # inline comment
age1aaa

  age1ccc
`
	if err := os.WriteFile(file, []byte(contents), 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := resolveRecipients([]string{"age1bbb", "age1zzz"}, file)
	if err != nil {
		t.Fatalf("resolveRecipients: %v", err)
	}
	want := []string{"age1bbb", "age1zzz", "age1aaa", "age1ccc"}
	if len(got) != len(want) {
		t.Fatalf("len mismatch: got %v want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("at %d: got %q want %q", i, got[i], want[i])
		}
	}
}

func TestAddRejectsEmptyRecipients(t *testing.T) {
	out := filepath.Join(t.TempDir(), "x.age")
	err := Add(Config{Out: out})
	if err == nil || !strings.Contains(err.Error(), "no recipient keys") {
		t.Fatalf("want recipient-required error, got %v", err)
	}
}

func TestAddRejectsMissingOutDir(t *testing.T) {
	err := Add(Config{
		Recipients: []string{"age1aaa"},
		Out:        filepath.Join(t.TempDir(), "missing", "x.age"),
		PopupRunner: func(scratch, label string) error {
			return os.WriteFile(scratch, []byte("hi"), 0o600)
		},
		AgeRunner: stubAge(t),
	})
	if err == nil || !strings.Contains(err.Error(), "parent dir does not exist") {
		t.Fatalf("want parent-dir error, got %v", err)
	}
}

func TestAddRejectsEmptyPaste(t *testing.T) {
	dir := t.TempDir()
	out := filepath.Join(dir, "x.age")
	err := Add(Config{
		Recipients: []string{"age1aaa"},
		Out:        out,
		PopupRunner: func(scratch, label string) error {
			return os.WriteFile(scratch, []byte(""), 0o600)
		},
		AgeRunner: stubAge(t),
	})
	if err == nil || !strings.Contains(err.Error(), "empty") {
		t.Fatalf("want empty-paste error, got %v", err)
	}
}

func TestAddRejectsNewlineOnlyPaste(t *testing.T) {
	dir := t.TempDir()
	out := filepath.Join(dir, "x.age")
	err := Add(Config{
		Recipients: []string{"age1aaa"},
		Out:        out,
		PopupRunner: func(scratch, label string) error {
			return os.WriteFile(scratch, []byte("\n\n"), 0o600)
		},
		AgeRunner: stubAge(t),
	})
	if err == nil || !strings.Contains(err.Error(), "empty") {
		t.Fatalf("want empty-paste error, got %v", err)
	}
}

func TestAddPropagatesPopupError(t *testing.T) {
	dir := t.TempDir()
	out := filepath.Join(dir, "x.age")
	popupErr := errors.New("popup boom")
	err := Add(Config{
		Recipients: []string{"age1aaa"},
		Out:        out,
		PopupRunner: func(scratch, label string) error {
			return popupErr
		},
		AgeRunner: stubAge(t),
	})
	if err == nil || !strings.Contains(err.Error(), "popup boom") {
		t.Fatalf("want popup error to surface, got %v", err)
	}
}

func TestAddPassesLabelToPopup(t *testing.T) {
	dir := t.TempDir()
	out := filepath.Join(dir, "x.age")
	var seen string
	err := Add(Config{
		Recipients: []string{"age1aaa"},
		Out:        out,
		Label:      "linear-key",
		PopupRunner: func(scratch, label string) error {
			seen = label
			return os.WriteFile(scratch, []byte("v"), 0o600)
		},
		AgeRunner: stubAge(t),
	})
	if err != nil {
		t.Fatalf("Add: %v", err)
	}
	if seen != "linear-key" {
		t.Fatalf("label not propagated: got %q", seen)
	}
}

func TestAddCallsAgeWithPlaintextAndRecipients(t *testing.T) {
	dir := t.TempDir()
	out := filepath.Join(dir, "secret.age")
	var gotPT []byte
	var gotR []string
	var gotOut string
	stderr := &bytes.Buffer{}
	err := Add(Config{
		Recipients:     []string{"age1aaa"},
		RecipientsFile: writeFile(t, dir, "r.txt", "age1bbb\n"),
		Out:            out,
		PopupRunner: func(scratch, label string) error {
			return os.WriteFile(scratch, []byte("hunter2\n"), 0o600)
		},
		AgeRunner: func(pt []byte, r []string, op string) error {
			gotPT = append([]byte(nil), pt...)
			gotR = append([]string(nil), r...)
			gotOut = op
			return os.WriteFile(op, []byte("ENCRYPTED"), 0o600)
		},
		Stderr: stderr,
	})
	if err != nil {
		t.Fatalf("Add: %v", err)
	}
	if string(gotPT) != "hunter2" {
		t.Fatalf("trailing newline not stripped: %q", gotPT)
	}
	wantR := []string{"age1aaa", "age1bbb"}
	if len(gotR) != len(wantR) || gotR[0] != wantR[0] || gotR[1] != wantR[1] {
		t.Fatalf("recipients: got %v want %v", gotR, wantR)
	}
	wantOut, _ := filepath.Abs(out)
	if gotOut != wantOut {
		t.Fatalf("out: got %q want %q", gotOut, wantOut)
	}
	if !strings.Contains(stderr.String(), "stored: secret.age") {
		t.Fatalf("stderr missing summary: %q", stderr.String())
	}
}

func TestAddRejectsAgeProducedEmpty(t *testing.T) {
	dir := t.TempDir()
	out := filepath.Join(dir, "x.age")
	err := Add(Config{
		Recipients: []string{"age1aaa"},
		Out:        out,
		PopupRunner: func(scratch, label string) error {
			return os.WriteFile(scratch, []byte("v"), 0o600)
		},
		AgeRunner: func(_ []byte, _ []string, op string) error {
			return os.WriteFile(op, nil, 0o600)
		},
	})
	if err == nil || !strings.Contains(err.Error(), "empty output") {
		t.Fatalf("want empty-output error, got %v", err)
	}
}

func TestDefaultPopupRefusesWithoutTMUX(t *testing.T) {
	t.Setenv("TMUX", "")
	if err := defaultPopup("/tmp/scratch", "lbl"); err == nil || !strings.Contains(err.Error(), "$TMUX") {
		t.Fatalf("want TMUX-required error, got %v", err)
	}
}

func TestScratchUsesXDGRuntimeDir(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_RUNTIME_DIR", dir)
	path, cleanup, err := makeScratch()
	if err != nil {
		t.Fatalf("makeScratch: %v", err)
	}
	defer cleanup()
	if filepath.Dir(path) != dir {
		t.Fatalf("scratch in wrong dir: %q (want under %q)", path, dir)
	}
	st, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat scratch: %v", err)
	}
	if st.Mode().Perm() != 0o600 {
		t.Fatalf("scratch perm: got %o want 0600", st.Mode().Perm())
	}
}

func writeFile(t *testing.T, dir, name, body string) string {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

func stubAge(t *testing.T) func([]byte, []string, string) error {
	t.Helper()
	return func(_ []byte, _ []string, op string) error {
		return os.WriteFile(op, []byte("ENCRYPTED"), 0o600)
	}
}
