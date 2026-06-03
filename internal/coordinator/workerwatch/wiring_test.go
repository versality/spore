package workerwatch

import (
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"testing"
)

func writeTask(t *testing.T, tasksDir, slug, status, agent string) {
	t.Helper()
	if err := os.MkdirAll(tasksDir, 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	body := "---\nstatus: " + status + "\nslug: " + slug + "\ntitle: " + slug + "\n"
	if agent != "" {
		body += "agent: " + agent + "\n"
	}
	body += "---\n"
	if err := os.WriteFile(filepath.Join(tasksDir, slug+".md"), []byte(body), 0o644); err != nil {
		t.Fatalf("WriteFile %s: %v", slug, err)
	}
}

func TestReadProjects(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "projects")
	content := "# header comment\n/a/b\n\n   /c/d  # trailing comment\n   \n/e/f\n"
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	got, err := ReadProjects(path)
	if err != nil {
		t.Fatalf("ReadProjects: %v", err)
	}
	want := []string{"/a/b", "/c/d", "/e/f"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}
}

func TestReadProjectsMissingFile(t *testing.T) {
	got, err := ReadProjects(filepath.Join(t.TempDir(), "nope"))
	if err != nil {
		t.Fatalf("ReadProjects: %v", err)
	}
	if got != nil {
		t.Fatalf("got %v, want nil", got)
	}
}

func TestResolveProjectRootsFallbackToCwd(t *testing.T) {
	dir := t.TempDir()
	cwd, _ := os.Getwd()
	t.Cleanup(func() { _ = os.Chdir(cwd) })
	if err := os.Chdir(dir); err != nil {
		t.Fatalf("Chdir: %v", err)
	}
	got, err := ResolveProjectRoots(filepath.Join(dir, "nonexistent"))
	if err != nil {
		t.Fatalf("ResolveProjectRoots: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("len = %d, want 1 (fallback)", len(got))
	}
}

func TestResolveProjectRootsHonorsFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "projects")
	if err := os.WriteFile(path, []byte("/p/one\n/p/two\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	got, err := ResolveProjectRoots(path)
	if err != nil {
		t.Fatalf("ResolveProjectRoots: %v", err)
	}
	want := []string{"/p/one", "/p/two"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}
}

func TestScanActiveSingleProject(t *testing.T) {
	root := t.TempDir()
	tasksDir := filepath.Join(root, "tasks")
	writeTask(t, tasksDir, "foo", "active", "")
	writeTask(t, tasksDir, "bar", "draft", "")
	writeTask(t, tasksDir, "baz", "done", "")
	writeTask(t, tasksDir, "qux", "active", "opencode")

	refs, err := ScanActive([]string{root})
	if err != nil {
		t.Fatalf("ScanActive: %v", err)
	}
	if len(refs) != 2 {
		t.Fatalf("len = %d, want 2 (foo + qux)", len(refs))
	}
	sort.Slice(refs, func(i, j int) bool { return refs[i].BaseSlug < refs[j].BaseSlug })
	if refs[0].BaseSlug != "foo" || refs[0].Slug != "foo" {
		t.Errorf("refs[0] = %+v", refs[0])
	}
	if refs[0].Agent != "claude" {
		t.Errorf("missing agent default = %q, want claude", refs[0].Agent)
	}
	if refs[1].BaseSlug != "qux" || refs[1].Agent != "opencode" {
		t.Errorf("refs[1] = %+v", refs[1])
	}
	// Single-project mode: Slug equals BaseSlug, no project prefix.
	if refs[0].Slug != refs[0].BaseSlug || refs[1].Slug != refs[1].BaseSlug {
		t.Errorf("single-project slug got project-prefixed: %+v", refs)
	}
}

func TestScanActiveMultiProjectPrefixesSlug(t *testing.T) {
	rootA := filepath.Join(t.TempDir(), "alpha")
	rootB := filepath.Join(t.TempDir(), "beta")
	writeTask(t, filepath.Join(rootA, "tasks"), "x", "active", "")
	writeTask(t, filepath.Join(rootB, "tasks"), "y", "active", "")

	refs, err := ScanActive([]string{rootA, rootB})
	if err != nil {
		t.Fatalf("ScanActive: %v", err)
	}
	if len(refs) != 2 {
		t.Fatalf("len = %d, want 2", len(refs))
	}
	sort.Slice(refs, func(i, j int) bool { return refs[i].BaseSlug < refs[j].BaseSlug })
	if refs[0].Slug != "alpha/x" || refs[0].BaseSlug != "x" || refs[0].ProjectRoot != rootA {
		t.Errorf("refs[0] = %+v", refs[0])
	}
	if refs[1].Slug != "beta/y" || refs[1].BaseSlug != "y" || refs[1].ProjectRoot != rootB {
		t.Errorf("refs[1] = %+v", refs[1])
	}
}

func TestScanActiveSkipsMissingTasksDir(t *testing.T) {
	// Project root with no tasks/ dir must not propagate ENOENT - the
	// watcher needs to keep going for sibling projects.
	rootA := t.TempDir()
	rootB := t.TempDir()
	writeTask(t, filepath.Join(rootB, "tasks"), "y", "active", "")

	refs, err := ScanActive([]string{rootA, rootB})
	if err != nil {
		t.Fatalf("ScanActive: %v", err)
	}
	if len(refs) != 1 || refs[0].BaseSlug != "y" {
		t.Fatalf("refs = %+v, want [y]", refs)
	}
}

func TestResolveDisappearanceMissingFile(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "tasks"), 0o755); err != nil {
		t.Fatal(err)
	}
	got := ResolveDisappearance("slug", "slug", root)
	if got.Status != "missing" {
		t.Fatalf("Status = %q, want missing", got.Status)
	}
}

func TestResolveDisappearanceReadsStatus(t *testing.T) {
	root := t.TempDir()
	tasksDir := filepath.Join(root, "tasks")
	writeTask(t, tasksDir, "foo", "blocked", "")
	got := ResolveDisappearance("foo", "foo", root)
	if got.Status != "blocked" {
		t.Fatalf("Status = %q, want blocked", got.Status)
	}
	if got.Verdict != "" {
		t.Fatalf("Verdict = %q on non-done, want empty", got.Verdict)
	}
}

func TestResolveDisappearanceFillsVerdictOnDone(t *testing.T) {
	root := t.TempDir()
	tasksDir := filepath.Join(root, "tasks")
	writeTask(t, tasksDir, "foo", "done", "")
	got := ResolveDisappearance("foo", "foo", root)
	if got.Status != "done" {
		t.Fatalf("Status = %q, want done", got.Status)
	}
	// Verdict comes from verify.Verify; we don't pin a specific verdict
	// (it depends on the brief contents), only that the field gets
	// populated for the done branch.
	if got.Verdict == "" {
		t.Fatalf("Verdict empty on done; want some verify.Verify result")
	}
}

func TestResolveDisappearanceMalformedFrontmatter(t *testing.T) {
	root := t.TempDir()
	tasksDir := filepath.Join(root, "tasks")
	if err := os.MkdirAll(tasksDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(tasksDir, "foo.md"), []byte("no frontmatter here"), 0o644); err != nil {
		t.Fatal(err)
	}
	got := ResolveDisappearance("foo", "foo", root)
	if got.Status != "?" {
		t.Fatalf("Status = %q, want ?", got.Status)
	}
}
