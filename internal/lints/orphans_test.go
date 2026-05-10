package lints

import "testing"

func TestOrphans(t *testing.T) {
	root := newTestRepo(t, map[string]string{
		"configs/wired/file.txt":    "x\n",
		"configs/orphan/file.txt":   "x\n",
		"configs/allowed/file.txt":  "x\n",
		"bash/wired.sh":             "echo wired\n",
		"bash/orphan.sh":            "echo orphan\n",
		"nix/modules/foo.nix":       "{ ./configs/wired ./bash/wired.sh }\n",
		"nix/harness/notes.nix":     "{ /* mentions configs/orphan but excluded */ }\n",
		"harness/orphans-allowlist": "configs/allowed\n# comment\n\n",
	})
	issues, err := Orphans{}.Run(root)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	got := map[string]bool{}
	for _, i := range issues {
		got[i.Path] = true
	}
	if !got["configs/orphan"] || !got["bash/orphan.sh"] {
		t.Errorf("expected configs/orphan and bash/orphan.sh hits; got %v", got)
	}
	if got["configs/wired"] || got["bash/wired.sh"] || got["configs/allowed"] {
		t.Errorf("unexpected hits: %v", got)
	}
}
