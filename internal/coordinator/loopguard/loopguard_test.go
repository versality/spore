package loopguard

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestCheckNoEvents(t *testing.T) {
	dir := t.TempDir()
	cfg := Config{StateDir: dir, MaxRespawns: 3, Window: time.Minute}
	s, err := Check(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if s.Tripped {
		t.Error("expected not tripped with no events")
	}
	if s.RecentCount != 0 {
		t.Errorf("RecentCount = %d, want 0", s.RecentCount)
	}
}

func TestCheckTripsOnThreshold(t *testing.T) {
	dir := t.TempDir()
	cfg := Config{StateDir: dir, MaxRespawns: 3, Window: time.Minute, Cooldown: time.Second}

	now := time.Now().UTC()
	for i := 0; i < 3; i++ {
		Record(cfg, RespawnEvent{Timestamp: now.Add(-time.Duration(i) * time.Second)})
	}

	s, err := Check(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if !s.Tripped {
		t.Error("expected tripped after 3 respawns")
	}
	if s.RecentCount != 3 {
		t.Errorf("RecentCount = %d, want 3", s.RecentCount)
	}

	if _, err := os.Stat(tripMarkerPath(dir)); err != nil {
		t.Error("expected trip marker file to exist")
	}
}

func TestCheckCooldownExpiry(t *testing.T) {
	dir := t.TempDir()
	cfg := Config{StateDir: dir, MaxRespawns: 3, Window: time.Minute, Cooldown: time.Millisecond}

	f, _ := os.Create(tripMarkerPath(dir))
	f.Close()
	past := time.Now().Add(-time.Second)
	os.Chtimes(tripMarkerPath(dir), past, past)

	s, err := Check(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if s.Tripped {
		t.Error("expected cooldown to have expired")
	}
}

func TestRecordAndRead(t *testing.T) {
	dir := t.TempDir()
	cfg := Config{StateDir: dir}

	now := time.Now().UTC()
	for i := 0; i < 5; i++ {
		err := Record(cfg, RespawnEvent{
			Timestamp: now.Add(-time.Duration(i) * time.Second),
			SessionID: "test",
			Reason:    "context-wrap",
		})
		if err != nil {
			t.Fatal(err)
		}
	}

	events, err := readRecentEvents(dir, now.Add(-10*time.Second))
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 5 {
		t.Errorf("got %d events, want 5", len(events))
	}
}

func TestRecordOldEventsFiltered(t *testing.T) {
	dir := t.TempDir()
	cfg := Config{StateDir: dir}

	old := time.Now().UTC().Add(-time.Hour)
	recent := time.Now().UTC()
	Record(cfg, RespawnEvent{Timestamp: old})
	Record(cfg, RespawnEvent{Timestamp: recent})

	events, err := readRecentEvents(dir, time.Now().UTC().Add(-time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 1 {
		t.Errorf("got %d events, want 1 (old should be filtered)", len(events))
	}
}

func TestReset(t *testing.T) {
	dir := t.TempDir()
	marker := tripMarkerPath(dir)
	f, _ := os.Create(marker)
	f.Close()

	if err := Reset(dir); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Error("expected marker removed after reset")
	}
}

func TestLedgerPath(t *testing.T) {
	got := ledgerPath("/tmp/state")
	want := filepath.Join("/tmp/state", ledgerName)
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}
