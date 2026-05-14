package lints

import (
	"sort"
	"testing"
)

func TestAgenix(t *testing.T) {
	tests := []struct {
		name  string
		lint  Agenix
		files map[string]string
		want  []string
	}{
		{
			name: "readFile of literal .age path",
			files: map[string]string{
				"a.nix": "{ x = builtins.readFile ./secrets/foo.age; }\n",
			},
			want: []string{"a.nix:1"},
		},
		{
			name: "readFile of age.secrets path",
			files: map[string]string{
				"a.nix": "{ x = builtins.readFile config.age.secrets.foo.path; }\n",
			},
			want: []string{"a.nix:1"},
		},
		{
			name: "lib.readFile of age.secrets",
			files: map[string]string{
				"a.nix": "{ x = lib.readFile cfg.age.secrets.bar.path; }\n",
			},
			want: []string{"a.nix:1"},
		},
		{
			name: "readFile with parenthesized arg",
			files: map[string]string{
				"a.nix": "{ x = readFile (cfg.age.secrets.x.path); }\n",
			},
			want: []string{"a.nix:1"},
		},
		{
			name: "writeText leak inline",
			files: map[string]string{
				"a.nix": "{ x = pkgs.writeText \"n\" \"hi ${config.age.secrets.foo.path} bye\"; }\n",
			},
			want: []string{"a.nix:1"},
		},
		{
			name: "writeShellScript heredoc leak",
			files: map[string]string{
				"a.nix": "{ x = pkgs.writeShellScript \"n\" ''\n  echo ${config.age.secrets.foo.path}\n''; }\n",
			},
			want: []string{"a.nix:1"},
		},
		{
			name: "writeScriptBin leak",
			files: map[string]string{
				"a.nix": "{ x = pkgs.writeScriptBin \"n\" \"${config.age.secrets.x.path}\"; }\n",
			},
			want: []string{"a.nix:1"},
		},
		{
			name: "writeScript leak",
			files: map[string]string{
				"a.nix": "{ x = pkgs.writeScript \"n\" \"${config.age.secrets.x.path}\"; }\n",
			},
			want: []string{"a.nix:1"},
		},
		{
			name: "text = heredoc leak",
			files: map[string]string{
				"a.nix": "{\n  systemd.services.foo = {\n    text = ''\n      echo ${config.age.secrets.x.path}\n    '';\n  };\n}\n",
			},
			want: []string{"a.nix:3"},
		},
		{
			name: "script = heredoc leak",
			files: map[string]string{
				"a.nix": "{\n  systemd.services.foo = {\n    script = ''\n      cat ${config.age.secrets.x.path}\n    '';\n  };\n}\n",
			},
			want: []string{"a.nix:3"},
		},
		{
			name: "dynamic attr name in interpolation",
			files: map[string]string{
				"a.nix": "{ x = pkgs.writeText \"n\" \"${config.age.secrets.${name}.path}\"; }\n",
			},
			want: []string{"a.nix:1"},
		},
		{
			name: "passwordCommand is clean",
			files: map[string]string{
				"a.nix": "{ services.foo.passwordCommand = \"cat ${config.age.secrets.db.path}\"; }\n",
			},
			want: nil,
		},
		{
			name: "preStart is clean",
			files: map[string]string{
				"a.nix": "{ systemd.services.foo.preStart = \"install -m600 ${config.age.secrets.db.path} /run/foo\"; }\n",
			},
			want: nil,
		},
		{
			name: "environmentFile attribute reference is clean",
			files: map[string]string{
				"a.nix": "{ services.foo.environmentFile = config.age.secrets.x.path; }\n",
			},
			want: nil,
		},
		{
			name: "ExecStart with secrets path is clean",
			files: map[string]string{
				"a.nix": "{ systemd.services.foo.serviceConfig.ExecStart = \"${pkg}/bin/foo --key ${config.age.secrets.x.path}\"; }\n",
			},
			want: nil,
		},
		{
			name: "readFileType custom wrapper is not matched",
			files: map[string]string{
				"a.nix": "{ x = lib.readFileType cfg.age.secrets.x.path; }\n",
			},
			want: nil,
		},
		{
			name: "templates/ is skipped by default",
			files: map[string]string{
				"templates/x.nix": "{ x = builtins.readFile config.age.secrets.foo.path; }\n",
			},
			want: nil,
		},
		{
			name: "secrets/ is skipped by default",
			files: map[string]string{
				"secrets/x.nix": "{ x = builtins.readFile config.age.secrets.foo.path; }\n",
			},
			want: nil,
		},
		{
			name: "skip_path honored",
			lint: Agenix{SkipPath: []string{"vendored/"}},
			files: map[string]string{
				"vendored/a.nix": "{ x = builtins.readFile config.age.secrets.foo.path; }\n",
				"modules/b.nix":  "{ x = builtins.readFile config.age.secrets.foo.path; }\n",
			},
			want: []string{"modules/b.nix:1"},
		},
		{
			name: "scan_dirs limits the walk",
			lint: Agenix{ScanDirs: []string{"modules"}},
			files: map[string]string{
				"modules/a.nix": "{ x = builtins.readFile config.age.secrets.foo.path; }\n",
				"other/b.nix":   "{ x = builtins.readFile config.age.secrets.foo.path; }\n",
			},
			want: []string{"modules/a.nix:1"},
		},
		{
			name: "no nix files is a no-op",
			files: map[string]string{
				"README.md": "hi\n",
			},
			want: nil,
		},
		{
			name: "clean nix file produces no issues",
			files: map[string]string{
				"a.nix": "{ services.foo.environmentFile = config.age.secrets.x.path; }\n",
			},
			want: nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			root := newTestRepo(t, tt.files)
			issues, err := tt.lint.Run(root)
			if err != nil {
				t.Fatalf("Run: %v", err)
			}
			got := make([]string, 0, len(issues))
			for _, i := range issues {
				got = append(got, locTag(i))
			}
			sort.Strings(got)
			want := append([]string(nil), tt.want...)
			sort.Strings(want)
			if !equalStrings(got, want) {
				t.Fatalf("issues: got %v want %v (full: %v)", got, want, issues)
			}
		})
	}
}
