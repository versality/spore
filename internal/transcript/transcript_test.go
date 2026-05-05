package transcript

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestExtractContextFromLine(t *testing.T) {
	line := `{"type":"assistant","message":{"role":"assistant","content":[],"usage":{"input_tokens":100000,"output_tokens":5000,"cache_creation_input_tokens":20000,"cache_read_input_tokens":30000}}}`
	got := ExtractContextFromLine(line)
	want := 150000
	if got != want {
		t.Errorf("ExtractContextFromLine = %d, want %d", got, want)
	}
}

func TestExtractContextFromLineNoUsage(t *testing.T) {
	line := `{"type":"assistant","message":{"role":"assistant","content":[]}}`
	if got := ExtractContextFromLine(line); got != 0 {
		t.Errorf("expected 0, got %d", got)
	}
}

func TestExtractContextFromLineMultipleUsageBlocks(t *testing.T) {
	// Streaming transcripts can include intermediate usage blocks; the
	// last one is the final accounting and is what we sum.
	line := `{"role":"assistant","usage":{"input_tokens":1,"output_tokens":1},"content":[],"usage":{"input_tokens":50000,"cache_read_input_tokens":50000}}`
	if got := ExtractContextFromLine(line); got != 100000 {
		t.Errorf("expected 100000, got %d", got)
	}
}

func TestSumContextTokens(t *testing.T) {
	dir := t.TempDir()
	f := filepath.Join(dir, "transcript.jsonl")

	lines := []string{
		`{"type":"human","message":{"role":"user"}}`,
		`{"type":"assistant","message":{"role":"assistant","content":[],"usage":{"input_tokens":50000,"output_tokens":1000,"cache_creation_input_tokens":10000,"cache_read_input_tokens":5000}}}`,
		`{"type":"human","message":{"role":"user"}}`,
		`{"type":"assistant","message":{"role":"assistant","content":[],"usage":{"input_tokens":80000,"output_tokens":2000,"cache_creation_input_tokens":15000,"cache_read_input_tokens":25000}}}`,
	}
	var content []byte
	for _, l := range lines {
		content = append(content, []byte(l+"\n")...)
	}
	if err := os.WriteFile(f, content, 0o644); err != nil {
		t.Fatal(err)
	}

	if got, want := SumContextTokens(f), 120000; got != want {
		t.Errorf("SumContextTokens = %d, want %d", got, want)
	}
}

func TestSumContextTokensMissingFile(t *testing.T) {
	if got := SumContextTokens(filepath.Join(t.TempDir(), "missing.jsonl")); got != 0 {
		t.Errorf("expected 0 for missing file, got %d", got)
	}
}

func TestFindFallbackTranscript(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	cwd := "/tmp/some/project"
	t.Setenv("CLAUDE_PROJECT_DIR", cwd)

	encoded := "-tmp-some-project"
	projDir := filepath.Join(home, ".claude", "projects", encoded)
	if err := os.MkdirAll(projDir, 0o755); err != nil {
		t.Fatal(err)
	}

	older := filepath.Join(projDir, "older.jsonl")
	newer := filepath.Join(projDir, "newer.jsonl")
	if err := os.WriteFile(older, []byte("{}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(newer, []byte("{}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	past := time.Now().Add(-time.Hour)
	future := time.Now().Add(time.Hour)
	if err := os.Chtimes(older, past, past); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(newer, future, future); err != nil {
		t.Fatal(err)
	}

	if got := FindFallbackTranscript(); got != newer {
		t.Errorf("FindFallbackTranscript = %q, want %q", got, newer)
	}
}

func TestFindFallbackTranscriptMissingDir(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("CLAUDE_PROJECT_DIR", "/nope")
	if got := FindFallbackTranscript(); got != "" {
		t.Errorf("expected empty, got %q", got)
	}
}
