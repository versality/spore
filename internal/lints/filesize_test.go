package lints

import (
	"strings"
	"testing"
)

func TestFileSize_FlagsOversize(t *testing.T) {
	short := strings.Repeat("x\n", 10)
	long := strings.Repeat("x\n", 50)
	root := newTestRepo(t, map[string]string{
		"short.go": short,
		"long.go":  long,
		"doc.md":   long,
	})
	issues, err := FileSize{Limit: 20}.Run(root)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(issues) != 1 {
		t.Fatalf("expected 1 issue, got %d: %v", len(issues), issues)
	}
	if issues[0].Path != "long.go" {
		t.Fatalf("issue path: got %q want long.go", issues[0].Path)
	}
}

func TestFileSize_DefaultLimit(t *testing.T) {
	body := strings.Repeat("x\n", defaultFileSizeLimit+1)
	root := newTestRepo(t, map[string]string{"big.go": body})
	issues, err := FileSize{}.Run(root)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(issues) != 1 {
		t.Fatalf("expected 1 issue with default limit, got %v", issues)
	}
}

func TestFileSize_AllUnder(t *testing.T) {
	root := newTestRepo(t, map[string]string{
		"a.go": "package a\n",
		"b.go": "package b\n",
	})
	issues, err := FileSize{Limit: defaultFileSizeLimit}.Run(root)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(issues) != 0 {
		t.Fatalf("expected zero issues, got %v", issues)
	}
}

func TestFileSize_SkipsGeneratedFiles(t *testing.T) {
	long := strings.Repeat("x\n", 50)
	root := newTestRepo(t, map[string]string{
		"db/schema.rb":             long,
		"api/foo.pb.go":            long,
		"internal/x/types_gen.go":  long,
		"sorbet/rbi/all-types.rbi": long,
		"app/models/post.rb":       long,
	})
	issues, err := FileSize{Limit: 20}.Run(root)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(issues) != 1 {
		t.Fatalf("expected only the hand-written file to flag, got %d: %v", len(issues), issues)
	}
	if issues[0].Path != "app/models/post.rb" {
		t.Fatalf("issue path: got %q want app/models/post.rb", issues[0].Path)
	}
}

func TestIsGenerated(t *testing.T) {
	cases := map[string]bool{
		"db/schema.rb":                  true,
		"db/structure.sql":              true,
		"api/v1/foo.pb.go":              true,
		"api/v1/foo.pb.gw.go":           true,
		"internal/x/types_gen.go":       true,
		"internal/x/types_generated.go": true,
		"sorbet/rbi/all-types.rbi":      true,
		"app/models/post.rb":            false,
		"db/seeds.rb":                   false,
		"foo_gen_test.go":               false,
	}
	for path, want := range cases {
		if got := isGenerated(path); got != want {
			t.Errorf("isGenerated(%q) = %v, want %v", path, got, want)
		}
	}
}
