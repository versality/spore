package spore

import "testing"

func TestVersion(t *testing.T) {
	if got := Version(); got != "0.0.2" {
		t.Fatalf("Version() = %q, want %q", got, "0.0.2")
	}
}

func TestBuildVersion(t *testing.T) {
	prev := buildCommit
	defer func() { buildCommit = prev }()

	buildCommit = "abc123"
	if got := BuildVersion(); got != "0.0.2 (abc123)" {
		t.Fatalf("BuildVersion() = %q, want commit", got)
	}

	buildCommit = "unknown"
	if got := BuildVersion(); got != "0.0.2 (commit unknown)" {
		t.Fatalf("BuildVersion() = %q, want unknown marker", got)
	}
}
