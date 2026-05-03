package verify

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestNewestJSONLEmpty(t *testing.T) {
	dir := t.TempDir()
	got := newestJSONL(dir)
	if got != "" {
		t.Errorf("expected empty string for empty dir, got %q", got)
	}
}

func TestNewestJSONLMissingDir(t *testing.T) {
	got := newestJSONL("/nonexistent/path/that/cannot/exist")
	if got != "" {
		t.Errorf("expected empty string for missing dir, got %q", got)
	}
}

func TestNewestJSONLPicksNewest(t *testing.T) {
	dir := t.TempDir()

	older := filepath.Join(dir, "2026-01-01T00-00-00.jsonl")
	newer := filepath.Join(dir, "2026-06-01T00-00-00.jsonl")

	if err := os.WriteFile(older, []byte("{}"), 0o644); err != nil {
		t.Fatal(err)
	}
	// Set mtime on older file to the past.
	past := time.Now().Add(-time.Hour)
	if err := os.Chtimes(older, past, past); err != nil {
		t.Fatal(err)
	}

	if err := os.WriteFile(newer, []byte("{}"), 0o644); err != nil {
		t.Fatal(err)
	}

	got := newestJSONL(dir)
	if got != newer {
		t.Errorf("got %q, want %q", got, newer)
	}
}

func TestNewestJSONLIgnoresNonJSONL(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "notes.txt"), []byte("hi"), 0o644); err != nil {
		t.Fatal(err)
	}
	got := newestJSONL(dir)
	if got != "" {
		t.Errorf("expected empty for non-jsonl file, got %q", got)
	}
}

func TestAnalyzeSessionEmptyPath(t *testing.T) {
	finalTool, finalText, lastTS, gitCommitSeen, wtMergeSeen, crossRepoPath := analyzeSession("")
	if finalTool != "none" {
		t.Errorf("finalTool = %q, want none", finalTool)
	}
	if finalText != "" {
		t.Errorf("finalText = %q, want empty", finalText)
	}
	if lastTS != "?" {
		t.Errorf("lastTS = %q, want ?", lastTS)
	}
	if gitCommitSeen || wtMergeSeen {
		t.Error("expected no git/merge activity for empty path")
	}
	if crossRepoPath != "" {
		t.Errorf("crossRepoPath = %q, want empty", crossRepoPath)
	}
}

func TestAnalyzeSessionWtMerge(t *testing.T) {
	path := writeTranscript(t, []transcriptMsg{
		{
			Type:      "assistant",
			Timestamp: "2026-05-01T10:00:00Z",
			Message: struct {
				Content []struct {
					Type  string `json:"type"`
					Text  string `json:"text,omitempty"`
					Name  string `json:"name,omitempty"`
					Input struct {
						Command string `json:"command,omitempty"`
					} `json:"input,omitempty"`
				} `json:"content"`
			}{
				Content: []struct {
					Type  string `json:"type"`
					Text  string `json:"text,omitempty"`
					Name  string `json:"name,omitempty"`
					Input struct {
						Command string `json:"command,omitempty"`
					} `json:"input,omitempty"`
				}{
					{Type: "tool_use", Name: "Bash", Input: struct {
						Command string `json:"command,omitempty"`
					}{Command: "wt merge demo"}},
				},
			},
		},
	})
	finalTool, _, _, _, wtMergeSeen, _ := analyzeSession(path)
	if finalTool != "wt-merge" {
		t.Errorf("finalTool = %q, want wt-merge", finalTool)
	}
	if !wtMergeSeen {
		t.Error("wtMergeSeen should be true")
	}
}

func TestAnalyzeSessionGitCommit(t *testing.T) {
	path := writeTranscript(t, []transcriptMsg{
		{
			Type:      "assistant",
			Timestamp: "2026-05-01T10:00:00Z",
			Message: struct {
				Content []struct {
					Type  string `json:"type"`
					Text  string `json:"text,omitempty"`
					Name  string `json:"name,omitempty"`
					Input struct {
						Command string `json:"command,omitempty"`
					} `json:"input,omitempty"`
				} `json:"content"`
			}{
				Content: []struct {
					Type  string `json:"type"`
					Text  string `json:"text,omitempty"`
					Name  string `json:"name,omitempty"`
					Input struct {
						Command string `json:"command,omitempty"`
					} `json:"input,omitempty"`
				}{
					{Type: "tool_use", Name: "Bash", Input: struct {
						Command string `json:"command,omitempty"`
					}{Command: "git commit -m 'feat: implement'"}},
				},
			},
		},
	})
	_, _, _, gitCommitSeen, _, _ := analyzeSession(path)
	if !gitCommitSeen {
		t.Error("gitCommitSeen should be true")
	}
}

