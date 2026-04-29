package align

import (
	"os"
	"path/filepath"
	"testing"
)

func tempProject(t *testing.T) (root string, paths Paths) {
	t.Helper()
	root = t.TempDir()
	t.Setenv("XDG_STATE_HOME", filepath.Join(root, "state"))
	t.Setenv("HOME", filepath.Join(root, "home"))
	p, err := Resolve(root)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	return root, p
}

func TestNoteAppendsBullet(t *testing.T) {
	_, p := tempProject(t)
	if err := Note(p, "prefer small commits"); err != nil {
		t.Fatalf("Note: %v", err)
	}
	if err := Note(p, "- ask before installing deps"); err != nil {
		t.Fatalf("Note (already-bulleted): %v", err)
	}
	b, err := os.ReadFile(p.AlignmentFile)
	if err != nil {
		t.Fatalf("read alignment.md: %v", err)
	}
	want := "- prefer small commits\n- ask before installing deps\n"
	if string(b) != want {
		t.Fatalf("alignment.md mismatch:\n got: %q\nwant: %q", b, want)
	}
}

func TestNoteEmptyRejected(t *testing.T) {
	_, p := tempProject(t)
	if err := Note(p, "   "); err == nil {
		t.Fatal("Note: expected error for blank line, got nil")
	}
}

func TestStatusCountsAndPromotion(t *testing.T) {
	_, p := tempProject(t)
	for _, n := range []string{
		"a", "b", "c",
		"[promoted] d",
		"e",
		"[promoted] f",
	} {
		if err := Note(p, n); err != nil {
			t.Fatalf("Note %q: %v", n, err)
		}
	}
	s, err := Read(p, DefaultCriteria())
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if s.Notes != 6 || s.Promoted != 2 {
		t.Fatalf("counts: notes=%d promoted=%d; want 6/2", s.Notes, s.Promoted)
	}
	if s.Met() {
		t.Fatal("Met: true; want false (under thresholds + not flipped)")
	}
}

func TestFlipAndMet(t *testing.T) {
	_, p := tempProject(t)
	for i := 0; i < 10; i++ {
		body := "pref"
		if i < 3 {
			body = "[promoted] pref"
		}
		if err := Note(p, body); err != nil {
			t.Fatalf("Note: %v", err)
		}
	}
	s, err := Read(p, DefaultCriteria())
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if s.Met() {
		t.Fatal("Met before flip: want false")
	}
	if err := Flip(p); err != nil {
		t.Fatalf("Flip: %v", err)
	}
	s, err = Read(p, DefaultCriteria())
	if err != nil {
		t.Fatalf("Read post-flip: %v", err)
	}
	if !s.Met() {
		t.Fatalf("Met post-flip: false; status=%+v", s)
	}
	if s.Active() {
		t.Fatal("Active post-flip: true; want false")
	}
}

func TestActiveDefaultOnFreshProject(t *testing.T) {
	root, _ := tempProject(t)
	on, err := Active(root)
	if err != nil {
		t.Fatalf("Active: %v", err)
	}
	if !on {
		t.Fatal("Active on fresh project: false; want true")
	}
}

func TestLoadCriteriaOverrides(t *testing.T) {
	root, _ := tempProject(t)
	if err := os.WriteFile(filepath.Join(root, "spore.toml"), []byte(`
# unrelated leading comment

[other]
required_notes = 99    # ignored, not in [align]

[align]
required_notes = 5
required_promoted = 2  # trailing comment
`), 0o644); err != nil {
		t.Fatalf("write spore.toml: %v", err)
	}
	c, err := LoadCriteria(root)
	if err != nil {
		t.Fatalf("LoadCriteria: %v", err)
	}
	if c.RequiredNotes != 5 || c.RequiredPromoted != 2 {
		t.Fatalf("LoadCriteria: %+v; want notes=5 promoted=2", c)
	}
}

func TestLoadCriteriaMissingFileFallsBackToDefaults(t *testing.T) {
	root, _ := tempProject(t)
	c, err := LoadCriteria(root)
	if err != nil {
		t.Fatalf("LoadCriteria: %v", err)
	}
	if c != DefaultCriteria() {
		t.Fatalf("LoadCriteria fallback: %+v; want %+v", c, DefaultCriteria())
	}
}

func TestLoadCriteriaMalformedAlignSurfaces(t *testing.T) {
	root, _ := tempProject(t)
	if err := os.WriteFile(filepath.Join(root, "spore.toml"),
		[]byte("[align]\nrequired_notes = not-a-number\n"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	if _, err := LoadCriteria(root); err == nil {
		t.Fatal("LoadCriteria: expected error on malformed value")
	}
}

func TestResolvePicksGitToplevelBasename(t *testing.T) {
	if testing.Short() {
		t.Skip("skip git-dependent test in short mode")
	}
	root, p := tempProject(t)
	if p.Project != filepath.Base(root) {
		t.Fatalf("Project: %q; want %q", p.Project, filepath.Base(root))
	}
}
