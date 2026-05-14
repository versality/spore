package lints

import (
	"sort"
	"testing"
)

func TestFlakeInputShadow(t *testing.T) {
	flake := `{
  description = "demo";
  inputs = {
    nixpkgs.url = "github:NixOS/nixpkgs";
    myinput.url = "github:foo/bar";
    other-input = {
      url = "github:baz/qux";
      inputs.nixpkgs.follows = "nixpkgs";
    };
  };
  outputs = inputs: { };
}
`
	tests := []struct {
		name  string
		lint  FlakeInputShadow
		files map[string]string
		want  []string
	}{
		{
			name: "flags pkgs.myinput",
			files: map[string]string{
				"flake.nix":      flake,
				"nix/a.nix":      "{ pkgs, ... }: { x = pkgs.myinput.foo; }\n",
				"nix/clean.nix":  "{ pkgs, myinput, ... }: { x = myinput.foo; y = pkgs.hello; }\n",
				"nix/pkgsnp.nix": "{ pkgs, ... }: { x = pkgs.nixpkgs; }\n",
			},
			want: []string{"nix/a.nix:1"},
		},
		{
			name: "flags multiple inputs",
			files: map[string]string{
				"flake.nix": flake,
				"nix/b.nix": "{ pkgs, ... }: {\n  a = pkgs.myinput;\n  b = pkgs.other-input.foo;\n}\n",
			},
			want: []string{"nix/b.nix:2", "nix/b.nix:3"},
		},
		{
			name: "allowinputs honored",
			lint: FlakeInputShadow{AllowInputs: []string{"myinput"}},
			files: map[string]string{
				"flake.nix": flake,
				"nix/a.nix": "{ pkgs, ... }: { x = pkgs.myinput; y = pkgs.other-input; }\n",
			},
			want: []string{"nix/a.nix:1"},
		},
		{
			name: "skip_path honored",
			lint: FlakeInputShadow{SkipPath: []string{"nix/skip/"}},
			files: map[string]string{
				"flake.nix":         flake,
				"nix/skip/x.nix":    "{ pkgs, ... }: { x = pkgs.myinput; }\n",
				"nix/checked/y.nix": "{ pkgs, ... }: { x = pkgs.myinput; }\n",
			},
			want: []string{"nix/checked/y.nix:1"},
		},
		{
			name: "flake.nix itself never flagged",
			files: map[string]string{
				"flake.nix": flake + "\n# pkgs.myinput would not be a real call here\n",
				"nix/c.nix": "{ x = 1; }\n",
			},
			want: nil,
		},
		{
			name: "missing flake.nix is a no-op",
			files: map[string]string{
				"nix/a.nix": "{ pkgs, ... }: { x = pkgs.anything; }\n",
			},
			want: nil,
		},
		{
			name: "scan_dirs limits the walk",
			lint: FlakeInputShadow{ScanDirs: []string{"nix"}},
			files: map[string]string{
				"flake.nix":   flake,
				"nix/in.nix":  "{ pkgs, ... }: { x = pkgs.myinput; }\n",
				"other/o.nix": "{ pkgs, ... }: { x = pkgs.myinput; }\n",
			},
			want: []string{"nix/in.nix:1"},
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

func TestReadFlakeInputs(t *testing.T) {
	root := newTestRepo(t, map[string]string{
		"flake.nix": `{
  inputs = {
    nixpkgs.url = "x";
    foo.url = "y";
    bar = {
      url = "z";
      inputs.nixpkgs.follows = "nixpkgs";
    };
    baz-quux.url = "w";
  };
}
`,
	})
	got, err := readFlakeInputs(root + "/flake.nix")
	if err != nil {
		t.Fatalf("readFlakeInputs: %v", err)
	}
	sort.Strings(got)
	want := []string{"bar", "baz-quux", "foo", "nixpkgs"}
	if !equalStrings(got, want) {
		t.Fatalf("got %v want %v", got, want)
	}
}

func locTag(i Issue) string {
	return i.Path + ":" + itoa(i.Line)
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b [12]byte
	pos := len(b)
	for n > 0 {
		pos--
		b[pos] = byte('0' + n%10)
		n /= 10
	}
	return string(b[pos:])
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
