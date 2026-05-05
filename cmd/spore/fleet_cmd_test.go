package main

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
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

func TestRunMatterSyncNoConfig(t *testing.T) {
	root := t.TempDir()
	var buf bytes.Buffer
	if err := runMatterSync(root, "tasks", &buf); err != nil {
		t.Fatalf("runMatterSync: %v", err)
	}
	if buf.Len() != 0 {
		t.Errorf("expected no output without config, got %q", buf.String())
	}
}

func TestRunMatterSyncWithLinearConfig(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		body := string(raw)
		switch {
		case strings.Contains(body, "workflowStates"):
			_ = json.NewEncoder(w).Encode(map[string]any{
				"data": map[string]any{
					"workflowStates": map[string]any{
						"nodes": []map[string]string{
							{"id": "s-ready", "name": "Ready"},
							{"id": "s-doing", "name": "In Progress"},
							{"id": "s-done", "name": "Done"},
						},
					},
				},
			})
		case strings.Contains(body, "issueUpdate"):
			_ = json.NewEncoder(w).Encode(map[string]any{
				"data": map[string]any{
					"issueUpdate": map[string]any{"success": true},
				},
			})
		case strings.Contains(body, "issues("):
			_ = json.NewEncoder(w).Encode(map[string]any{
				"data": map[string]any{
					"issues": map[string]any{
						"nodes": []map[string]string{
							{
								"id":          "uuid-1",
								"identifier":  "MAR-1",
								"title":       "First task",
								"description": "do the thing",
								"url":         "https://linear.app/x/issue/MAR-1",
							},
						},
					},
				},
			})
		}
	}))
	defer srv.Close()

	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "tasks"), 0o755); err != nil {
		t.Fatal(err)
	}
	tomlBody := "[matter.linear]\n" +
		"team = \"MAR\"\n" +
		"api_key_env = \"LINEAR_API_KEY\"\n" +
		"endpoint = \"" + srv.URL + "\"\n"
	if err := os.WriteFile(filepath.Join(root, "spore.toml"), []byte(tomlBody), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("LINEAR_API_KEY", "lin_test_main")

	var buf bytes.Buffer
	if err := runMatterSync(root, "tasks", &buf); err != nil {
		t.Fatalf("runMatterSync: %v", err)
	}
	if !strings.Contains(buf.String(), "matter[linear]:") {
		t.Errorf("output missing matter line: %q", buf.String())
	}
	if !strings.Contains(buf.String(), "adopted: MAR-1") {
		t.Errorf("output missing adopted: %q", buf.String())
	}
	entries, err := os.ReadDir(filepath.Join(root, "tasks"))
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		t.Fatalf("expected one task file, got %d (%v)", len(entries), entries)
	}
}
