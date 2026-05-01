package hooks

import (
	"os"
	"testing"
)

func TestSettings_GoldenFile(t *testing.T) {
	stops := []HookBin{
		{Name: "watch-inbox", BinPath: "/usr/bin/watch-inbox"},
		{Name: "replenish", BinPath: "/usr/bin/replenish", Timeout: 30},
	}
	postToolUse := []HookBin{
		{Name: "lint-noise", BinPath: "/usr/bin/lint-noise write", Matcher: "Write|Edit", Timeout: 10},
	}
	notification := []HookBin{
		{Name: "notify-coordinator", BinPath: "/usr/bin/notify-coordinator"},
	}

	got, err := Settings(stops, postToolUse, notification)
	if err != nil {
		t.Fatalf("Settings: %v", err)
	}

	golden, err := os.ReadFile("testdata/settings.golden.json")
	if err != nil {
		t.Fatalf("read golden: %v", err)
	}
	if string(got) != string(golden) {
		t.Fatalf("Settings output differs from golden file.\ngot:\n%s\nwant:\n%s", got, golden)
	}
}

func TestSettings_EmptySlicesOmitHooks(t *testing.T) {
	got, err := Settings(nil, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	want := "{\n  \"$schema\": \"https://json.schemastore.org/claude-code-settings.json\"\n}\n"
	if string(got) != want {
		t.Fatalf("got:\n%s\nwant:\n%s", got, want)
	}
}

func TestSettings_EmptyBinPathErrors(t *testing.T) {
	_, err := Settings([]HookBin{{Name: "bad"}}, nil, nil)
	if err == nil {
		t.Fatal("expected error for empty BinPath")
	}
}

func TestSettings_SingleEvent(t *testing.T) {
	got, err := Settings(nil, nil, []HookBin{
		{Name: "x", BinPath: "/bin/x"},
	})
	if err != nil {
		t.Fatal(err)
	}
	// Should contain Notification but not Stop or PostToolUse.
	s := string(got)
	if !contains(s, `"Notification"`) {
		t.Fatalf("missing Notification:\n%s", s)
	}
	if contains(s, `"Stop"`) || contains(s, `"PostToolUse"`) {
		t.Fatalf("unexpected event keys:\n%s", s)
	}
}

func contains(s, sub string) bool {
	return len(s) >= len(sub) && (s == sub || len(s) > 0 && containsSub(s, sub))
}

func containsSub(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
