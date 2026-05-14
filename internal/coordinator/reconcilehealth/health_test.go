package reconcilehealth

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func mustNow(t *testing.T, s string) time.Time {
	t.Helper()
	parsed, err := time.Parse(time.RFC3339, s)
	if err != nil {
		t.Fatalf("parse %q: %v", s, err)
	}
	return parsed
}

func TestVerdictMissingIsInformational(t *testing.T) {
	findings, rc := Verdict(nil, time.Now(), DefaultStaleAfter)
	if rc != 0 {
		t.Fatalf("rc=%d, want 0", rc)
	}
	if len(findings) != 1 || !strings.Contains(findings[0], "unwritten") {
		t.Fatalf("findings=%v, want one 'unwritten' line", findings)
	}
}

func TestVerdictFleetDisabledSuppresses(t *testing.T) {
	h := &Health{
		TS:            "2026-05-14T12:00:00Z",
		FleetDisabled: true,
		Projects: map[string]ProjectHealth{
			"spore": {Status: "dirty-main", DirtyFiles: []string{"a", "b"}},
		},
	}
	findings, rc := Verdict(h, mustNow(t, "2026-05-14T12:00:30Z"), DefaultStaleAfter)
	if rc != 0 {
		t.Fatalf("rc=%d, want 0 under fleet-disabled", rc)
	}
	if len(findings) != 1 || !strings.Contains(findings[0], "paused") {
		t.Fatalf("findings=%v, want single paused line", findings)
	}
}

func TestVerdictDirtyMain(t *testing.T) {
	h := &Health{
		TS: "2026-05-14T12:00:00Z",
		Projects: map[string]ProjectHealth{
			"spore": {
				Status:       "dirty-main",
				DirtyFiles:   []string{" M cmd/spore/main.go", " D internal/x.go"},
				SkippedSlugs: []string{"slug-a", "slug-b"},
			},
		},
	}
	findings, rc := Verdict(h, mustNow(t, "2026-05-14T12:00:30Z"), DefaultStaleAfter)
	if rc != 2 {
		t.Fatalf("rc=%d, want 2", rc)
	}
	if len(findings) != 1 {
		t.Fatalf("findings=%v, want one line", findings)
	}
	line := findings[0]
	for _, want := range []string{"dirty-main", "spore", "2 files", "2 slug"} {
		if !strings.Contains(line, want) {
			t.Errorf("missing %q in %q", want, line)
		}
	}
}

func TestVerdictMisProjected(t *testing.T) {
	h := &Health{
		TS: "2026-05-14T12:00:00Z",
		MisProjected: []MisProjection{
			{Slug: "spore-foo", ExpectedProject: "spore", FoundProject: "nix-config"},
		},
	}
	findings, rc := Verdict(h, mustNow(t, "2026-05-14T12:00:30Z"), DefaultStaleAfter)
	if rc != 2 {
		t.Fatalf("rc=%d, want 2", rc)
	}
	if len(findings) != 1 || !strings.Contains(findings[0], "mis-projected spore-foo") {
		t.Fatalf("findings=%v", findings)
	}
}

func TestVerdictReplenishSkipped(t *testing.T) {
	h := &Health{
		TS:            "2026-05-14T12:00:00Z",
		LastReplenish: &ReplenishSummary{Floor: 5, Was: 2, SkippedCount: 3},
	}
	findings, rc := Verdict(h, mustNow(t, "2026-05-14T12:00:30Z"), DefaultStaleAfter)
	if rc != 2 {
		t.Fatalf("rc=%d, want 2", rc)
	}
	if len(findings) != 1 || !strings.Contains(findings[0], "skipped=3") {
		t.Fatalf("findings=%v", findings)
	}
}

func TestVerdictStaleFires(t *testing.T) {
	h := &Health{
		TS:       "2026-05-14T12:00:00Z",
		Projects: map[string]ProjectHealth{"spore": {Status: "ok"}},
	}
	findings, rc := Verdict(h, mustNow(t, "2026-05-14T12:10:00Z"), DefaultStaleAfter)
	if rc != 2 {
		t.Fatalf("rc=%d, want 2", rc)
	}
	if len(findings) != 1 || !strings.Contains(findings[0], "stale") {
		t.Fatalf("findings=%v, want stale line", findings)
	}
}

