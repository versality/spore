// Package golden provides a tiny golden-file helper with a shared
// `-update` flag so tests across packages can use one regeneration
// idiom.
//
// Test usage:
//
//	got := Render(...)
//	golden.Equal(t, "testdata/foo.golden", got)
//
// Regenerating the corpus:
//
//	go test ./internal/foo/... -update
//
// On a failing compare the test fails as usual; on -update the file is
// rewritten in place (creating the testdata dir if absent) and the
// test passes. Inspect git diff before committing.
package golden

import (
	"flag"
	"os"
	"path/filepath"
	"testing"
)

// Update is true when `-update` is passed to `go test`. The flag is
// shared by every test that imports this package.
var Update = flag.Bool("update", false, "rewrite golden files in testdata/ to current output")

// Equal compares got against the contents of path. When -update is
// set, path is rewritten instead of compared. Trailing whitespace is
// not normalised; tests should pass bytes that match exactly.
func Equal(t *testing.T, path string, got []byte) {
	t.Helper()
	if *Update {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatalf("golden: mkdir %s: %v", filepath.Dir(path), err)
		}
		if err := os.WriteFile(path, got, 0o644); err != nil {
			t.Fatalf("golden: write %s: %v", path, err)
		}
		return
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("golden: read %s: %v (re-run with -update to seed)", path, err)
	}
	if string(want) != string(got) {
		t.Fatalf("golden mismatch at %s\nwant:\n%s\n---\ngot:\n%s", path, want, got)
	}
}
