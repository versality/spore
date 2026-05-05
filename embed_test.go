package spore

import (
	"os"
	"strings"
	"testing"
)

// versionFromFile is the source of truth for the test expectation: read
// VERSION at test time so a release that bumps the file (and the tag)
// does not require an `embed_test.go` edit too. Keeps `just release`
// from cascading into hand-edited test fixtures.
func versionFromFile(t *testing.T) string {
	t.Helper()
	raw, err := os.ReadFile("VERSION")
	if err != nil {
		t.Fatalf("read VERSION: %v", err)
	}
	return strings.TrimSpace(string(raw))
}

func TestVersion(t *testing.T) {
	want := versionFromFile(t)
	if got := Version(); got != want {
		t.Fatalf("Version() = %q, want %q (from VERSION file)", got, want)
	}
}

func TestBuildVersion(t *testing.T) {
	want := versionFromFile(t)
	prev := buildCommit
	defer func() { buildCommit = prev }()

	buildCommit = "abc123"
	if got, exp := BuildVersion(), want+" (abc123)"; got != exp {
		t.Fatalf("BuildVersion() = %q, want %q", got, exp)
	}

	buildCommit = "unknown"
	if got, exp := BuildVersion(), want+" (commit unknown)"; got != exp {
		t.Fatalf("BuildVersion() = %q, want %q", got, exp)
	}
}