func TestVerdictStaleAndBrokenBothFire(t *testing.T) {
	h := &Health{
		TS: "2026-05-14T12:00:00Z",
		Projects: map[string]ProjectHealth{
			"spore": {Status: "dirty-main", DirtyFiles: []string{"a"}},
		},
	}
	findings, rc := Verdict(h, mustNow(t, "2026-05-14T12:10:00Z"), DefaultStaleAfter)
	if rc != 2 {
		t.Fatalf("rc=%d, want 2", rc)
	}
	if len(findings) != 2 {
		t.Fatalf("findings=%v, want two lines (stale + dirty)", findings)
	}
	if !strings.Contains(findings[0], "stale") {
		t.Errorf("first finding should be stale: %q", findings[0])
	}
	if !strings.Contains(findings[1], "dirty-main") {
		t.Errorf("second finding should be dirty-main: %q", findings[1])
	}
}

func TestVerdictAllOk(t *testing.T) {
	h := &Health{
		TS: "2026-05-14T12:00:00Z",
		Projects: map[string]ProjectHealth{
			"spore":      {Status: "ok"},
			"nix-config": {Status: "ok"},
		},
	}
	findings, rc := Verdict(h, mustNow(t, "2026-05-14T12:00:30Z"), DefaultStaleAfter)
	if rc != 0 {
		t.Fatalf("rc=%d, want 0", rc)
	}
	if len(findings) != 0 {
		t.Fatalf("findings=%v, want empty", findings)
	}
}

func TestVerdictProjectOrderingDeterministic(t *testing.T) {
	h := &Health{
		TS: "2026-05-14T12:00:00Z",
		Projects: map[string]ProjectHealth{
			"zeta":  {Status: "dirty-main", DirtyFiles: []string{"a"}},
			"alpha": {Status: "dirty-main", DirtyFiles: []string{"b"}},
		},
	}
	findings, rc := Verdict(h, mustNow(t, "2026-05-14T12:00:30Z"), DefaultStaleAfter)
	if rc != 2 {
		t.Fatalf("rc=%d", rc)
	}
	if len(findings) != 2 {
		t.Fatalf("findings=%v", findings)
	}
	if !strings.Contains(findings[0], "alpha") || !strings.Contains(findings[1], "zeta") {
		t.Fatalf("expected alpha then zeta; got %v", findings)
	}
}

func TestReadMissingFile(t *testing.T) {
	h, err := Read(filepath.Join(t.TempDir(), "no-such.json"))
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if h != nil {
		t.Fatalf("h=%v, want nil for missing file", h)
	}
}

func TestReadRoundtrip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "reconcile-health.json")
	want := Health{
		TS:      "2026-05-14T12:00:00Z",
		Version: 1,
		Projects: map[string]ProjectHealth{
			"spore": {Status: "dirty-main", DirtyFiles: []string{"a"}, SkippedSlugs: []string{"s"}},
		},
		MisProjected: []MisProjection{
			{Slug: "x", ExpectedProject: "spore", FoundProject: "nix-config"},
		},
		LastReplenish: &ReplenishSummary{Floor: 5, Was: 2, SkippedCount: 1},
	}
	b, err := json.Marshal(want)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, b, 0o644); err != nil {
		t.Fatal(err)
	}

	got, err := Read(path)
	if err != nil {
		t.Fatal(err)
	}
	if got.TS != want.TS || got.Version != 1 {
		t.Fatalf("got=%+v", got)
	}
	if got.Projects["spore"].Status != "dirty-main" {
		t.Fatalf("projects=%+v", got.Projects)
	}
	if len(got.MisProjected) != 1 || got.MisProjected[0].Slug != "x" {
		t.Fatalf("mis=%+v", got.MisProjected)
	}
	if got.LastReplenish == nil || got.LastReplenish.SkippedCount != 1 {
		t.Fatalf("replenish=%+v", got.LastReplenish)
	}
}

func TestReadParseError(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "reconcile-health.json")
	if err := os.WriteFile(path, []byte("not json"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Read(path); err == nil {
		t.Fatal("expected parse error, got nil")
	}
}

func TestVerdictEmptyTSIsStale(t *testing.T) {
	h := &Health{Projects: map[string]ProjectHealth{"spore": {Status: "ok"}}}
	findings, rc := Verdict(h, time.Now(), DefaultStaleAfter)
	if rc != 2 {
		t.Fatalf("rc=%d, want 2 for empty ts", rc)
	}
	if len(findings) != 1 || !strings.Contains(findings[0], "stale") {
		t.Fatalf("findings=%v", findings)
	}
}
