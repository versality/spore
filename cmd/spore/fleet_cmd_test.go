package main

import (
	"bytes"
	"path/filepath"
	"strings"
	"testing"
)

func TestIsCoordinatorSession(t *testing.T) {
	state := t.TempDir()
	cases := []struct {
		name     string
		envInbox string
		envCoord string
		want     bool
	}{
		{"empty inbox is not coordinator", "", state, false},
		{"inbox exactly the state dir", state, state, true},
		{"inbox under state dir", filepath.Join(state, "proj/inbox"), state, true},
		{"inbox unrelated to state dir", "/tmp/worker-x/inbox", state, false},
		{"trailing slash on state dir is normalised", filepath.Join(state, "proj/inbox"), state + "/", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("SPORE_TASK_INBOX", tc.envInbox)
			t.Setenv("SPORE_COORDINATOR_STATE_DIR", tc.envCoord)
			if got := isCoordinatorSession(); got != tc.want {
				t.Errorf("isCoordinatorSession() = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestResolveMaxWorkers_HonoursWTFleetFloor(t *testing.T) {
	t.Setenv("SPORE_FLEET_MAX_WORKERS", "")
	t.Setenv("WT_FLEET_FLOOR", "8")
	root := t.TempDir()
	got, err := resolveMaxWorkers(0, root, nil)
	if err != nil {
		t.Fatalf("resolveMaxWorkers: %v", err)
	}
	if got != 8 {
		t.Errorf("got %d, want 8 (from WT_FLEET_FLOOR)", got)
	}
}

func TestResolveMaxWorkers_SporeMaxBeatsWTFloor(t *testing.T) {
	t.Setenv("SPORE_FLEET_MAX_WORKERS", "4")
	t.Setenv("WT_FLEET_FLOOR", "8")
	root := t.TempDir()
	got, err := resolveMaxWorkers(0, root, nil)
	if err != nil {
		t.Fatalf("resolveMaxWorkers: %v", err)
	}
	if got != 4 {
		t.Errorf("got %d, want 4 (SPORE_FLEET_MAX_WORKERS wins)", got)
	}
}

func TestResolveMaxWorkers_BadWTFloorErrors(t *testing.T) {
	t.Setenv("SPORE_FLEET_MAX_WORKERS", "")
	t.Setenv("WT_FLEET_FLOOR", "0")
	root := t.TempDir()
	if _, err := resolveMaxWorkers(0, root, nil); err == nil {
		t.Error("expected error for WT_FLEET_FLOOR=0")
	}
}

func TestResolveMaxWorkers_FlagVsEnvPrecedence(t *testing.T) {
	cases := []struct {
		name        string
		flag        int
		env         string
		want        int
		wantWarn    bool
		warnSubstrs []string
	}{
		{name: "flag only", flag: 5, env: "", want: 5},
		{name: "env only", flag: 0, env: "12", want: 12},
		{name: "flag and env equal", flag: 7, env: "7", want: 7},
		{
			name:        "flag and env disagree",
			flag:        5,
			env:         "12",
			want:        5,
			wantWarn:    true,
			warnSubstrs: []string{"--max-workers=5", "SPORE_FLEET_MAX_WORKERS=12"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("SPORE_FLEET_MAX_WORKERS", tc.env)
			t.Setenv("WT_FLEET_FLOOR", "")
			root := t.TempDir()
			var buf bytes.Buffer
			got, err := resolveMaxWorkers(tc.flag, root, &buf)
			if err != nil {
				t.Fatalf("resolveMaxWorkers: %v", err)
			}
			if got != tc.want {
				t.Errorf("got %d, want %d", got, tc.want)
			}
			out := buf.String()
			if tc.wantWarn {
				if out == "" {
					t.Fatalf("expected a warning line, got none")
				}
				if n := strings.Count(out, "\n"); n != 1 {
					t.Errorf("expected exactly one warning line, got %d (%q)", n, out)
				}
				for _, sub := range tc.warnSubstrs {
					if !strings.Contains(out, sub) {
						t.Errorf("warning %q missing substring %q", out, sub)
					}
				}
			} else if out != "" {
				t.Errorf("expected no warning, got %q", out)
			}
		})
	}
}

// runMatterSync was the prior CLI-level wrapper around the Linear
// adapter. The matter sync now runs inside fleet.Reconcile (see
// internal/fleet/matter_test.go) and the Linear adapter has its own
// integration suite (internal/matter/linear/linear_test.go), so the
// CLI no longer owns its own coverage of the matter pass.
