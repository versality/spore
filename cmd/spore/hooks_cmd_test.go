package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestHooksNotifyCoordinatorNoArgsUsesEnv(t *testing.T) {
	state := t.TempDir()
	t.Setenv("SPORE_COORDINATOR_STATE_DIR", state)
	t.Setenv("WT_PROJECT", "project")
	t.Setenv("SPORE_TASK_INBOX", filepath.Join(t.TempDir(), "worker", "inbox"))

	if code := runHooksNotifyCoordinator(nil); code != 0 {
		t.Fatalf("runHooksNotifyCoordinator(nil) = %d, want 0", code)
	}
	entries, err := os.ReadDir(filepath.Join(state, "project", "inbox"))
	if err != nil {
		t.Fatalf("read coordinator inbox: %v", err)
	}
	found := false
	for _, entry := range entries {
		if !entry.IsDir() && strings.HasSuffix(entry.Name(), ".json") {
			found = true
		}
	}
	if !found {
		t.Fatal("notify-coordinator env mode did not write a json poke")
	}
}

func TestHooksWatchInboxNoArgsRequiresEnv(t *testing.T) {
	t.Setenv("SPORE_TASK_INBOX", "")

	if code := runHooksWatchInbox(nil); code != 2 {
		t.Fatalf("runHooksWatchInbox(nil) = %d, want 2 without SPORE_TASK_INBOX", code)
	}
}

func TestHooksSettingsKindFilter(t *testing.T) {
	input := `{
	  "events": {
	    "Stop": [
	      {"command": "/bin/coord-watch", "kinds": ["coordinator"]},
	      {"command": "/bin/token-monitor", "kinds": ["worker"]},
	      {"command": "/bin/lint-noise"}
	    ]
	  }
	}`

	cases := []struct {
		name      string
		argsOrEnv func(t *testing.T) []string
		want      []string
		notWant   []string
	}{
		{
			name:      "flag coordinator",
			argsOrEnv: func(t *testing.T) []string { return []string{"--kind", "coordinator"} },
			want:      []string{"/bin/coord-watch", "/bin/lint-noise"},
			notWant:   []string{"/bin/token-monitor"},
		},
		{
			name: "env worker",
			argsOrEnv: func(t *testing.T) []string {
				t.Setenv("SPORE_RENDER_KIND", "worker")
				return nil
			},
			want:    []string{"/bin/token-monitor", "/bin/lint-noise"},
			notWant: []string{"/bin/coord-watch"},
		},
		{
			name:      "kind=operator drops every fleet hook",
			argsOrEnv: func(t *testing.T) []string { return []string{"--kind=operator"} },
			want:      []string{"/bin/lint-noise"},
			notWant:   []string{"/bin/coord-watch", "/bin/token-monitor"},
		},
		{
			name:      "no kind keeps everything",
			argsOrEnv: func(t *testing.T) []string { return nil },
			want:      []string{"/bin/coord-watch", "/bin/token-monitor", "/bin/lint-noise"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("SPORE_RENDER_KIND", "")
			args := tc.argsOrEnv(t)

			oldIn, oldOut := os.Stdin, os.Stdout
			rIn, wIn, err := os.Pipe()
			if err != nil {
				t.Fatal(err)
			}
			rOut, wOut, err := os.Pipe()
			if err != nil {
				t.Fatal(err)
			}
			os.Stdin = rIn
			os.Stdout = wOut
			t.Cleanup(func() {
				os.Stdin = oldIn
				os.Stdout = oldOut
			})

			go func() {
				_, _ = wIn.Write([]byte(input))
				_ = wIn.Close()
			}()

			done := make(chan int, 1)
			go func() {
				code := runHooksSettings(args)
				_ = wOut.Close()
				done <- code
			}()

			outBytes, err := readAll(rOut)
			if err != nil {
				t.Fatal(err)
			}
			if code := <-done; code != 0 {
				t.Fatalf("runHooksSettings exit = %d, want 0", code)
			}
			out := string(outBytes)
			for _, w := range tc.want {
				if !strings.Contains(out, w) {
					t.Errorf("missing %q in output:\n%s", w, out)
				}
			}
			for _, n := range tc.notWant {
				if strings.Contains(out, n) {
					t.Errorf("unexpected %q in output:\n%s", n, out)
				}
			}
			if strings.Contains(out, `"kinds"`) {
				t.Errorf("rendered output leaks the kinds field:\n%s", out)
			}
		})
	}
}

func readAll(r *os.File) ([]byte, error) {
	var buf [4096]byte
	var out []byte
	for {
		n, err := r.Read(buf[:])
		if n > 0 {
			out = append(out, buf[:n]...)
		}
		if err != nil {
			if err.Error() == "EOF" {
				return out, nil
			}
			return out, err
		}
	}
}
