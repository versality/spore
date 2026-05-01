package task

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestWaybar(t *testing.T) {
	dir := t.TempDir()
	write := func(slug, status string) {
		content := "---\nstatus: " + status + "\nslug: " + slug + "\n---\n"
		if err := os.WriteFile(filepath.Join(dir, slug+".md"), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("a", "draft")
	write("b", "active")
	write("c", "active")
	write("d", "blocked")
	write("e", "done")

	out, err := Waybar(dir)
	if err != nil {
		t.Fatal(err)
	}
	var chip WaybarChip
	if err := json.Unmarshal(out, &chip); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	if chip.Text != "1/2/0/1" {
		t.Errorf("text = %q, want 1/2/0/1", chip.Text)
	}
	if chip.Class != "blocked" {
		t.Errorf("class = %q, want blocked", chip.Class)
	}
}
