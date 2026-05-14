package codex

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestSessionStart_CarryOver_Stuck(t *testing.T) {
	dir := t.TempDir()
	stateDir := filepath.Join(dir, "state")
	os.MkdirAll(stateDir, 0o700)
	briefPath := filepath.Join(dir, "brief.md")
	if err := os.WriteFile(briefPath, []byte("ROLE BRIEF\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	tpath := writeCodexJSONL(t, dir, []string{
		`{"type":"session_meta","payload":{"id":"sess-resume"}}`,
		`{"type":"response_item","payload":{"type":"function_call","name":"exec_command","call_id":"call_K"}}`,
	})

	cfg := SessionStartConfig{
		Inbox:               stateDir,
		CoordinatorStateDir: stateDir,
		BriefPath:           briefPath,
		Now:                 func() time.Time { return time.Date(2026, 5, 14, 12, 0, 0, 0, time.UTC) },
	}
	payload := fmt.Sprintf(`{"session_id":"sess-resume","transcript_path":"%s"}`, tpath)
	res, err := SessionStart(cfg, strings.NewReader(payload))
	if err != nil {
		t.Fatal(err)
	}
	if res.Skipped {
		t.Fatalf("expected output")
	}

	var out struct {
		HookSpecificOutput struct {
			AdditionalContext string `json:"additionalContext"`
		} `json:"hookSpecificOutput"`
	}
	if err := json.Unmarshal(res.JSON, &out); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	got := out.HookSpecificOutput.AdditionalContext
	if !strings.HasPrefix(got, "ROLE BRIEF\n") {
		t.Errorf("brief should remain at top: %q", got)
	}
	for _, want := range []string{
		"## Carry-over tool call(s) from prior turn",
		"function_call `exec_command`",
		"call_id=call_K",
		"do not re-dispatch",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("additionalContext missing %q: %s", want, got)
		}
	}
}

func TestSessionStart_CarryOver_Clean(t *testing.T) {
	dir := t.TempDir()
	stateDir := filepath.Join(dir, "state")
	os.MkdirAll(stateDir, 0o700)
	briefPath := filepath.Join(dir, "brief.md")
	os.WriteFile(briefPath, []byte("BRIEF\n"), 0o600)
	tpath := writeCodexJSONL(t, dir, []string{
		`{"type":"session_meta","payload":{"id":"sess-clean"}}`,
		`{"type":"response_item","payload":{"type":"function_call","name":"exec","call_id":"c1"}}`,
		`{"type":"response_item","payload":{"type":"function_call_output","call_id":"c1","output":"ok"}}`,
	})
	cfg := SessionStartConfig{
		Inbox:               stateDir,
		CoordinatorStateDir: stateDir,
		BriefPath:           briefPath,
	}
	payload := fmt.Sprintf(`{"session_id":"sess-clean","transcript_path":"%s"}`, tpath)
	res, err := SessionStart(cfg, strings.NewReader(payload))
	if err != nil {
		t.Fatal(err)
	}
	var out struct {
		HookSpecificOutput struct {
			AdditionalContext string `json:"additionalContext"`
		} `json:"hookSpecificOutput"`
	}
	json.Unmarshal(res.JSON, &out)
	if strings.Contains(out.HookSpecificOutput.AdditionalContext, "Carry-over") {
		t.Errorf("clean transcript should not produce carry-over block: %s", out.HookSpecificOutput.AdditionalContext)
	}
}

func TestSessionStart_CarryOver_NoTranscriptPathSkips(t *testing.T) {
	dir := t.TempDir()
	briefPath := filepath.Join(dir, "brief.md")
	os.WriteFile(briefPath, []byte("BRIEF\n"), 0o600)
	cfg := SessionStartConfig{
		Inbox:               dir,
		CoordinatorStateDir: dir,
		BriefPath:           briefPath,
	}
	res, err := SessionStart(cfg, strings.NewReader(`{"session_id":"sess"}`))
	if err != nil {
		t.Fatal(err)
	}
	var out struct {
		HookSpecificOutput struct {
			AdditionalContext string `json:"additionalContext"`
		} `json:"hookSpecificOutput"`
	}
	json.Unmarshal(res.JSON, &out)
	if out.HookSpecificOutput.AdditionalContext != "BRIEF\n" {
		t.Errorf("missing transcript_path should leave brief untouched, got %q", out.HookSpecificOutput.AdditionalContext)
	}
}

func TestSessionStart_CarryOver_TranscriptUnreadableSkips(t *testing.T) {
	dir := t.TempDir()
	briefPath := filepath.Join(dir, "brief.md")
	os.WriteFile(briefPath, []byte("BRIEF\n"), 0o600)
	cfg := SessionStartConfig{
		Inbox:               dir,
		CoordinatorStateDir: dir,
		BriefPath:           briefPath,
	}
	payload := `{"session_id":"sess","transcript_path":"/nonexistent/path.jsonl"}`
	res, err := SessionStart(cfg, strings.NewReader(payload))
	if err != nil {
		t.Fatal(err)
	}
	var out struct {
		HookSpecificOutput struct {
			AdditionalContext string `json:"additionalContext"`
		} `json:"hookSpecificOutput"`
	}
	json.Unmarshal(res.JSON, &out)
	if strings.Contains(out.HookSpecificOutput.AdditionalContext, "Carry-over") {
		t.Errorf("unreadable transcript should not produce carry-over: %s", out.HookSpecificOutput.AdditionalContext)
	}
}
