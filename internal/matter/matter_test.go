package matter

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadLinearConfigMissing(t *testing.T) {
	t.Setenv(LinearTOMLEnv, "")
	root := t.TempDir()
	cfg, err := LoadLinearConfig(root)
	if err != nil {
		t.Fatalf("LoadLinearConfig: %v", err)
	}
	if cfg != nil {
		t.Fatalf("expected nil config when spore.toml missing, got %+v", cfg)
	}
}

func TestLoadLinearConfigOverridePath(t *testing.T) {
	dir := t.TempDir()
	override := filepath.Join(dir, "matter.toml")
	body := `[matter.linear]
team = "OVR"
api_key_env = "LINEAR_API_KEY"
`
	if err := os.WriteFile(override, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv(LinearTOMLEnv, override)
	root := t.TempDir() // intentionally without a spore.toml
	cfg, err := LoadLinearConfig(root)
	if err != nil {
		t.Fatalf("LoadLinearConfig: %v", err)
	}
	if cfg == nil || cfg.Team != "OVR" {
		t.Fatalf("expected override-loaded config team=OVR, got %+v", cfg)
	}
}

func TestLoadLinearConfigSectionAbsent(t *testing.T) {
	t.Setenv(LinearTOMLEnv, "")
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "spore.toml"),
		[]byte("[fleet]\nmax_workers = 3\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, err := LoadLinearConfig(root)
	if err != nil {
		t.Fatalf("LoadLinearConfig: %v", err)
	}
	if cfg != nil {
		t.Fatalf("expected nil config without [matter.linear], got %+v", cfg)
	}
}

func TestLoadLinearConfigDefaults(t *testing.T) {
	t.Setenv(LinearTOMLEnv, "")
	root := t.TempDir()
	body := `
[matter.linear]
team = "MAR"
api_key_env = "LINEAR_API_KEY"
`
	if err := os.WriteFile(filepath.Join(root, "spore.toml"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, err := LoadLinearConfig(root)
	if err != nil {
		t.Fatalf("LoadLinearConfig: %v", err)
	}
	if cfg == nil {
		t.Fatal("expected non-nil config")
	}
	if cfg.Team != "MAR" {
		t.Errorf("Team = %q, want MAR", cfg.Team)
	}
	if cfg.ReadyState != "Ready" {
		t.Errorf("ReadyState = %q, want Ready", cfg.ReadyState)
	}
	if cfg.InProgressState != "In Progress" {
		t.Errorf("InProgressState = %q, want In Progress", cfg.InProgressState)
	}
	if cfg.DoneState != "Done" {
		t.Errorf("DoneState = %q, want Done", cfg.DoneState)
	}
	if cfg.Endpoint != "https://api.linear.app/graphql" {
		t.Errorf("Endpoint = %q, want default", cfg.Endpoint)
	}
	if cfg.APIKeyEnv != "LINEAR_API_KEY" {
		t.Errorf("APIKeyEnv = %q", cfg.APIKeyEnv)
	}
}

func TestLoadLinearConfigOverrides(t *testing.T) {
	t.Setenv(LinearTOMLEnv, "")
	root := t.TempDir()
	body := `
[fleet]
max_workers = 4

[matter.linear]
team = "MAR"
ready_state = "Triaged"
in_progress_state = "Doing"
done_state = "Shipped"
api_key_file = "linear-token"
endpoint = "https://example.test/gql"
`
	if err := os.WriteFile(filepath.Join(root, "spore.toml"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, err := LoadLinearConfig(root)
	if err != nil {
		t.Fatalf("LoadLinearConfig: %v", err)
	}
	if cfg.ReadyState != "Triaged" || cfg.InProgressState != "Doing" || cfg.DoneState != "Shipped" {
		t.Errorf("state overrides not applied: %+v", cfg)
	}
	if cfg.APIKeyFile != "linear-token" {
		t.Errorf("APIKeyFile = %q", cfg.APIKeyFile)
	}
	if cfg.Endpoint != "https://example.test/gql" {
		t.Errorf("Endpoint = %q", cfg.Endpoint)
	}
}

func TestLoadLinearConfigRequiresTeam(t *testing.T) {
	t.Setenv(LinearTOMLEnv, "")
	root := t.TempDir()
	body := `
[matter.linear]
api_key_env = "LINEAR_API_KEY"
`
	if err := os.WriteFile(filepath.Join(root, "spore.toml"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadLinearConfig(root); err == nil {
		t.Fatal("expected error when team missing")
	}
}

func TestLoadLinearConfigRequiresKeySource(t *testing.T) {
	t.Setenv(LinearTOMLEnv, "")
	root := t.TempDir()
	body := `
[matter.linear]
team = "MAR"
`
	if err := os.WriteFile(filepath.Join(root, "spore.toml"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadLinearConfig(root); err == nil {
		t.Fatal("expected error when api_key_env and api_key_file both empty")
	}
}

func TestResolveAPIKeyEnv(t *testing.T) {
	t.Setenv("LINEAR_API_KEY", "lin_abc")
	cfg := &LinearConfig{APIKeyEnv: "LINEAR_API_KEY"}
	got, err := cfg.ResolveAPIKey()
	if err != nil {
		t.Fatalf("ResolveAPIKey: %v", err)
	}
	if got != "lin_abc" {
		t.Errorf("ResolveAPIKey = %q, want lin_abc", got)
	}
}

func TestResolveAPIKeyEnvMissing(t *testing.T) {
	t.Setenv("LINEAR_NOPE", "")
	cfg := &LinearConfig{APIKeyEnv: "LINEAR_NOPE"}
	if _, err := cfg.ResolveAPIKey(); err == nil {
		t.Fatal("expected error when env unset")
	}
}

func TestResolveAPIKeyFileAbs(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "key")
	if err := os.WriteFile(p, []byte("lin_xyz\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg := &LinearConfig{APIKeyFile: p}
	got, err := cfg.ResolveAPIKey()
	if err != nil {
		t.Fatalf("ResolveAPIKey: %v", err)
	}
	if got != "lin_xyz" {
		t.Errorf("ResolveAPIKey = %q, want lin_xyz", got)
	}
}

func TestResolveAPIKeyFileRelativeToCredentialsDir(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "linear-token"), []byte("lin_creds"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("CREDENTIALS_DIRECTORY", dir)
	cfg := &LinearConfig{APIKeyFile: "linear-token"}
	got, err := cfg.ResolveAPIKey()
	if err != nil {
		t.Fatalf("ResolveAPIKey: %v", err)
	}
	if got != "lin_creds" {
		t.Errorf("ResolveAPIKey = %q, want lin_creds", got)
	}
}
