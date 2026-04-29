package composer

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", filepath.Dir(path), err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

func setupPool(t *testing.T) (rulesDir, consumersDir string) {
	t.Helper()
	root := t.TempDir()
	rulesDir = filepath.Join(root, "rules")
	consumersDir = filepath.Join(root, "consumers")
	writeFile(t, filepath.Join(rulesDir, "core", "a.md"), "# a body\n")
	writeFile(t, filepath.Join(rulesDir, "core", "b.md"), "## b body\n")
	return rulesDir, consumersDir
}

func TestCompose_GoldenConcat(t *testing.T) {
	rulesDir, consumersDir := setupPool(t)
	consumer := filepath.Join(consumersDir, "test.txt")
	writeFile(t, consumer, "core/a\ncore/b\n")

	got, err := Compose(rulesDir, consumer, Options{})
	if err != nil {
		t.Fatalf("Compose: %v", err)
	}
	want := "# a body\n\n## b body\n"
	if got != want {
		t.Fatalf("Compose mismatch:\n got: %q\nwant: %q", got, want)
	}
}

func TestCompose_MissingRule(t *testing.T) {
	rulesDir, consumersDir := setupPool(t)
	consumer := filepath.Join(consumersDir, "test.txt")
	writeFile(t, consumer, "core/a\ncore/missing\n")

	_, err := Compose(rulesDir, consumer, Options{})
	if err == nil {
		t.Fatalf("Compose: expected error for missing rule, got nil")
	}
	if !strings.Contains(err.Error(), "core/missing") {
		t.Fatalf("Compose: error %q does not name missing rule", err)
	}
}

func TestCompose_CommentsAndBlanks(t *testing.T) {
	rulesDir, consumersDir := setupPool(t)
	consumer := filepath.Join(consumersDir, "test.txt")
	writeFile(t, consumer, "# header comment\n\ncore/a\n   \n# trailing comment\ncore/b\n\n")

	got, err := Compose(rulesDir, consumer, Options{})
	if err != nil {
		t.Fatalf("Compose: %v", err)
	}
	want := "# a body\n\n## b body\n"
	if got != want {
		t.Fatalf("Compose mismatch:\n got: %q\nwant: %q", got, want)
	}
}

func TestCompose_MissingConsumer(t *testing.T) {
	rulesDir, consumersDir := setupPool(t)
	consumer := filepath.Join(consumersDir, "absent.txt")

	_, err := Compose(rulesDir, consumer, Options{})
	if err == nil {
		t.Fatalf("Compose: expected error for missing consumer, got nil")
	}
}

func TestCompose_PredicateOff(t *testing.T) {
	rulesDir, consumersDir := setupPool(t)
	consumer := filepath.Join(consumersDir, "test.txt")
	writeFile(t, consumer, "core/a\n?align core/b\n")

	got, err := Compose(rulesDir, consumer, Options{})
	if err != nil {
		t.Fatalf("Compose: %v", err)
	}
	want := "# a body\n"
	if got != want {
		t.Fatalf("Compose predicate-off:\n got: %q\nwant: %q", got, want)
	}
}

func TestCompose_PredicateOn(t *testing.T) {
	rulesDir, consumersDir := setupPool(t)
	consumer := filepath.Join(consumersDir, "test.txt")
	writeFile(t, consumer, "core/a\n?align core/b\n")

	got, err := Compose(rulesDir, consumer, Options{Predicates: map[string]bool{"align": true}})
	if err != nil {
		t.Fatalf("Compose: %v", err)
	}
	want := "# a body\n\n## b body\n"
	if got != want {
		t.Fatalf("Compose predicate-on:\n got: %q\nwant: %q", got, want)
	}
}

func TestCompose_PredicateMalformed(t *testing.T) {
	rulesDir, consumersDir := setupPool(t)
	consumer := filepath.Join(consumersDir, "test.txt")
	writeFile(t, consumer, "core/a\n?align\n")

	_, err := Compose(rulesDir, consumer, Options{})
	if err == nil {
		t.Fatal("Compose: expected error on malformed predicate line, got nil")
	}
}
