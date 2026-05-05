package matter

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestParseMatterTOMLBasic(t *testing.T) {
	src := `
# top-level comment
[fleet]
max_workers = 5

[matter.linear]
enabled = true
team = "MAR"
ready_state = 'Ready'   # quoted with comment
api_key_env = LINEAR_API_KEY

[matter.github-issues]
enabled = false
repo = "org/repo"
`
	got, err := LoadFromString(src)
	if err != nil {
		t.Fatalf("LoadFromString: %v", err)
	}
	want := []Config{
		{
			Name:    "github-issues",
			Enabled: false,
			Options: map[string]string{"repo": "org/repo"},
		},
		{
			Name:    "linear",
			Enabled: true,
			Options: map[string]string{
				"team":        "MAR",
				"ready_state": "Ready",
				"api_key_env": "LINEAR_API_KEY",
			},
		},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %#v\nwant %#v", got, want)
	}
}

func TestParseMatterTOMLBoolForms(t *testing.T) {
	cases := map[string]bool{
		"true": true, "yes": true, "on": true, "1": true,
		"false": false, "no": false, "off": false, "0": false,
	}
	for v, want := range cases {
		src := "[matter.x]\nenabled = " + v + "\n"
		got, err := LoadFromString(src)
		if err != nil {
			t.Fatalf("%q: %v", v, err)
		}
		if len(got) != 1 || got[0].Enabled != want {
			t.Errorf("%q: enabled=%v, want %v", v, got[0].Enabled, want)
		}
	}
	if _, err := LoadFromString("[matter.x]\nenabled = maybe\n"); err == nil {
		t.Error("want error for non-boolean enabled")
	}
}

func TestParseMatterTOMLMalformed(t *testing.T) {
	cases := []string{
		"[matter.x]\nno_equals\n",
		"[matter.]\nenabled = true\n",
	}
	for _, src := range cases {
		if _, err := LoadFromString(src); err == nil {
			t.Errorf("want error for %q", src)
		}
	}
}

func TestLoadFromProjectMissingFile(t *testing.T) {
	dir := t.TempDir()
	got, err := LoadFromProject(dir)
	if err != nil {
		t.Fatalf("missing spore.toml should not error: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("want empty slice, got %v", got)
	}
}

func TestLoadFromProjectFileWins(t *testing.T) {
	dir := t.TempDir()
	src := `[matter.linear]
enabled = true
team = "MAR"
`
	if err := os.WriteFile(filepath.Join(dir, "spore.toml"), []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	got, err := LoadFromProject(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Name != "linear" || !got[0].Enabled || got[0].Options["team"] != "MAR" {
		t.Errorf("unexpected configs: %#v", got)
	}
}

func TestLoadFromProjectEnvOverridesAndAdds(t *testing.T) {
	dir := t.TempDir()
	src := `[matter.linear]
enabled = true
team = "MAR"
ready_state = "Ready"
`
	if err := os.WriteFile(filepath.Join(dir, "spore.toml"), []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("SPORE_MATTER_LINEAR__TEAM", "OVR")
	t.Setenv("SPORE_MATTER_GITHUB_ISSUES__ENABLED", "1")
	t.Setenv("SPORE_MATTER_GITHUB_ISSUES__REPO", "org/repo")

	got, err := LoadFromProject(dir)
	if err != nil {
		t.Fatal(err)
	}
	byName := map[string]Config{}
	for _, c := range got {
		byName[c.Name] = c
	}
	linear, ok := byName["linear"]
	if !ok {
		t.Fatal("linear missing")
	}
	if linear.Options["team"] != "OVR" {
		t.Errorf("env should override team: %q", linear.Options["team"])
	}
	if linear.Options["ready_state"] != "Ready" {
		t.Errorf("untouched key should survive: %q", linear.Options["ready_state"])
	}
	gh, ok := byName["github_issues"]
	if !ok {
		t.Fatalf("env-only matter missing; got names %v", keys(byName))
	}
	if !gh.Enabled || gh.Options["repo"] != "org/repo" {
		t.Errorf("env-only matter wrong: %#v", gh)
	}
}

func TestEnvOverrideMatchesDashedTOMLName(t *testing.T) {
	dir := t.TempDir()
	src := `[matter.github-issues]
enabled = true
repo = "org/old"
`
	if err := os.WriteFile(filepath.Join(dir, "spore.toml"), []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("SPORE_MATTER_GITHUB_ISSUES__REPO", "org/new")

	got, err := LoadFromProject(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Fatalf("want 1 config, got %#v", got)
	}
	if got[0].Name != "github-issues" {
		t.Errorf("name should keep TOML form: %q", got[0].Name)
	}
	if got[0].Options["repo"] != "org/new" {
		t.Errorf("env should override the dashed TOML entry, got %q", got[0].Options["repo"])
	}
}

func keys(m map[string]Config) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

func TestStripComment(t *testing.T) {
	cases := map[string]string{
		`key = "value # not a comment" # comment`: `key = "value # not a comment" `,
		`key = bare # tail`:                       `key = bare `,
		`# whole line`:                            ``,
		`'single # quoted' # tail`:                `'single # quoted' `,
	}
	for in, want := range cases {
		if got := stripComment(in); got != want {
			t.Errorf("stripComment(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestStripQuotes(t *testing.T) {
	cases := map[string]string{
		`"x"`: `x`,
		`'x'`: `x`,
		`x`:   `x`,
		`""`:  ``,
		`"`:   `"`,
	}
	for in, want := range cases {
		if got := stripQuotes(in); got != want {
			t.Errorf("stripQuotes(%q) = %q, want %q", in, got, want)
		}
	}
}

// Sanity check: confirm the key constants we promise the rest of the
// codebase keep their expected names. Frontmatter keys are part of the
// task-file format contract.
func TestFrontmatterKeyConstants(t *testing.T) {
	if MatterKey != "matter" || MatterIDKey != "matter_id" || MatterURLKey != "matter_url" {
		t.Errorf("frontmatter key drift: %s / %s / %s", MatterKey, MatterIDKey, MatterURLKey)
	}
	if !strings.HasPrefix(EnvPrefix, "SPORE_MATTER_") {
		t.Errorf("EnvPrefix drift: %q", EnvPrefix)
	}
}
