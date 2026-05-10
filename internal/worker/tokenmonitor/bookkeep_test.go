package tokenmonitor

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func fixedTime() func() time.Time {
	t, _ := time.Parse(time.RFC3339, "2026-05-10T12:00:00Z")
	return func() time.Time { return t }
}

func TestBookkeep_FirstFireBumpsCount(t *testing.T) {
	dir := t.TempDir()
	bk := BookkeepingConfig{StateDir: dir, Now: fixedTime()}
	res := CheckResult{ShouldFire: true, Slug: "feat", Tier: "max", Ctx: 180000}

	out := Bookkeep(bk, "sess-1", res)
	if !out.FirstFire {
		t.Fatal("expected FirstFire=true")
	}
	if out.Count != 1 {
		t.Fatalf("count = %d, want 1", out.Count)
	}
	body, _ := os.ReadFile(filepath.Join(dir, "worker-wrap-count", "feat"))
	if strings.TrimSpace(string(body)) != "1" {
		t.Errorf("count file = %q", body)
	}
	vol, _ := os.ReadFile(filepath.Join(dir, "worker-voluntary-events.jsonl"))
	if !strings.Contains(string(vol), `"slug":"feat"`) {
		t.Errorf("voluntary missing slug: %s", vol)
	}
	if !strings.Contains(string(vol), `"tier":"max"`) {
		t.Errorf("voluntary missing tier: %s", vol)
	}
	ev, _ := os.ReadFile(filepath.Join(dir, "events.jsonl"))
	if !strings.Contains(string(ev), `"event":"worker-token-wrap"`) {
		t.Errorf("events missing wrap event: %s", ev)
	}
}

func TestBookkeep_DedupesWithinSession(t *testing.T) {
	dir := t.TempDir()
	bk := BookkeepingConfig{StateDir: dir, Now: fixedTime()}
	res := CheckResult{ShouldFire: true, Slug: "feat", Tier: "max", Ctx: 180000}

	out1 := Bookkeep(bk, "sess-1", res)
	out2 := Bookkeep(bk, "sess-1", res)

	if !out1.FirstFire || out2.FirstFire {
		t.Fatalf("FirstFire flags = %v / %v", out1.FirstFire, out2.FirstFire)
	}
	if out1.Count != 1 || out2.Count != 1 {
		t.Fatalf("counts = %d / %d, want 1 / 1", out1.Count, out2.Count)
	}
	vol, _ := os.ReadFile(filepath.Join(dir, "worker-voluntary-events.jsonl"))
	if c := strings.Count(string(vol), `"slug":"feat"`); c != 1 {
		t.Errorf("voluntary entries = %d, want 1 (dedupe)", c)
	}
	ev, _ := os.ReadFile(filepath.Join(dir, "events.jsonl"))
	if c := strings.Count(string(ev), `"event":"worker-token-wrap"`); c != 2 {
		t.Errorf("events entries = %d, want 2 (events log unconditionally)", c)
	}
}

func TestBookkeep_CountAcrossSessions(t *testing.T) {
	dir := t.TempDir()
	bk := BookkeepingConfig{StateDir: dir, Now: fixedTime()}
	res := CheckResult{ShouldFire: true, Slug: "feat", Tier: "pro", Ctx: 130000}

	Bookkeep(bk, "sess-1", res)
	out := Bookkeep(bk, "sess-2", res)

	if !out.FirstFire {
		t.Fatal("FirstFire should be true on new session")
	}
	if out.Count != 2 {
		t.Fatalf("count after second session = %d, want 2", out.Count)
	}
}

func TestBookkeep_NoFire_NoOp(t *testing.T) {
	dir := t.TempDir()
	bk := BookkeepingConfig{StateDir: dir, Now: fixedTime()}
	res := CheckResult{ShouldFire: false, Slug: "feat"}
	out := Bookkeep(bk, "s", res)
	if out.Count != 0 {
		t.Errorf("count = %d, want 0 on no-fire", out.Count)
	}
	if _, err := os.Stat(filepath.Join(dir, "worker-wrap-count", "feat")); err == nil {
		t.Errorf("count file should not be created on no-fire")
	}
}

func TestBookkeep_NoSlug_NoOp(t *testing.T) {
	dir := t.TempDir()
	bk := BookkeepingConfig{StateDir: dir, Now: fixedTime()}
	res := CheckResult{ShouldFire: true, Slug: ""}
	out := Bookkeep(bk, "s", res)
	if out.Count != 0 {
		t.Errorf("count = %d", out.Count)
	}
}

func TestAnnotateMessage(t *testing.T) {
	got := AnnotateMessage("BODY\n", BookkeepingResult{Count: 3}, "feat")
	want := "Voluntary wrap #3 for slug=feat (cumulative across resume cycles).\nBODY\n"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestAnnotateMessage_NoCount(t *testing.T) {
	got := AnnotateMessage("BODY\n", BookkeepingResult{Count: 0}, "feat")
	if got != "BODY\n" {
		t.Errorf("got %q, want pass-through", got)
	}
}

func TestBookkeep_CountFileWithGarbage(t *testing.T) {
	dir := t.TempDir()
	countDir := filepath.Join(dir, "worker-wrap-count")
	os.MkdirAll(countDir, 0o700)
	os.WriteFile(filepath.Join(countDir, "feat"), []byte("  9\nstuff"), 0o600)

	bk := BookkeepingConfig{StateDir: dir, Now: fixedTime()}
	res := CheckResult{ShouldFire: true, Slug: "feat", Tier: "max", Ctx: 180000}
	out := Bookkeep(bk, "sess-1", res)
	if out.Count != 10 {
		t.Errorf("count = %d, want 10 (parsed 9 + bumped)", out.Count)
	}
}
