package install_test

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	spore "github.com/versality/spore"
	"github.com/versality/spore/internal/install"
)

func TestInstallDropsSkillsWithCorrectModes(t *testing.T) {
	root := t.TempDir()
	res, err := install.Install(root, spore.BundledSkills, "bootstrap/skills", ".claude/skills")
	if err != nil {
		t.Fatalf("Install: %v", err)
	}
	if len(res.Written) == 0 {
		t.Fatal("Install wrote 0 files; want > 0")
	}

	skillSkillMd := filepath.Join(root, ".claude", "skills", "spore-bootstrap", "SKILL.md")
	body, err := os.ReadFile(skillSkillMd)
	if err != nil {
		t.Fatalf("read SKILL.md: %v", err)
	}
	if !bytes.Contains(body, []byte("name: spore-bootstrap")) {
		t.Errorf("SKILL.md missing frontmatter; got first 80 bytes: %q", body[:min(80, len(body))])
	}

	diagramScript := filepath.Join(root, ".claude", "skills", "diagram", "diagram")
	info, err := os.Stat(diagramScript)
	if err != nil {
		t.Fatalf("stat diagram script: %v", err)
	}
	if info.Mode().Perm()&0o100 == 0 {
		t.Errorf("diagram script not executable: mode=%v", info.Mode().Perm())
	}

	skillReadme := filepath.Join(root, ".claude", "skills", "spore-bootstrap", "README.md")
	info, err = os.Stat(skillReadme)
	if err != nil {
		t.Fatalf("stat README.md: %v", err)
	}
	if info.Mode().Perm()&0o100 != 0 {
		t.Errorf("README.md unexpectedly executable: mode=%v", info.Mode().Perm())
	}
}

func TestInstallIsIdempotent(t *testing.T) {
	root := t.TempDir()
	first, err := install.Install(root, spore.BundledSkills, "bootstrap/skills", ".claude/skills")
	if err != nil {
		t.Fatalf("Install #1: %v", err)
	}
	second, err := install.Install(root, spore.BundledSkills, "bootstrap/skills", ".claude/skills")
	if err != nil {
		t.Fatalf("Install #2: %v", err)
	}
	if len(second.Written) != 0 {
		t.Errorf("Install #2 Written=%d; want 0 (idempotent)", len(second.Written))
	}
	if len(second.Skipped) != len(first.Written) {
		t.Errorf("Install #2 Skipped=%d; want %d", len(second.Skipped), len(first.Written))
	}
}

func TestInstallOverwritesDriftedFile(t *testing.T) {
	root := t.TempDir()
	if _, err := install.Install(root, spore.BundledSkills, "bootstrap/skills", ".claude/skills"); err != nil {
		t.Fatalf("Install #1: %v", err)
	}
	target := filepath.Join(root, ".claude", "skills", "spore-bootstrap", "SKILL.md")
	if err := os.WriteFile(target, []byte("# drifted\n"), 0o644); err != nil {
		t.Fatalf("seed drift: %v", err)
	}
	res, err := install.Install(root, spore.BundledSkills, "bootstrap/skills", ".claude/skills")
	if err != nil {
		t.Fatalf("Install #2: %v", err)
	}
	found := false
	for _, w := range res.Written {
		if w == target {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("drifted file not in Written: %v", res.Written)
	}
	body, err := os.ReadFile(target)
	if err != nil {
		t.Fatalf("read after restore: %v", err)
	}
	if !bytes.Contains(body, []byte("name: spore-bootstrap")) {
		t.Errorf("drifted file not restored to embedded content")
	}
}

func TestInstallEmptyRootReturnsError(t *testing.T) {
	_, err := install.Install("", spore.BundledSkills, "bootstrap/skills", ".claude/skills")
	if err == nil {
		t.Fatal("Install(\"\") returned nil error; want non-nil")
	}
}

func TestInstallDropsScriptsExecutable(t *testing.T) {
	root := t.TempDir()
	res, err := install.Install(root, spore.BundledScripts, "bootstrap/scripts", "harness")
	if err != nil {
		t.Fatalf("Install scripts: %v", err)
	}
	if len(res.Written) == 0 {
		t.Fatal("Install wrote 0 script files; want > 0")
	}

	for _, name := range []string{"hooks-render.sh", "auto-commit-tasks.sh", "quiet-run.sh", "report-main-worktree-dirty.sh"} {
		path := filepath.Join(root, "harness", name)
		info, err := os.Stat(path)
		if err != nil {
			t.Fatalf("stat %s: %v", name, err)
		}
		if info.Mode().Perm()&0o100 == 0 {
			t.Errorf("%s not executable: mode=%v", name, info.Mode().Perm())
		}
	}

	readme := filepath.Join(root, "harness", "README.md")
	info, err := os.Stat(readme)
	if err != nil {
		t.Fatalf("stat README.md: %v", err)
	}
	if info.Mode().Perm()&0o100 != 0 {
		t.Errorf("README.md unexpectedly executable: mode=%v", info.Mode().Perm())
	}
}

func TestInstallEmptyDestSubpathReturnsError(t *testing.T) {
	_, err := install.Install(t.TempDir(), spore.BundledSkills, "bootstrap/skills", "")
	if err == nil {
		t.Fatal("Install(destSubpath=\"\") returned nil error; want non-nil")
	}
}

func TestInstallIfMissingCreatesConfigs(t *testing.T) {
	root := t.TempDir()
	res, err := install.InstallIfMissing(root, spore.BundledConfigs, "configs", "configs")
	if err != nil {
		t.Fatalf("InstallIfMissing: %v", err)
	}
	for _, rel := range []string{
		filepath.Join("configs", "codex", "hooks-config.json"),
		filepath.Join("configs", "claude", "hooks-config.json"),
		filepath.Join("configs", "claude", "settings-extras.json"),
	} {
		if _, err := os.Stat(filepath.Join(root, rel)); err != nil {
			t.Fatalf("stat %s: %v", rel, err)
		}
	}
	if len(res.Written) != 3 {
		t.Fatalf("Written=%d, want 3: %v", len(res.Written), res.Written)
	}
}

func TestInstallIfMissingPreservesCustomizedConfig(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "configs", "codex", "hooks-config.json")
	custom := []byte(`{"custom":true}` + "\n")
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(target, custom, 0o644); err != nil {
		t.Fatal(err)
	}
	res, err := install.InstallIfMissing(root, spore.BundledConfigs, "configs", "configs")
	if err != nil {
		t.Fatalf("InstallIfMissing: %v", err)
	}
	body, err := os.ReadFile(target)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(body, custom) {
		t.Fatalf("custom config overwritten: %q", body)
	}
	foundSkipped := false
	for _, p := range res.Skipped {
		if p == target {
			foundSkipped = true
			break
		}
	}
	if !foundSkipped {
		t.Fatalf("custom config not marked skipped: %+v", res)
	}
}

func TestInstallIfMissingDoesNotRewriteRuntimeHooks(t *testing.T) {
	root := t.TempDir()
	runtime := filepath.Join(root, ".codex", "hooks.json")
	custom := []byte(`{"runtime":"custom"}` + "\n")
	if err := os.MkdirAll(filepath.Dir(runtime), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(runtime, custom, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := install.InstallIfMissing(root, spore.BundledConfigs, "configs", "configs"); err != nil {
		t.Fatalf("InstallIfMissing: %v", err)
	}
	body, err := os.ReadFile(runtime)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(body, custom) {
		t.Fatalf("runtime hook file overwritten: %q", body)
	}
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
