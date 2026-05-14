package leakdict

import "testing"

func TestDictionaryNonEmpty(t *testing.T) {
	if len(Dictionary) == 0 {
		t.Fatal("Dictionary must not be empty")
	}
	mustHave := []string{
		"skyhelm",
		"skywing",
		"skytower",
		"skypad",
		"helm-mcom",
		"marketercom",
		"/home/sky/nix-config",
		"rower",
	}
	have := map[string]bool{}
	for _, t := range Dictionary {
		have[t] = true
	}
	for _, m := range mustHave {
		if !have[m] {
			t.Errorf("dictionary missing %q", m)
		}
	}
}

func TestScanMessage(t *testing.T) {
	cases := []struct {
		msg  string
		want string
	}{
		{"normal commit message", ""},
		{"refactor coordinator wiring", ""},
		{"fix skyhelm probe", "skyhelm"},
		{"port harness from /home/sky/nix-config/harness/x.sh", "/home/sky/nix-config"},
		{"SKYHELM uppercase still flagged", "skyhelm"},
		{"stale rower reference", "rower"},
		{"Rower mid-sentence", "rower"},
	}
	for _, c := range cases {
		if got := ScanMessage(c.msg, nil); got != c.want {
			t.Errorf("ScanMessage(%q) = %q, want %q", c.msg, got, c.want)
		}
	}
}

func TestScanMessageExtra(t *testing.T) {
	if got := ScanMessage("this references private-term", []string{"private-term"}); got != "private-term" {
		t.Errorf("expected extra-term match, got %q", got)
	}
}

func TestScanLine(t *testing.T) {
	hits := ScanLine("references skyhelm and helm-mcom both", nil)
	if len(hits) != 2 {
		t.Fatalf("expected 2 hits, got %v", hits)
	}
	want := map[string]bool{"skyhelm": false, "helm-mcom": false}
	for _, h := range hits {
		want[h] = true
	}
	if !want["skyhelm"] || !want["helm-mcom"] {
		t.Errorf("missing expected term in %v", hits)
	}
}

func TestFormFor(t *testing.T) {
	if got := FormFor("skyhelm"); got != "skyhelm" {
		t.Errorf("got %q", got)
	}
	// unknown -> passthrough
	if got := FormFor("zzz"); got != "zzz" {
		t.Errorf("got %q", got)
	}
}
