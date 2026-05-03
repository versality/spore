package task

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestTellWritesJSON(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	state := t.TempDir()
	t.Setenv("XDG_STATE_HOME", state)

	if err := Tell("foo", "hello"); err != nil {
		t.Fatalf("Tell: %v", err)
	}

	inbox := filepath.Join(state, "spore", filepath.Base(dir), "foo", "inbox")
	entries, err := os.ReadDir(inbox)
	if err != nil {
		t.Fatalf("read inbox: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("inbox entries = %d, want 1", len(entries))
	}
	name := entries[0].Name()
	if !strings.HasSuffix(name, ".json") {
		t.Errorf("entry %q lacks .json suffix", name)
	}

	raw, err := os.ReadFile(filepath.Join(inbox, name))
	if err != nil {
		t.Fatal(err)
	}
	var got struct {
		Slug string `json:"slug"`
		TS   string `json:"ts"`
		Msg  string `json:"msg"`
	}
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got.Slug != "foo" {
		t.Errorf("slug = %q, want foo", got.Slug)
	}
	if got.Msg != "hello" {
		t.Errorf("msg = %q, want hello", got.Msg)
	}
	if got.TS == "" {
		t.Error("ts is empty")
	}
}

func TestWriteUniqueInboxFilePreservesCollisions(t *testing.T) {
	dir := t.TempDir()
	body1 := []byte(`{"msg":"first"}`)
	body2 := []byte(`{"msg":"second"}`)

	if err := writeUniqueInboxFile(dir, 1234, body1); err != nil {
		t.Fatalf("first writeUniqueInboxFile: %v", err)
	}
	if err := writeUniqueInboxFile(dir, 1234, body2); err != nil {
		t.Fatalf("second writeUniqueInboxFile: %v", err)
	}

	first, err := os.ReadFile(filepath.Join(dir, "1234.json"))
	if err != nil {
		t.Fatal(err)
	}
	second, err := os.ReadFile(filepath.Join(dir, "1234-1.json"))
	if err != nil {
		t.Fatal(err)
	}
	if string(first) != string(body1) {
		t.Errorf("first body = %s, want %s", first, body1)
	}
	if string(second) != string(body2) {
		t.Errorf("second body = %s, want %s", second, body2)
	}
}
