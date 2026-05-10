package initconfig

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/versality/spore/internal/align"
	"github.com/versality/spore/internal/fleet"
	"github.com/versality/spore/internal/matter"
)

func TestDefaultTOMLParsesAcrossKernel(t *testing.T) {
	dir := t.TempDir()
	res, err := Run(dir, Options{})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !res.Created {
		t.Fatalf("expected Created=true, got %+v", res)
	}
	body, err := os.ReadFile(res.Path)
	if err != nil {
		t.Fatalf("read written file: %v", err)
	}
	if string(body) != DefaultTOML() {
		t.Fatalf("written file diverges from DefaultTOML")
	}

	wcfg, err := fleet.LoadWorkersConfig(dir)
	if err != nil {
		t.Fatalf("LoadWorkersConfig: %v", err)
	}
	if wcfg.Default != "claude" {
		t.Errorf("workers default = %q, want claude", wcfg.Default)
	}
	if wcfg.Ratio["claude"] != 67 || wcfg.Ratio["codex"] != 33 {
		t.Errorf("workers ratio = %+v, want claude=67 codex=33", wcfg.Ratio)
	}

	ccfg, err := fleet.LoadCoordinatorConfig(dir)
	if err != nil {
		t.Fatalf("LoadCoordinatorConfig: %v", err)
	}
	if (ccfg != fleet.CoordinatorConfig{}) {
		t.Errorf("coordinator should be all-empty (commented out), got %+v", ccfg)
	}

	crit, err := align.LoadCriteria(dir)
	if err != nil {
		t.Fatalf("LoadCriteria: %v", err)
	}
	if crit != align.DefaultCriteria() {
		t.Errorf("align criteria should match defaults, got %+v", crit)
	}

	matters, err := matter.LoadFromString(string(body))
	if err != nil {
		t.Fatalf("LoadFromString: %v", err)
	}
	if len(matters) != 0 {
		t.Errorf("matter should be empty (linear is commented), got %+v", matters)
	}
}

func TestRunNoOpOnExistingFile(t *testing.T) {
	dir := t.TempDir()
	original := []byte("[fleet.workers]\ndefault = \"codex\"\n")
	path := filepath.Join(dir, "spore.toml")
	if err := os.WriteFile(path, original, 0o644); err != nil {
		t.Fatalf("seed: %v", err)
	}
	res, err := Run(dir, Options{})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !res.NoOp {
		t.Fatalf("expected NoOp=true, got %+v", res)
	}
	got, _ := os.ReadFile(path)
	if string(got) != string(original) {
		t.Fatalf("file mutated despite NoOp\nwant %q\ngot  %q", original, got)
	}
}

func TestRunForceOverwrites(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "spore.toml")
	if err := os.WriteFile(path, []byte("# stale\n"), 0o644); err != nil {
		t.Fatalf("seed: %v", err)
	}
	res, err := Run(dir, Options{Force: true})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !res.Overwritten {
		t.Fatalf("expected Overwritten=true, got %+v", res)
	}
	got, _ := os.ReadFile(path)
	if !strings.Contains(string(got), "[fleet.workers]") {
		t.Fatalf("force overwrite did not produce default template:\n%s", got)
	}
}

func TestRunSectionAppendsToPartialFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "spore.toml")
	original := "[matter.linear]\nenabled = true\nteam = \"MAR\"\n"
	if err := os.WriteFile(path, []byte(original), 0o644); err != nil {
		t.Fatalf("seed: %v", err)
	}
	res, err := Run(dir, Options{Sections: []string{"fleet.workers"}})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(res.Added) != 1 || res.Added[0] != "fleet.workers" {
		t.Fatalf("expected fleet.workers added, got %+v", res)
	}
	got, _ := os.ReadFile(path)
	if !strings.HasPrefix(string(got), original) {
		t.Errorf("original content was not preserved at the head:\n%s", got)
	}
	if !strings.Contains(string(got), "[fleet.workers]") {
		t.Errorf("fleet.workers header missing after append:\n%s", got)
	}
	wcfg, err := fleet.LoadWorkersConfig(dir)
	if err != nil {
		t.Fatalf("LoadWorkersConfig: %v", err)
	}
	if wcfg.Default != "claude" || wcfg.Ratio["codex"] != 33 {
		t.Errorf("appended workers section did not parse: %+v", wcfg)
	}
	mat, err := matter.LoadFromString(string(got))
	if err != nil {
		t.Fatalf("LoadFromString: %v", err)
	}
	if len(mat) != 1 || mat[0].Name != "linear" || !mat[0].Enabled {
		t.Errorf("pre-existing matter.linear was lost: %+v", mat)
	}
}

func TestRunSectionSkipsAlreadyPresent(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "spore.toml")
	original := "[fleet.workers.ratio]\nclaude = 100\n"
	if err := os.WriteFile(path, []byte(original), 0o644); err != nil {
		t.Fatalf("seed: %v", err)
	}
	res, err := Run(dir, Options{Sections: []string{"fleet.workers"}})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !res.NoOp || len(res.Added) != 0 {
		t.Fatalf("expected NoOp with no additions, got %+v", res)
	}
	got, _ := os.ReadFile(path)
	if string(got) != original {
		t.Errorf("file mutated despite skip: %q", got)
	}
}

func TestRunSectionCreatesFileIfMissing(t *testing.T) {
	dir := t.TempDir()
	res, err := Run(dir, Options{Sections: []string{"align"}})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !res.Created || len(res.Added) != 1 || res.Added[0] != "align" {
		t.Fatalf("expected Created with align added, got %+v", res)
	}
	got, err := os.ReadFile(res.Path)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if !strings.HasPrefix(string(got), "# spore.toml") {
		t.Errorf("expected header at top of new file:\n%s", got)
	}
	if !strings.Contains(string(got), "[align]") {
		t.Errorf("align block missing:\n%s", got)
	}
}

func TestRunSectionUnknownNameErrors(t *testing.T) {
	dir := t.TempDir()
	_, err := Run(dir, Options{Sections: []string{"bogus"}})
	if err == nil {
		t.Fatal("expected error for unknown section")
	}
}

func TestPresentSectionsIgnoresCommented(t *testing.T) {
	have := presentSections("# [fleet.workers]\n[align]\n")
	if have["[fleet.workers]"] {
		t.Errorf("commented header should not register as present")
	}
	if !have["[align]"] {
		t.Errorf("active header missed")
	}
}
