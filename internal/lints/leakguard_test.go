package lints

import (
	"sort"
	"strings"
	"testing"

	"github.com/versality/spore/internal/leakdict"
)

func TestLeakGuard_FlagsAllDictionaryTerms(t *testing.T) {
	files := map[string]string{}
	for _, term := range leakdict.Dictionary {
		fname := "fixture-" + strings.ReplaceAll(strings.ReplaceAll(term, "/", "_"), "~", "tilde") + ".md"
		files[fname] = "leak line: " + term + "\n"
	}
	root := newTestRepo(t, files)
	issues, err := LeakGuard{}.Run(root)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	gotTerms := map[string]bool{}
	for _, i := range issues {
		if a, b := strings.Index(i.Message, "\""), strings.LastIndex(i.Message, "\""); a >= 0 && b > a {
			gotTerms[i.Message[a+1:b]] = true
		}
	}
	for _, want := range leakdict.Dictionary {
		if !gotTerms[want] {
			t.Errorf("dictionary term %q was not flagged; got terms %v", want, sortedBoolMapKeys(gotTerms))
		}
	}
}

func TestLeakGuard_AllowlistsSelf(t *testing.T) {
	root := newTestRepo(t, map[string]string{
		"internal/lints/leakguard.go":      "package lints // dictionary literal: skyhelm\n",
		"internal/lints/leakguard_test.go": "// fixture mentions skyhelm\n",
		"internal/leakdict/leakdict.go":    "var Dictionary = []string{\"skyhelm\"}\n",
		"docs/clean.md":                    "all clean\n",
	})
	issues, err := LeakGuard{}.Run(root)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	for _, i := range issues {
		if strings.HasPrefix(i.Path, "internal/lints/leakguard") || strings.HasPrefix(i.Path, "internal/leakdict/") {
			t.Errorf("expected leak-source files to be allowlisted, got %v", i)
		}
	}
}

func TestLeakGuard_AllowlistsCoordinatorBoot(t *testing.T) {
	root := newTestRepo(t, map[string]string{
		"internal/coordinator/boot/boot.go":   "package boot // skyhelm cold-boot probes\n",
		"internal/coordinator/boot/probes.go": "package boot // exec(\"skyhelm-budget\", \"summary\")\n",
		"docs/leaks.md":                       "this file mentions skyhelm and must flag\n",
	})
	issues, err := LeakGuard{}.Run(root)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	for _, i := range issues {
		if strings.HasPrefix(i.Path, "internal/coordinator/boot/") {
			t.Errorf("expected coordinator/boot to be allowlisted, got %v", i)
		}
	}
	var flaggedDocs bool
	for _, i := range issues {
		if i.Path == "docs/leaks.md" {
			flaggedDocs = true
		}
	}
	if !flaggedDocs {
		t.Errorf("expected docs/leaks.md to still flag, got issues=%v", issues)
	}
}

func TestLeakGuard_CaseInsensitive(t *testing.T) {
	root := newTestRepo(t, map[string]string{
		"docs/x.md": "See SKYHELM and SkyHelm references.\n",
	})
	issues, err := LeakGuard{}.Run(root)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(issues) == 0 {
		t.Fatal("expected case-insensitive match on SKYHELM/SkyHelm")
	}
}

func TestLeakGuard_NoFalsePositiveOnCleanRepo(t *testing.T) {
	root := newTestRepo(t, map[string]string{
		"docs/clean.md":  "coordinator + worker; host-a, host-b.\n",
		"main.go":        "package main\n\nfunc main() {}\n",
		"internal/x.go":  "package x // pure spore generic code\n",
		"configs/c.toml": "key = \"value\"\n",
	})
	issues, err := LeakGuard{}.Run(root)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(issues) != 0 {
		t.Errorf("expected zero issues on clean repo, got %v", issues)
	}
}

func TestLeakGuard_ExtraTerms(t *testing.T) {
	root := newTestRepo(t, map[string]string{
		"docs/x.md": "this mentions custom-private-term in a sentence.\n",
	})
	issues, err := LeakGuard{Extra: []string{"custom-private-term"}}.Run(root)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(issues) == 0 {
		t.Fatal("expected match on Extra term")
	}
}

func TestLeakGuard_SkipsBinaryAndUnknownExts(t *testing.T) {
	root := newTestRepo(t, map[string]string{
		"assets/x.png": "fake-png-skyhelm-bytes\n",
	})
	issues, err := LeakGuard{}.Run(root)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(issues) != 0 {
		t.Errorf("expected png to be skipped, got %v", issues)
	}
}

func sortedBoolMapKeys(m map[string]bool) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
