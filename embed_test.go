package spore

import "testing"

func TestVersion(t *testing.T) {
	if got := Version(); got != "0.0.2" {
		t.Fatalf("Version() = %q, want %q", got, "0.0.2")
	}
}
