package main

import (
	"path/filepath"
	"testing"
)

func TestIsCoordinatorSession(t *testing.T) {
	state := t.TempDir()
	cases := []struct {
		name     string
		envInbox string
		envState string
		envCoord string
		want     bool
	}{
		{"empty inbox is not coordinator", "", state, "", false},
		{"inbox exactly the legacy state dir", state, state, "", true},
		{"inbox under legacy state dir", filepath.Join(state, "proj/inbox"), state, "", true},
		{"inbox under kernel-neutral state dir", filepath.Join(state, "proj/inbox"), "", state, true},
		{"inbox unrelated to state dirs", "/tmp/rower-x/inbox", state, state, false},
		{"trailing slash on state dir is normalised", filepath.Join(state, "proj/inbox"), state + "/", "", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("SKYBOT_INBOX", tc.envInbox)
			t.Setenv("SKYHELM_STATE_DIR", tc.envState)
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
	got, err := resolveMaxWorkers(0, root)
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
	got, err := resolveMaxWorkers(0, root)
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
	if _, err := resolveMaxWorkers(0, root); err == nil {
		t.Error("expected error for WT_FLEET_FLOOR=0")
	}
}
