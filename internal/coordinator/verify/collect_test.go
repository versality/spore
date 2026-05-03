package verify

import (
	"os"
	"path/filepath"
	"testing"
)

func TestExtractFilesSection(t *testing.T) {
	content := []byte("---\nstatus: done\n---\n\n# Summary\nSome body.\n\n# Files\n- `internal/foo.go`\n- `internal/bar.go`\n\n# Other\nmore text\n")
	got := extractFilesSection(content)
	if got == "" {
		t.Fatal("expected non-empty files section")
	}
	if !findSubstring(got, "foo.go") {
		t.Errorf("files section missing foo.go:\n%s", got)
	}
	if findSubstring(got, "Other") {
		t.Errorf("files section leaked into next heading:\n%s", got)
	}
}

func TestExtractFilesSectionMissing(t *testing.T) {
	content := []byte("---\nstatus: done\n---\n\n# Summary\nno files section here\n")
	got := extractFilesSection(content)
	if got != "" {
		t.Errorf("expected empty string for missing section, got %q", got)
	}
}

func TestExtractBacktickPaths(t *testing.T) {
	cases := []struct {
		name  string
		input string
		want  []string
	}{
		{
			name:  "single path",
			input: "- `internal/foo.go`",
			want:  []string{"internal/foo.go"},
		},
		{
			name:  "multiple paths",
			input: "- `internal/foo.go`\n- `cmd/bar/main.go`\n",
			want:  []string{"internal/foo.go", "cmd/bar/main.go"},
		},
		{
			name:  "no backticks",
			input: "just plain text",
			want:  nil,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := extractBacktickPaths(tc.input)
			if len(got) != len(tc.want) {
				t.Fatalf("got %v, want %v", got, tc.want)
			}
			for i, w := range tc.want {
				if got[i] != w {
					t.Errorf("[%d]: got %q, want %q", i, got[i], w)
				}
			}
		})
	}
}

func TestCheckEvidenceNoEvidenceRequired(t *testing.T) {
	dir := t.TempDir()
	writeTaskFileV(t, dir, "notask", "---\nstatus: active\nslug: notask\ntitle: No Evidence\n---\n")

	got := checkEvidence(dir, "notask")
	if got != "" {
		t.Errorf("expected empty failures, got %q", got)
	}
}

func TestCheckEvidenceFileMissing(t *testing.T) {
	dir := t.TempDir()
	content := "---\nstatus: done\nslug: fmissing\ntitle: File Missing\nevidence_required: [file]\n---\n\n## Evidence\n\n- file: internal/missing.go not present\n"
	writeTaskFileV(t, dir, "fmissing", content)

	got := checkEvidence(dir, "fmissing")
	if got == "" {
		t.Error("expected failure for missing file, got empty")
	}
}

func TestCheckEvidenceFilePresent(t *testing.T) {
	dir := t.TempDir()
	content := "---\nstatus: done\nslug: fpresent\ntitle: File Present\nevidence_required: [file]\n---\n\n## Evidence\n\n- file: internal/fpresent/x.go present\n"
	writeTaskFileV(t, dir, "fpresent", content)

	refDir := filepath.Join(dir, "internal", "fpresent")
	if err := os.MkdirAll(refDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(refDir, "x.go"), []byte("package x"), 0o644); err != nil {
		t.Fatal(err)
	}

	got := checkEvidence(dir, "fpresent")
	if got != "" {
		t.Errorf("expected no failures for present file, got %q", got)
	}
}

func TestHasEvidenceSectionTrue(t *testing.T) {
	dir := t.TempDir()
	content := "---\nstatus: done\nevidence_required: [commit]\n---\n"
	writeTaskFileV(t, dir, "ev", content)

	if !hasEvidenceSection(dir, "ev") {
		t.Error("expected hasEvidenceSection to return true")
	}
}

func TestHasEvidenceSectionFalse(t *testing.T) {
	dir := t.TempDir()
	content := "---\nstatus: done\nslug: noev\n---\n"
	writeTaskFileV(t, dir, "noev", content)

	if hasEvidenceSection(dir, "noev") {
		t.Error("expected hasEvidenceSection to return false")
	}
}

func TestHasEvidenceSectionMissingFile(t *testing.T) {
	dir := t.TempDir()
	// No task file - should return false, not panic.
	if hasEvidenceSection(dir, "ghost") {
		t.Error("expected hasEvidenceSection to return false for missing file")
	}
}

func TestReadFrontmatterStatus(t *testing.T) {
	dir := t.TempDir()
	writeTaskFileV(t, dir, "myslug", "---\nstatus: paused\nslug: myslug\n---\n")
	got := readFrontmatterStatus(dir, "myslug")
	if got != "paused" {
		t.Errorf("got %q, want paused", got)
	}
}

func TestReadFrontmatterStatusMissing(t *testing.T) {
	dir := t.TempDir()
	got := readFrontmatterStatus(dir, "ghost")
	if got != "?" {
		t.Errorf("missing file: got %q, want ?", got)
	}
}

func TestFindPrevReflogSHA(t *testing.T) {
	reflog := "abc1234 HEAD@{0}: merge wt/demo: fast-forward\ndef5678 HEAD@{1}: checkout: moving from main to wt/demo\n9abcdef HEAD@{2}: commit: init\n"
	got := findPrevReflogSHA(reflog, "HEAD@{0}")
	if got != "def5678" {
		t.Errorf("got %q, want def5678", got)
	}
}

func TestFindPrevReflogSHANoMatch(t *testing.T) {
	reflog := "abc1234 HEAD@{0}: some action\n"
	got := findPrevReflogSHA(reflog, "HEAD@{99}")
	if got != "" {
		t.Errorf("got %q, want empty", got)
	}
}

// writeTaskFileV creates tasks/<slug>.md under root.
func writeTaskFileV(t *testing.T, root, slug, content string) {
	t.Helper()
	tasksDir := filepath.Join(root, "tasks")
	if err := os.MkdirAll(tasksDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(tasksDir, slug+".md"), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}
