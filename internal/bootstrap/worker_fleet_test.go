package bootstrap

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDetectWorkerFleetReadyRoundTrips(t *testing.T) {
	root := t.TempDir()
	notes, err := detectWorkerFleetReady(root)
	if err != nil {
		t.Fatalf("detect: %v", err)
	}
	if !strings.Contains(notes, "round-tripped") {
		t.Errorf("notes=%q; want round-tripped mention", notes)
	}
	// the smoke task file must be cleaned up.
	matches, _ := filepath.Glob(filepath.Join(root, "tasks", "spore-bootstrap-smoke*.md"))
	if len(matches) != 0 {
		t.Errorf("smoke task left behind: %v", matches)
	}
	// tasks/ dir is created and stays.
	if _, err := os.Stat(filepath.Join(root, "tasks")); err != nil {
		t.Errorf("tasks/ should be created: %v", err)
	}
}

func TestDetectWorkerFleetReadyRejectsEmptyRoot(t *testing.T) {
	_, err := detectWorkerFleetReady("")
	if err == nil {
		t.Fatal("err=nil; want rejection of empty root")
	}
}
