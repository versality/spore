package tokenmonitor

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestIsCoordinator(t *testing.T) {
	cases := []struct {
		inbox    string
		stateDir string
		want     bool
	}{
		{"/state/coord", "/state/coord", true},
		{"/state/coord/some/inbox", "/state/coord", true},
		{"/state/workers/slug/inbox", "/state/coord", false},
		{"", "/state/coord", false},
	}
	for _, tc := range cases {
		cfg := Config{Inbox: tc.inbox, CoordinatorStateDir: tc.stateDir}
		if got := cfg.IsCoordinator(); got != tc.want {
			t.Errorf("IsCoordinator(%q, %q) = %v, want %v", tc.inbox, tc.stateDir, got, tc.want)
		}
	}
}

func TestSlug(t *testing.T) {
	cases := []struct {
		inbox string
		want  string
	}{
		{"/state/workers/my-slug/inbox", "my-slug"},
		{"/anything/abc/inbox", "abc"},
		{"", ""},
		{"/", ""},
	}
	for _, tc := range cases {
		cfg := Config{Inbox: tc.inbox}
		if got := cfg.Slug(); got != tc.want {
			t.Errorf("Slug(%q) = %q, want %q", tc.inbox, got, tc.want)
		}
	}
}

func TestWrapCap(t *testing.T) {
	cases := []struct {
		name string
		cfg  Config
		want int
	}{
		{"override beats tier", Config{WrapOverride: 99000, Tier: "max"}, 99000},
		{"max tier", Config{Tier: "max"}, DefaultWrapMax},
		{"sub tier", Config{Tier: "pro"}, DefaultWrapSub},
		{"unknown tier", Config{Tier: ""}, DefaultWrapSub},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cfg := tc.cfg.Defaults()
			if got := cfg.WrapCap(); got != tc.want {
				t.Errorf("WrapCap = %d, want %d", got, tc.want)
			}
		})
	}
}

func TestCheckSkipsCoordinator(t *testing.T) {
	stateDir := t.TempDir()
	cfg := Config{
		Inbox:               filepath.Join(stateDir, "myproject", "inbox"),
		CoordinatorStateDir: stateDir,
	}
	if got := Check(cfg, HookPayload{}); got.Level != "skip" {
		t.Errorf("expected skip for coordinator inbox, got %s", got.Level)
	}
}

func TestCheckSkipsEmptyInbox(t *testing.T) {
	if got := Check(Config{}, HookPayload{}); got.Level != "skip" {
		t.Errorf("expected skip for empty inbox, got %s", got.Level)
	}
}

func TestCheckSkipsBadInboxLayout(t *testing.T) {
	cfg := Config{
		Inbox:               "/inbox",
		CoordinatorStateDir: "/never",
	}
	if got := Check(cfg, HookPayload{}); got.Level != "skip" {
		t.Errorf("expected skip for bad inbox layout, got %s", got.Level)
	}
}

