package bootstrap

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestDetectCredsWired(t *testing.T) {
	cases := []struct {
		name     string
		files    map[string]string
		wantErr  string
		wantNote string
	}{
		{
			name:     "no secrets, no instruction file needed",
			files:    map[string]string{"flake.nix": "{}\n"},
			wantNote: "no secret surface detected",
		},
		{
			name: ".env documented in CLAUDE.md",
			files: map[string]string{
				".env":      "FOO=bar\n",
				"CLAUDE.md": "Secrets live in `.env`.\n",
			},
			wantNote: "documented",
		},
		{
			name: ".env documented in AGENTS.md",
			files: map[string]string{
				".env":      "FOO=bar\n",
				"AGENTS.md": "Secrets live in `.env`.\n",
			},
			wantNote: "documented in AGENTS.md",
		},
		{
			name: ".envrc documented as environment variable",
			files: map[string]string{
				".envrc":    "export FOO=bar\n",
				"CLAUDE.md": "set the environment variable per project.\n",
			},
			wantNote: "documented",
		},
		{
			name: "secrets dir, agenix mention",
			files: map[string]string{
				"secrets/foo.age": "AGE\n",
				"CLAUDE.md":       "agenix decrypts to /run/agenix/foo.\n",
			},
			wantNote: "documented",
		},
		{
			name: "secret surface but instructions silent",
			files: map[string]string{
				".env":      "FOO=bar\n",
				"CLAUDE.md": "lorem ipsum dolor sit amet.\n",
			},
			wantErr: "agent instructions mention none",
		},
		{
			name: "secret surface, instructions missing",
			files: map[string]string{
				".env": "FOO=bar\n",
			},
			wantErr: "agent instructions are absent",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			for p, c := range tc.files {
				writeFile(t, filepath.Join(root, p), []byte(c))
			}
			notes, err := detectCredsWired(root)
			if tc.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
					t.Fatalf("err=%v; want substring %q", err, tc.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("detect: %v", err)
			}
			if !strings.Contains(notes, tc.wantNote) {
				t.Errorf("notes=%q; want substring %q", notes, tc.wantNote)
			}
		})
	}
}
