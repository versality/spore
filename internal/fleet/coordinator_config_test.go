package fleet

import (
	"os"
	"path/filepath"
	"testing"
)

func TestParseCoordinatorTOML(t *testing.T) {
	cases := []struct {
		name    string
		input   string
		want    CoordinatorConfig
		wantErr bool
	}{
		{
			name: "all keys",
			input: `
[coordinator]
driver = "claude"
model  = "opus"
brief  = "docs/helm.md"
`,
			want: CoordinatorConfig{Driver: "claude", Model: "opus", Brief: "docs/helm.md"},
		},
		{
			name:  "single quotes",
			input: "[coordinator]\ndriver = 'codex'\n",
			want:  CoordinatorConfig{Driver: "codex"},
		},
		{
			name: "comments and blanks",
			input: `
# leading comment
[coordinator]
# inline driver below
driver = "claude" # trailing comment
`,
			want: CoordinatorConfig{Driver: "claude"},
		},
		{
			name: "other section ignored",
			input: `
[fleet]
max_workers = 2
[coordinator]
driver = "claude"
`,
			want: CoordinatorConfig{Driver: "claude"},
		},
		{
			name:  "external_session_pattern",
			input: "[coordinator]\nexternal_session_pattern = \"^helm-.*\"\n",
			want:  CoordinatorConfig{ExternalSessionPattern: "^helm-.*"},
		},
		{
			name:    "unknown key errors",
			input:   "[coordinator]\nbogus = 1\n",
			wantErr: true,
		},
		{
			name:    "malformed line errors",
			input:   "[coordinator]\nnoequals\n",
			wantErr: true,
		},
		{
			name:  "no section returns zero",
			input: "[fleet]\nmax_workers = 2\n",
			want:  CoordinatorConfig{},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := parseCoordinatorTOML(tc.input)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("expected error, got nil; result=%+v", got)
				}
				return
			}
			if err != nil {
				t.Fatalf("parseCoordinatorTOML: %v", err)
			}
			if got != tc.want {
				t.Errorf("got %+v, want %+v", got, tc.want)
			}
		})
	}
}

func TestLoadCoordinatorConfigMissingFile(t *testing.T) {
	dir := t.TempDir()
	got, err := LoadCoordinatorConfig(dir)
	if err != nil {
		t.Fatalf("LoadCoordinatorConfig on empty dir: %v", err)
	}
	if (got != CoordinatorConfig{}) {
		t.Errorf("expected zero CoordinatorConfig for missing spore.toml, got %+v", got)
	}
}

func TestLoadCoordinatorConfigReadsFile(t *testing.T) {
	dir := t.TempDir()
	body := []byte("[coordinator]\ndriver = \"codex\"\nmodel = \"gpt-5.5\"\nbrief = \"docs/helm.md\"\n")
	if err := os.WriteFile(filepath.Join(dir, "spore.toml"), body, 0o600); err != nil {
		t.Fatalf("write spore.toml: %v", err)
	}
	got, err := LoadCoordinatorConfig(dir)
	if err != nil {
		t.Fatalf("LoadCoordinatorConfig: %v", err)
	}
	want := CoordinatorConfig{Driver: "codex", Model: "gpt-5.5", Brief: "docs/helm.md"}
	if got != want {
		t.Errorf("got %+v, want %+v", got, want)
	}
}

func TestCoordinatorRolePathPrecedence(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "spore.toml"),
		[]byte("[coordinator]\nbrief = \"docs/helm.md\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	t.Run("env wins", func(t *testing.T) {
		t.Setenv(CoordinatorRoleEnv, "/etc/role.md")
		if got := CoordinatorRolePath(dir); got != "/etc/role.md" {
			t.Errorf("env override ignored: got %q", got)
		}
	})

	t.Run("toml relative resolves against root", func(t *testing.T) {
		t.Setenv(CoordinatorRoleEnv, "")
		want := filepath.Join(dir, "docs/helm.md")
		if got := CoordinatorRolePath(dir); got != want {
			t.Errorf("toml relative: got %q, want %q", got, want)
		}
	})

	t.Run("toml absolute passes through", func(t *testing.T) {
		t.Setenv(CoordinatorRoleEnv, "")
		abs := filepath.Join(dir, "abs-helm.md")
		if err := os.WriteFile(filepath.Join(dir, "spore.toml"),
			[]byte("[coordinator]\nbrief = \""+abs+"\"\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		if got := CoordinatorRolePath(dir); got != abs {
			t.Errorf("toml absolute: got %q, want %q", got, abs)
		}
	})

	t.Run("default fallback", func(t *testing.T) {
		empty := t.TempDir()
		t.Setenv(CoordinatorRoleEnv, "")
		want := filepath.Join(empty, "bootstrap", "coordinator", "role.md")
		if got := CoordinatorRolePath(empty); got != want {
			t.Errorf("default fallback: got %q, want %q", got, want)
		}
	})
}

func TestCoordinatorProviderModel(t *testing.T) {
	t.Run("env wins over toml", func(t *testing.T) {
		t.Setenv("SPORE_COORDINATOR_PROVIDER", "envprov")
		t.Setenv("SPORE_COORDINATOR_MODEL", "envmodel")
		cfg := CoordinatorConfig{Driver: "claude", Model: "opus"}
		if got := coordinatorProvider(cfg); got != "envprov" {
			t.Errorf("provider = %q, want envprov", got)
		}
		if got := coordinatorModel(cfg); got != "envmodel" {
			t.Errorf("model = %q, want envmodel", got)
		}
	})
	t.Run("toml fallback", func(t *testing.T) {
		t.Setenv("SPORE_COORDINATOR_PROVIDER", "")
		t.Setenv("SPORE_COORDINATOR_MODEL", "")
		cfg := CoordinatorConfig{Driver: "codex", Model: "gpt-5.5"}
		if got := coordinatorProvider(cfg); got != "codex" {
			t.Errorf("provider = %q, want codex", got)
		}
		if got := coordinatorModel(cfg); got != "gpt-5.5" {
			t.Errorf("model = %q, want gpt-5.5", got)
		}
	})
	t.Run("nothing set", func(t *testing.T) {
		t.Setenv("SPORE_COORDINATOR_PROVIDER", "")
		t.Setenv("SPORE_COORDINATOR_MODEL", "")
		if got := coordinatorProvider(CoordinatorConfig{}); got != "" {
			t.Errorf("provider = %q, want empty", got)
		}
		if got := coordinatorModel(CoordinatorConfig{}); got != "" {
			t.Errorf("model = %q, want empty", got)
		}
	})
}
