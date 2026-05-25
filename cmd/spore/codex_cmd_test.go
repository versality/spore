package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCodexTrustStatusAndAdd(t *testing.T) {
	root := t.TempDir()
	t.Chdir(root)
	if err := os.WriteFile("spore.toml", []byte("[fleet]\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	home := t.TempDir()
	t.Setenv("CODEX_HOME", home)

	code, stdout, _ := captureFn(t, func() int { return runCodex([]string{"trust", "status"}) })
	if code == 0 || !strings.Contains(stdout, "untrusted") {
		t.Fatalf("status code=%d stdout=%q", code, stdout)
	}
	if code := runCodex([]string{"trust", "add", "--yes"}); code != 0 {
		t.Fatalf("add code=%d", code)
	}
	body, err := os.ReadFile(filepath.Join(home, "config.toml"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(body), "trust_level = \"trusted\"") {
		t.Fatalf("config = %s", body)
	}
	code, stdout, _ = captureFn(t, func() int { return runCodex([]string{"trust", "status"}) })
	if code != 0 || !strings.Contains(stdout, "trusted") {
		t.Fatalf("status after add code=%d stdout=%q", code, stdout)
	}
}

func TestCodexTrustAddRequiresYes(t *testing.T) {
	root := t.TempDir()
	t.Chdir(root)
	if err := os.WriteFile("spore.toml", []byte("[fleet]\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("CODEX_HOME", t.TempDir())

	if code := runCodex([]string{"trust", "add"}); code != 2 {
		t.Fatalf("add without --yes code=%d, want 2", code)
	}
}
