package wtcheck

import (
	"bytes"
	"errors"
	"io"
	"os/exec"
	"reflect"
	"strings"
	"testing"
)

type call struct {
	dir  string
	name string
	args []string
}

type fakeRunner struct {
	calls []call
	code  int
	err   error
}

func (f *fakeRunner) Run(dir, name string, args []string, _, _ io.Writer) (int, error) {
	f.calls = append(f.calls, call{dir: dir, name: name, args: append([]string(nil), args...)})
	return f.code, f.err
}

func TestRun_invokesNixDevelopJustCheck(t *testing.T) {
	r := &fakeRunner{code: 0}
	got := Run(Config{Root: "/some/root"}, r.Run, io.Discard, io.Discard)
	if got != 0 {
		t.Fatalf("exit code: got %d, want 0", got)
	}
	if len(r.calls) != 1 {
		t.Fatalf("call count: got %d, want 1", len(r.calls))
	}
	c := r.calls[0]
	if c.dir != "/some/root" {
		t.Errorf("dir: got %q, want %q", c.dir, "/some/root")
	}
	if c.name != "nix" {
		t.Errorf("name: got %q, want %q", c.name, "nix")
	}
	wantArgs := []string{"develop", "-c", "just", "check"}
	if !reflect.DeepEqual(c.args, wantArgs) {
		t.Errorf("args: got %v, want %v", c.args, wantArgs)
	}
}

func TestRun_propagatesNonZeroExit(t *testing.T) {
	r := &fakeRunner{code: 7}
	got := Run(Config{Root: "/r"}, r.Run, io.Discard, io.Discard)
	if got != 7 {
		t.Fatalf("exit code: got %d, want 7 (propagated)", got)
	}
}

func TestRun_nixNotOnPath_returns2WithMessage(t *testing.T) {
	r := &fakeRunner{err: exec.ErrNotFound}
	var stderr bytes.Buffer
	got := Run(Config{Root: "/r"}, r.Run, io.Discard, &stderr)
	if got != 2 {
		t.Fatalf("exit code: got %d, want 2", got)
	}
	if !strings.Contains(stderr.String(), "nix not on PATH") {
		t.Errorf("stderr: want 'nix not on PATH' diagnostic, got %q", stderr.String())
	}
}

func TestRun_wrappedRunnerError_returns2(t *testing.T) {
	r := &fakeRunner{err: errors.New("fork: resource exhausted")}
	var stderr bytes.Buffer
	got := Run(Config{Root: "/r"}, r.Run, io.Discard, &stderr)
	if got != 2 {
		t.Fatalf("exit code: got %d, want 2", got)
	}
	if !strings.Contains(stderr.String(), "fork: resource exhausted") {
		t.Errorf("stderr: want underlying err echoed, got %q", stderr.String())
	}
}

func TestRun_emptyRoot_returns2(t *testing.T) {
	r := &fakeRunner{code: 0}
	var stderr bytes.Buffer
	got := Run(Config{Root: ""}, r.Run, io.Discard, &stderr)
	if got != 2 {
		t.Fatalf("exit code: got %d, want 2", got)
	}
	if len(r.calls) != 0 {
		t.Errorf("runner should not be called when root is empty; got %d calls", len(r.calls))
	}
	if !strings.Contains(stderr.String(), "root is empty") {
		t.Errorf("stderr: want 'root is empty' diagnostic, got %q", stderr.String())
	}
}

func TestLocalRunner_propagatesNonZeroExit(t *testing.T) {
	code, err := LocalRunner("", "sh", []string{"-c", "exit 3"}, io.Discard, io.Discard)
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if code != 3 {
		t.Errorf("exit code: got %d, want 3", code)
	}
}

func TestLocalRunner_zeroExit(t *testing.T) {
	code, err := LocalRunner("", "sh", []string{"-c", "exit 0"}, io.Discard, io.Discard)
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if code != 0 {
		t.Errorf("exit code: got %d, want 0", code)
	}
}

func TestLocalRunner_binaryNotFound_returnsErrNotFound(t *testing.T) {
	_, err := LocalRunner("", "definitely-not-a-real-binary-zzz", nil, io.Discard, io.Discard)
	if !errors.Is(err, exec.ErrNotFound) {
		t.Errorf("err: got %v, want exec.ErrNotFound", err)
	}
}
