package hooks

import (
	"errors"
	"os/exec"
	"testing"
)

func TestGateKind_MissOnEmptyEnv(t *testing.T) {
	err := GateKind([]string{"coordinator", "--", "/bin/true"}, func(string) string { return "" })
	if !errors.Is(err, ErrGateMiss) {
		t.Errorf("got %v, want ErrGateMiss", err)
	}
}

func TestGateKind_MissOnUnrelatedKind(t *testing.T) {
	err := GateKind([]string{"coordinator", "--", "/bin/true"}, func(string) string { return "worker" })
	if !errors.Is(err, ErrGateMiss) {
		t.Errorf("got %v, want ErrGateMiss", err)
	}
}

func lookOrSkip(t *testing.T, name string) string {
	t.Helper()
	path, err := exec.LookPath(name)
	if err != nil {
		t.Skipf("%s unavailable: %v", name, err)
	}
	return path
}

func TestGateKind_MatchExecsCmd(t *testing.T) {
	bin := lookOrSkip(t, "true")
	err := GateKind([]string{"worker", "--", bin}, func(string) string { return "worker" })
	if err != nil {
		t.Errorf("worker should match and %s should exit 0, got %v", bin, err)
	}
}

func TestGateKind_MultipleKinds(t *testing.T) {
	bin := lookOrSkip(t, "true")
	err := GateKind([]string{"coordinator", "worker", "--", bin},
		func(string) string { return "worker" })
	if err != nil {
		t.Errorf("worker should match coordinator|worker, got %v", err)
	}
}

func TestGateKind_PropagatesInnerExitCode(t *testing.T) {
	bin := lookOrSkip(t, "false")
	err := GateKind([]string{"worker", "--", bin}, func(string) string { return "worker" })
	var exitErr *GateKindExitError
	if !errors.As(err, &exitErr) {
		t.Fatalf("got %v, want *GateKindExitError", err)
	}
	if exitErr.Code != 1 {
		t.Errorf("inner exit code = %d, want 1", exitErr.Code)
	}
}

func TestGateKind_Usage(t *testing.T) {
	cases := [][]string{
		nil,
		{"coordinator"},
		{"--", "/bin/true"},          // no kinds
		{"coordinator", "--"},        // no command
		{"coordinator", "/bin/true"}, // missing -- separator
	}
	for i, args := range cases {
		err := GateKind(args, func(string) string { return "worker" })
		if !errors.Is(err, ErrGateUsage) {
			t.Errorf("case %d: args=%v: got %v, want ErrGateUsage", i, args, err)
		}
	}
}
