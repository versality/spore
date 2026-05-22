package main

import (
	"io"
	"os"
	"strings"
	"testing"
)

// silenceStdio swaps stdout + stderr to a pipe drained into a buffer
// so a smoke test can probe dispatcher exits without polluting the
// `go test` output stream. The returned cleanup restores the originals
// and returns the captured combined output.
func silenceStdio(t *testing.T) func() string {
	t.Helper()
	origOut, origErr := os.Stdout, os.Stderr
	rOut, wOut, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe: %v", err)
	}
	rErr, wErr, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe: %v", err)
	}
	os.Stdout = wOut
	os.Stderr = wErr
	doneOut := make(chan string, 1)
	doneErr := make(chan string, 1)
	go func() {
		b, _ := io.ReadAll(rOut)
		doneOut <- string(b)
	}()
	go func() {
		b, _ := io.ReadAll(rErr)
		doneErr <- string(b)
	}()
	return func() string {
		_ = wOut.Close()
		_ = wErr.Close()
		os.Stdout = origOut
		os.Stderr = origErr
		return <-doneOut + <-doneErr
	}
}

// TestDispatchersSmoke walks the six int-returning dispatchers in
// search/secret/opencode/merge/signal/bootstrap and asserts each
// produces the expected exit code for the three universal entry
// surfaces: no args (usage to stderr, rc=2), --help (rc=0), bogus
// subcommand (rc=2). Bootstrap returns error; covered in its own
// case below.
func TestDispatchersSmoke(t *testing.T) {
	intDispatchers := []struct {
		name string
		run  func([]string) int
	}{
		{"search", runSearch},
		{"secret", runSecret},
		{"opencode", runOpencode},
		{"merge", runMerge},
		{"signal", runSignal},
	}
	for _, d := range intDispatchers {
		t.Run(d.name+"/no_args", func(t *testing.T) {
			drain := silenceStdio(t)
			rc := d.run(nil)
			out := drain()
			if rc != 2 {
				t.Fatalf("rc = %d, want 2 (usage to stderr)", rc)
			}
			if !strings.Contains(out, "spore "+d.name) {
				t.Errorf("usage banner missing %q in output: %s", "spore "+d.name, out)
			}
		})
		t.Run(d.name+"/help", func(t *testing.T) {
			drain := silenceStdio(t)
			rc := d.run([]string{"--help"})
			out := drain()
			if rc != 0 {
				t.Fatalf("rc = %d, want 0 on --help", rc)
			}
			if !strings.Contains(out, "spore "+d.name) {
				t.Errorf("usage banner missing in --help output: %s", out)
			}
		})
		t.Run(d.name+"/unknown_subcommand", func(t *testing.T) {
			drain := silenceStdio(t)
			rc := d.run([]string{"definitely-not-a-real-subcommand"})
			out := drain()
			if rc != 2 {
				t.Fatalf("rc = %d, want 2 for unknown subcommand", rc)
			}
			if !strings.Contains(out, "unknown subcommand") {
				t.Errorf("expected 'unknown subcommand' in stderr: %s", out)
			}
		})
	}
}

func TestBootstrapSmoke(t *testing.T) {
	// runBootstrap returns error and dispatches to stage gates; the
	// safe smoke is the status / unknown / help subset that does not
	// shell out to git or write to a real project.
	t.Run("help_no_error", func(t *testing.T) {
		drain := silenceStdio(t)
		err := runBootstrap([]string{"--help"})
		out := drain()
		if err != nil {
			t.Fatalf("runBootstrap --help: %v", err)
		}
		if !strings.Contains(out, "spore bootstrap") {
			t.Errorf("usage missing in --help output: %s", out)
		}
	})
	t.Run("unknown_subcommand", func(t *testing.T) {
		drain := silenceStdio(t)
		err := runBootstrap([]string{"definitely-not-a-stage"})
		_ = drain()
		if err == nil {
			t.Fatal("expected error on unknown subcommand")
		}
	})
}
