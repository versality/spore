package bootstrap

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestDetectValidationGreenCleanRepo(t *testing.T) {
	root := t.TempDir()
	mustGitInit(t, root)
	writeFile(t, filepath.Join(root, "README.md"), []byte("# x\n"))
	notes, err := detectValidationGreen(root)
	if err != nil {
		t.Fatalf("detect: %v", err)
	}
	if !strings.Contains(notes, "emdash") || !strings.Contains(notes, ": 0") {
		t.Errorf("notes=%q; want zero-issue summary", notes)
	}
}

func TestDetectValidationGreenFlagsEmDash(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "main.go"), []byte("package main\n// has \u2014 dash\n"))
	mustGitInit(t, root)
	_, err := detectValidationGreen(root)
	if err == nil || !strings.Contains(err.Error(), "emdash") {
		t.Fatalf("err=%v; want emdash blocker", err)
	}
}
