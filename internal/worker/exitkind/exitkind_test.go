package exitkind

import (
	"os"
	"path/filepath"
	"testing"
)

func TestClassify(t *testing.T) {
	dir := t.TempDir()
	marker := filepath.Join(dir, "clean-exit")
	if err := os.WriteFile(marker, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	missing := filepath.Join(dir, "missing")

	cases := []struct {
		name   string
		rc     int
		marker string
		want   string
	}{
		// Marker present always wins, even on non-zero rc: the rower
		// wrote it before tearing down its own pty, so the rc-shape is
		// the lifecycle SIGHUP, not a crash.
		{"lifecycle-clean", 129, marker, "lifecycle"},
		{"lifecycle-rc0", 0, marker, "lifecycle"},
		{"lifecycle-nonzero", 137, marker, "lifecycle"},

		{"early-exit-rc0", 0, "", "early-exit"},
		{"early-exit-rc0-missing-marker", 0, missing, "early-exit"},

		{"sighup-external", 129, "", "sighup-external"},
		{"sighup-external-missing-marker", 129, missing, "sighup-external"},

		{"crash-rc1", 1, "", "crash-rc1"},
		{"crash-rc137", 137, "", "crash-rc137"},
		{"crash-rc2-missing-marker", 2, missing, "crash-rc2"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := Classify(tc.rc, tc.marker)
			if got != tc.want {
				t.Errorf("Classify(%d, %q) = %q, want %q", tc.rc, tc.marker, got, tc.want)
			}
		})
	}
}
