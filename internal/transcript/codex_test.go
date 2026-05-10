package transcript

import (
	"os"
	"path/filepath"
	"testing"
)

func writeCodexJSONL(t *testing.T, lines []string) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "rollout.jsonl")
	body := ""
	for _, l := range lines {
		body += l + "\n"
	}
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	return path
}

func TestCodexSessionID(t *testing.T) {
	path := writeCodexJSONL(t, []string{
		`{"type":"session_meta","payload":{"id":"abc-123","cwd":"/x"}}`,
		`{"type":"token_count","last_token_usage":{"total_tokens":42}}`,
	})
	got, ok := CodexSessionID(path)
	if !ok {
		t.Fatalf("CodexSessionID ok=false")
	}
	if got != "abc-123" {
		t.Fatalf("CodexSessionID = %q, want abc-123", got)
	}
}

func TestCodexSessionID_Missing(t *testing.T) {
	path := writeCodexJSONL(t, []string{
		`{"type":"token_count","last_token_usage":{"total_tokens":42}}`,
	})
	if _, ok := CodexSessionID(path); ok {
		t.Fatalf("CodexSessionID ok=true on file without session_meta")
	}
}

func TestCodexSessionID_FileMissing(t *testing.T) {
	if _, ok := CodexSessionID("/nonexistent/path"); ok {
		t.Fatalf("CodexSessionID ok=true on missing file")
	}
}

func TestCodexLastTokenCount(t *testing.T) {
	path := writeCodexJSONL(t, []string{
		`{"type":"session_meta","payload":{"id":"s"}}`,
		`{"type":"token_count","last_token_usage":{"total_tokens":1000}}`,
		`{"type":"event_msg"}`,
		`{"type":"token_count","last_token_usage":{"total_tokens":2500}}`,
	})
	got, ok := CodexLastTokenCount(path)
	if !ok {
		t.Fatalf("ok=false")
	}
	if got != 2500 {
		t.Fatalf("got %d, want 2500", got)
	}
}

func TestCodexLastTokenCount_None(t *testing.T) {
	path := writeCodexJSONL(t, []string{
		`{"type":"session_meta","payload":{"id":"s"}}`,
	})
	if _, ok := CodexLastTokenCount(path); ok {
		t.Fatalf("ok=true with no token_count line")
	}
}

func TestCodexLastTokenCount_BadJSON(t *testing.T) {
	path := writeCodexJSONL(t, []string{
		`not-json`,
		`{"type":"token_count","last_token_usage":{"total_tokens":7}}`,
		`also-not-json`,
	})
	got, ok := CodexLastTokenCount(path)
	if !ok || got != 7 {
		t.Fatalf("got %d ok=%v, want 7 true", got, ok)
	}
}
