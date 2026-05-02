package hooks

import (
	"os"
	"strings"
	"testing"
)

func TestSettings_GoldenFile(t *testing.T) {
	events := map[string][]HookBin{
		"Stop": {
			{Name: "watch-inbox", BinPath: "/usr/bin/watch-inbox"},
			{Name: "replenish", BinPath: "/usr/bin/replenish", Timeout: 30},
		},
		"PostToolUse": {
			{Name: "lint-noise", BinPath: "/usr/bin/lint-noise write", Matcher: "Write|Edit", Timeout: 10},
		},
		"Notification": {
			{Name: "notify-coordinator", BinPath: "/usr/bin/notify-coordinator"},
		},
	}

	got, err := Settings(events)
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

func TestSettings_EmptyEventsOmitHooks(t *testing.T) {
	got, err := Settings(nil)
	if err != nil {
		t.Fatal(err)
	}
	want := "{\n  \"$schema\": \"https://json.schemastore.org/claude-code-settings.json\"\n}\n"
	if string(got) != want {
		t.Fatalf("got:\n%s\nwant:\n%s", got, want)
	}
}

func TestSettings_EmptyBinPathErrors(t *testing.T) {
	_, err := Settings(map[string][]HookBin{
		"Stop": {{Name: "bad"}},
	})
	if err == nil {
		t.Fatal("expected error for empty BinPath")
	}
}

func TestSettings_SingleEvent(t *testing.T) {
	got, err := Settings(map[string][]HookBin{
		"Notification": {{Name: "x", BinPath: "/bin/x"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	s := string(got)
	if !strings.Contains(s, `"Notification"`) {
		t.Fatalf("missing Notification:\n%s", s)
	}
	if strings.Contains(s, `"Stop"`) || strings.Contains(s, `"PostToolUse"`) {
		t.Fatalf("unexpected event keys:\n%s", s)
	}
}

func TestSettings_AsyncFields(t *testing.T) {
	got, err := Settings(map[string][]HookBin{
		"Stop": {
			{Name: "watcher", BinPath: "/bin/watch", AsyncRewake: true, Timeout: 604800},
		},
		"Notification": {
			{Name: "notify", BinPath: "/bin/notify", Async: true, Timeout: 10},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	s := string(got)
	if !strings.Contains(s, `"asyncRewake": true`) {
		t.Fatalf("missing asyncRewake:\n%s", s)
	}
	if !strings.Contains(s, `"async": true`) {
		t.Fatalf("missing async:\n%s", s)
	}
}

func TestSettings_Consolidation(t *testing.T) {
	got, err := Settings(map[string][]HookBin{
		"Stop": {
			{Name: "a", BinPath: "/bin/a"},
			{Name: "b", BinPath: "/bin/b"},
			{Name: "c", BinPath: "/bin/c", Matcher: "Write"},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	s := string(got)
	// a and b share the same matcher (empty) -> one group with 2 entries.
	// c has a different matcher -> separate group.
	// So Stop should have 2 groups total.
	if strings.Count(s, `"matcher"`) != 1 {
		t.Fatalf("expected 1 matcher field (only the non-empty one):\n%s", s)
	}
	if strings.Count(s, `"/bin/a"`) != 1 || strings.Count(s, `"/bin/b"`) != 1 {
		t.Fatalf("missing consolidated entries:\n%s", s)
	}
}

func TestSettings_UserPromptSubmit(t *testing.T) {
	got, err := Settings(map[string][]HookBin{
		"UserPromptSubmit": {
			{Name: "feedback", BinPath: "/bin/feedback", Timeout: 5},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	s := string(got)
	if !strings.Contains(s, `"UserPromptSubmit"`) {
		t.Fatalf("missing UserPromptSubmit:\n%s", s)
	}
}
