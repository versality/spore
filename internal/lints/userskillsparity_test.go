package lints

import "testing"

func TestUserSkillsParity_NoHostsIsNoop(t *testing.T) {
	root := newTestRepo(t, map[string]string{"README.md": "x\n"})
	issues, err := UserSkillsParity{}.Run(root)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(issues) != 0 {
		t.Fatalf("expected no issues with no hosts, got %v", issues)
	}
}

func TestUserSkillsParity_DefaultsCarrySurfaces(t *testing.T) {
	if len(DefaultSkillSurfaces) != 3 {
		t.Fatalf("expected 3 default surfaces, got %d", len(DefaultSkillSurfaces))
	}
	have := map[string]bool{}
	for _, s := range DefaultSkillSurfaces {
		have[s.Label] = true
	}
	for _, want := range []string{"claude", "codex", "opencode"} {
		if !have[want] {
			t.Errorf("missing default surface %s", want)
		}
	}
}
