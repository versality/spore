package bootstrap

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDetectRepoMapped(t *testing.T) {
	cases := []struct {
		name     string
		files    map[string]string
		wantErr  string
		wantNote string
	}{
		{
			name:     "nix flake",
			files:    map[string]string{"flake.nix": "{}\n"},
			wantNote: "detected: nix",
		},
		{
			name:     "rust crate",
			files:    map[string]string{"Cargo.toml": "[package]\n"},
			wantNote: "detected: rust",
		},
		{
			name:     "go module",
			files:    map[string]string{"go.mod": "module x\n"},
			wantNote: "detected: go",
		},
		{
			name:     "node package",
			files:    map[string]string{"package.json": "{}\n"},
			wantNote: "detected: node",
		},
		{
			name:     "polyglot rust+nix",
			files:    map[string]string{"flake.nix": "{}\n", "Cargo.toml": "[package]\n"},
			wantNote: "detected: nix,rust",
		},
		{
			name:    "no markers",
			files:   map[string]string{"random.txt": "x\n"},
			wantErr: "no recognised project marker",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			for p, c := range tc.files {
				writeFile(t, filepath.Join(root, p), []byte(c))
			}
			notes, err := detectRepoMapped(root)
			if tc.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
					t.Fatalf("err=%v; want substring %q", err, tc.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("detect: %v", err)
			}
			if !strings.HasPrefix(notes, tc.wantNote) {
				t.Errorf("notes=%q; want prefix %q", notes, tc.wantNote)
			}
		})
	}
}

func TestDetectRepoMappedDropsStarterClaude(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "flake.nix"), []byte("{}\n"))
	notes, err := detectRepoMapped(root)
	if err != nil {
		t.Fatalf("detect: %v", err)
	}
	if !strings.Contains(notes, "wrote starter CLAUDE.md") {
		t.Errorf("notes=%q; want mention of starter CLAUDE.md", notes)
	}
	if _, err := os.Stat(filepath.Join(root, "CLAUDE.md")); err != nil {
		t.Fatalf("starter CLAUDE.md missing: %v", err)
	}
}

func TestDetectRepoMappedInstallsSkills(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "flake.nix"), []byte("{}\n"))
	notes, err := detectRepoMapped(root)
	if err != nil {
		t.Fatalf("detect: %v", err)
	}
	if !strings.Contains(notes, "installed") || !strings.Contains(notes, "skill file") {
		t.Errorf("notes=%q; want mention of installed skill files", notes)
	}
	skill := filepath.Join(root, ".claude", "skills", "spore-bootstrap", "SKILL.md")
	if _, err := os.Stat(skill); err != nil {
		t.Fatalf("spore-bootstrap SKILL.md missing: %v", err)
	}
}

func TestDetectRepoMappedSkillInstallIsIdempotent(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "flake.nix"), []byte("{}\n"))
	if _, err := detectRepoMapped(root); err != nil {
		t.Fatalf("detect #1: %v", err)
	}
	notes, err := detectRepoMapped(root)
	if err != nil {
		t.Fatalf("detect #2: %v", err)
	}
	if strings.Contains(notes, "installed") {
		t.Errorf("notes=%q; want no install mention on second run (idempotent)", notes)
	}
}

func TestDetectRepoMappedLeavesExistingClaude(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "flake.nix"), []byte("{}\n"))
	original := []byte("# my CLAUDE.md\nhello\n")
	writeFile(t, filepath.Join(root, "CLAUDE.md"), original)
	if _, err := detectRepoMapped(root); err != nil {
		t.Fatalf("detect: %v", err)
	}
	got, err := os.ReadFile(filepath.Join(root, "CLAUDE.md"))
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if string(got) != string(original) {
		t.Errorf("starter clobbered existing CLAUDE.md")
	}
}
