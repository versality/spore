package lints

import (
	"strings"
	"testing"
)

func TestFingerprintStableAcrossCalls(t *testing.T) {
	a := Fingerprint("em-dash", "docs/x.md", 12, "em-dash at column 4")
	b := Fingerprint("em-dash", "docs/x.md", 12, "em-dash at column 4")
	if a != b {
		t.Fatalf("expected stable hash, got %q vs %q", a, b)
	}
	if !strings.HasPrefix(a, FingerprintVersion+":") {
		t.Fatalf("expected %s: prefix, got %q", FingerprintVersion, a)
	}
}

func TestFingerprintDistinguishesInputs(t *testing.T) {
	cases := []struct {
		name string
		a, b string
	}{
		{"lint", Fingerprint("em-dash", "p", 1, "m"), Fingerprint("file-size", "p", 1, "m")},
		{"path", Fingerprint("l", "a", 1, "m"), Fingerprint("l", "b", 1, "m")},
		{"line", Fingerprint("l", "p", 1, "m"), Fingerprint("l", "p", 2, "m")},
		{"msg", Fingerprint("l", "p", 1, "a"), Fingerprint("l", "p", 1, "b")},
	}
	for _, c := range cases {
		if c.a == c.b {
			t.Errorf("%s: expected distinct fingerprints, both %q", c.name, c.a)
		}
	}
}