func TestCheckOk(t *testing.T) {
	dir := t.TempDir()
	transcriptFile := filepath.Join(dir, "session.jsonl")
	line := `{"role":"assistant","usage":{"input_tokens":50000,"cache_creation_input_tokens":0,"cache_read_input_tokens":0}}`
	if err := os.WriteFile(transcriptFile, []byte(line+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	cfg := Config{
		Inbox:               filepath.Join(dir, "workers", "test-slug", "inbox"),
		CoordinatorStateDir: filepath.Join(dir, "coord"),
		Tier:                "max",
	}
	got := Check(cfg, HookPayload{SessionID: "s", TranscriptPath: transcriptFile})
	if got.Level != "ok" {
		t.Errorf("Level = %s, want ok", got.Level)
	}
	if got.ShouldFire {
		t.Error("ShouldFire = true, want false")
	}
	if got.Slug != "test-slug" {
		t.Errorf("Slug = %q, want %q", got.Slug, "test-slug")
	}
}

func TestCheckWrapMax(t *testing.T) {
	dir := t.TempDir()
	transcriptFile := filepath.Join(dir, "session.jsonl")
	line := `{"role":"assistant","usage":{"input_tokens":190000,"cache_creation_input_tokens":0,"cache_read_input_tokens":0}}`
	if err := os.WriteFile(transcriptFile, []byte(line+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	cfg := Config{
		Inbox:               filepath.Join(dir, "workers", "wrap-slug", "inbox"),
		CoordinatorStateDir: filepath.Join(dir, "coord"),
		Tier:                "max",
	}
	got := Check(cfg, HookPayload{TranscriptPath: transcriptFile})
	if got.Level != "wrap" {
		t.Errorf("Level = %s, want wrap", got.Level)
	}
	if !got.ShouldFire {
		t.Error("ShouldFire = false, want true")
	}
	if got.WrapCap != DefaultWrapMax {
		t.Errorf("WrapCap = %d, want %d", got.WrapCap, DefaultWrapMax)
	}
	if !strings.Contains(got.Message, "wrap-slug") {
		t.Errorf("Message missing slug: %q", got.Message)
	}
	if !strings.Contains(got.Message, "tier=max") {
		t.Errorf("Message missing tier: %q", got.Message)
	}
}

func TestCheckWrapSubTier(t *testing.T) {
	dir := t.TempDir()
	transcriptFile := filepath.Join(dir, "session.jsonl")
	line := `{"role":"assistant","usage":{"input_tokens":121000,"cache_creation_input_tokens":0,"cache_read_input_tokens":0}}`
	if err := os.WriteFile(transcriptFile, []byte(line+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	cfg := Config{
		Inbox:               filepath.Join(dir, "workers", "sub-slug", "inbox"),
		CoordinatorStateDir: filepath.Join(dir, "coord"),
		Tier:                "pro",
	}
	got := Check(cfg, HookPayload{TranscriptPath: transcriptFile})
	if got.Level != "wrap" {
		t.Errorf("Level = %s, want wrap", got.Level)
	}
	if got.WrapCap != DefaultWrapSub {
		t.Errorf("WrapCap = %d, want %d", got.WrapCap, DefaultWrapSub)
	}
	if !strings.Contains(got.Message, "tier=pro") {
		t.Errorf("Message missing tier: %q", got.Message)
	}
}

func TestCheckWrapUnknownTierUsesSub(t *testing.T) {
	dir := t.TempDir()
	transcriptFile := filepath.Join(dir, "session.jsonl")
	line := `{"role":"assistant","usage":{"input_tokens":121000,"cache_creation_input_tokens":0,"cache_read_input_tokens":0}}`
	if err := os.WriteFile(transcriptFile, []byte(line+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	cfg := Config{
		Inbox:               filepath.Join(dir, "workers", "u", "inbox"),
		CoordinatorStateDir: filepath.Join(dir, "coord"),
	}
	got := Check(cfg, HookPayload{TranscriptPath: transcriptFile})
	if got.Level != "wrap" {
		t.Errorf("Level = %s, want wrap", got.Level)
	}
	if got.WrapCap != DefaultWrapSub {
		t.Errorf("WrapCap = %d, want %d (unknown tier should default to sub)", got.WrapCap, DefaultWrapSub)
	}
	if !strings.Contains(got.Message, "tier=unknown") {
		t.Errorf("Message tier rendering: %q", got.Message)
	}
}

func TestCheckWrapOverride(t *testing.T) {
	dir := t.TempDir()
	transcriptFile := filepath.Join(dir, "session.jsonl")
	line := `{"role":"assistant","usage":{"input_tokens":50000,"cache_creation_input_tokens":0,"cache_read_input_tokens":0}}`
	if err := os.WriteFile(transcriptFile, []byte(line+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	cfg := Config{
		Inbox:               filepath.Join(dir, "workers", "tiny", "inbox"),
		CoordinatorStateDir: filepath.Join(dir, "coord"),
		Tier:                "max",
		WrapOverride:        40000,
	}
	got := Check(cfg, HookPayload{TranscriptPath: transcriptFile})
	if got.Level != "wrap" {
		t.Errorf("Level = %s, want wrap", got.Level)
	}
	if got.WrapCap != 40000 {
		t.Errorf("WrapCap = %d, want 40000", got.WrapCap)
	}
}

func TestCheckMissingTranscript(t *testing.T) {
	dir := t.TempDir()
	cfg := Config{
		Inbox:               filepath.Join(dir, "workers", "slug", "inbox"),
		CoordinatorStateDir: filepath.Join(dir, "coord"),
	}
	t.Setenv("HOME", dir)
	t.Setenv("CLAUDE_PROJECT_DIR", "/no/such")
	got := Check(cfg, HookPayload{})
	if got.Level != "skip" {
		t.Errorf("Level = %s, want skip", got.Level)
	}
}