func TestAnalyzeSessionFinalText(t *testing.T) {
	path := writeTranscript(t, []transcriptMsg{
		{
			Type:      "assistant",
			Timestamp: "2026-05-01T10:00:00Z",
			Message: struct {
				Content []struct {
					Type  string `json:"type"`
					Text  string `json:"text,omitempty"`
					Name  string `json:"name,omitempty"`
					Input struct {
						Command string `json:"command,omitempty"`
					} `json:"input,omitempty"`
				} `json:"content"`
			}{
				Content: []struct {
					Type  string `json:"type"`
					Text  string `json:"text,omitempty"`
					Name  string `json:"name,omitempty"`
					Input struct {
						Command string `json:"command,omitempty"`
					} `json:"input,omitempty"`
				}{
					{Type: "text", Text: "Work is done. Merged successfully."},
				},
			},
		},
	})
	_, finalText, lastTS, _, _, _ := analyzeSession(path)
	if finalText == "" {
		t.Error("finalText should be non-empty")
	}
	if lastTS != "2026-05-01T10:00:00Z" {
		t.Errorf("lastTS = %q, want 2026-05-01T10:00:00Z", lastTS)
	}
}

func TestAnalyzeSessionWtAbandon(t *testing.T) {
	path := writeTranscript(t, []transcriptMsg{
		{
			Type:      "assistant",
			Timestamp: "2026-05-01T10:00:00Z",
			Message: struct {
				Content []struct {
					Type  string `json:"type"`
					Text  string `json:"text,omitempty"`
					Name  string `json:"name,omitempty"`
					Input struct {
						Command string `json:"command,omitempty"`
					} `json:"input,omitempty"`
				} `json:"content"`
			}{
				Content: []struct {
					Type  string `json:"type"`
					Text  string `json:"text,omitempty"`
					Name  string `json:"name,omitempty"`
					Input struct {
						Command string `json:"command,omitempty"`
					} `json:"input,omitempty"`
				}{
					{Type: "tool_use", Name: "Bash", Input: struct {
						Command string `json:"command,omitempty"`
					}{Command: "wt abandon demo"}},
				},
			},
		},
	})
	finalTool, _, _, _, _, _ := analyzeSession(path)
	if finalTool != "wt-abandon" {
		t.Errorf("finalTool = %q, want wt-abandon", finalTool)
	}
}

func TestAnalyzeSessionCrossRepoPath(t *testing.T) {
	path := writeTranscript(t, []transcriptMsg{
		{
			Type:      "assistant",
			Timestamp: "2026-05-01T10:00:00Z",
			Message: struct {
				Content []struct {
					Type  string `json:"type"`
					Text  string `json:"text,omitempty"`
					Name  string `json:"name,omitempty"`
					Input struct {
						Command string `json:"command,omitempty"`
					} `json:"input,omitempty"`
				} `json:"content"`
			}{
				Content: []struct {
					Type  string `json:"type"`
					Text  string `json:"text,omitempty"`
					Name  string `json:"name,omitempty"`
					Input struct {
						Command string `json:"command,omitempty"`
					} `json:"input,omitempty"`
				}{
					{Type: "tool_use", Name: "Bash", Input: struct {
						Command string `json:"command,omitempty"`
					}{Command: "git -C /work/projects/other-repo commit -m 'feat'"}},
				},
			},
		},
	})
	_, _, _, _, _, crossRepoPath := analyzeSession(path)
	if crossRepoPath == "" {
		t.Error("expected crossRepoPath to be set for git -C outside the consumer config repo")
	}
}

func TestFindSessionFileMissingDir(t *testing.T) {
	dir := t.TempDir()
	got := findSessionFile("no-such-slug", dir)
	if got != "" {
		t.Errorf("expected empty for missing projects dir, got %q", got)
	}
}

func TestFindSessionFileFindsJSONL(t *testing.T) {
	projectsDir := t.TempDir()
	slug := "my-task"
	sessionDir := filepath.Join(projectsDir, "-work-consumer-config--worktrees-"+slug)
	if err := os.MkdirAll(sessionDir, 0o755); err != nil {
		t.Fatal(err)
	}
	jsonlPath := filepath.Join(sessionDir, "session.jsonl")
	if err := os.WriteFile(jsonlPath, []byte("{}"), 0o644); err != nil {
		t.Fatal(err)
	}

	got := findSessionFile(slug, projectsDir)
	if got != jsonlPath {
		t.Errorf("got %q, want %q", got, jsonlPath)
	}
}

// writeTranscript writes msgs as newline-delimited JSON to a temp file.
func writeTranscript(t *testing.T, msgs []transcriptMsg) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "session.jsonl")
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	enc := json.NewEncoder(f)
	for _, m := range msgs {
		if err := enc.Encode(m); err != nil {
			t.Fatal(err)
		}
	}
	return path
}
