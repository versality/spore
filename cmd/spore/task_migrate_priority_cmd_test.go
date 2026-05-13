package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestMigratePriorityBackfillsByStatus(t *testing.T) {
	dir := t.TempDir()
	files := map[string]string{
		"active.md":  "---\nstatus: active\nslug: active\ntitle: a\n---\n",
		"parked.md":  "---\nstatus: parked\nslug: parked\ntitle: p\n---\n",
		"draft.md":   "---\nstatus: draft\nslug: draft\ntitle: d\n---\n",
		"done.md":    "---\nstatus: done\nslug: done\ntitle: o\n---\n",
		"valid.md":   "---\nstatus: active\nslug: valid\ntitle: v\npriority: high\n---\n",
		"blocked.md": "---\nstatus: blocked\nslug: blocked\ntitle: b\n---\n",
	}
	for name, body := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	if err := runTaskMigratePriority([]string{"--dir", dir}); err != nil {
		t.Fatalf("runTaskMigratePriority: %v", err)
	}

	want := map[string]string{
		"active.md":  "priority: medium",
		"parked.md":  "priority: low",
		"draft.md":   "priority: medium",
		"valid.md":   "priority: high",
		"done.md":    "",
		"blocked.md": "",
	}
	for name, sub := range want {
		raw, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			t.Fatal(err)
		}
		if sub == "" {
			if strings.Contains(string(raw), "priority:") {
				t.Errorf("%s should not have priority:\n%s", name, raw)
			}
			continue
		}
		if !strings.Contains(string(raw), sub) {
			t.Errorf("%s missing %q:\n%s", name, sub, raw)
		}
	}
}

func TestMigratePriorityDryRunLeavesFiles(t *testing.T) {
	dir := t.TempDir()
	body := "---\nstatus: active\nslug: x\ntitle: x\n---\n"
	path := filepath.Join(dir, "x.md")
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := runTaskMigratePriority([]string{"--dir", dir, "--dry-run"}); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(raw) != body {
		t.Errorf("dry-run modified file:\n%s", raw)
	}
}

func TestMigratePriorityRejectsInvalidValue(t *testing.T) {
	dir := t.TempDir()
	body := "---\nstatus: active\nslug: x\ntitle: x\npriority: urgent\n---\n"
	if err := os.WriteFile(filepath.Join(dir, "x.md"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	err := runTaskMigratePriority([]string{"--dir", dir})
	if err == nil {
		t.Fatal("expected error for invalid priority, got nil")
	}
	if !strings.Contains(err.Error(), "invalid") {
		t.Errorf("error %q should mention invalid", err)
	}
}
