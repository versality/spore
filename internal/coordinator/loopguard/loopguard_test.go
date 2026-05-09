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

// TestCheckFPBugAVoluntaryWrapsDoNotTrip pins the regression for state.md
// "Bug A": clean poke-driven respawns (Stop hook ran, agent killed the
// session voluntarily) all share the respawn ledger with crash boots.
// Without a kind distinction, N clean wraps in <Window trip the breaker
// even though nothing crashed. The fixture mirrors the bash acceptance
// criterion (4 voluntary wraps in 60s -> exit 0).
func TestCheckFPBugAVoluntaryWrapsDoNotTrip(t *testing.T) {
	dir := t.TempDir()
	cfg := Config{StateDir: dir, MaxRespawns: 3, Window: time.Minute, Cooldown: time.Minute}

	now := time.Now().UTC()
	for i := 0; i < 4; i++ {
		err := Record(cfg, RespawnEvent{
			Timestamp: now.Add(-time.Duration(i) * time.Second),
			SessionID: "wrap",
			Kind:      KindVoluntary,
		})
		if err != nil {
			t.Fatal(err)
		}
	}

	s, err := Check(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if s.Tripped {
		t.Fatalf("voluntary wraps must not trip the breaker, got Tripped=true (recent=%d)", s.RecentCount)
	}
	if s.RecentCount != 0 {
		t.Errorf("RecentCount = %d, want 0 (voluntary events excluded)", s.RecentCount)
	}
	if _, err := os.Stat(tripMarkerPath(dir)); !os.IsNotExist(err) {
		t.Error("trip marker must not be written for voluntary-only wraps")
	}
}

// TestCheckMixedCountsOnlyCrashes covers the second bash acceptance:
// 2 voluntary + 2 crash with N=3 -> not tripped (only 2 crashes count).
func TestCheckMixedCountsOnlyCrashes(t *testing.T) {
	dir := t.TempDir()
	cfg := Config{StateDir: dir, MaxRespawns: 3, Window: time.Minute, Cooldown: time.Minute}

	now := time.Now().UTC()
	Record(cfg, RespawnEvent{Timestamp: now.Add(-1 * time.Second), Kind: KindVoluntary})
	Record(cfg, RespawnEvent{Timestamp: now.Add(-2 * time.Second), Kind: KindVoluntary})
	Record(cfg, RespawnEvent{Timestamp: now.Add(-3 * time.Second)})
	Record(cfg, RespawnEvent{Timestamp: now.Add(-4 * time.Second)})

	s, err := Check(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if s.Tripped {
		t.Fatal("expected not tripped: 2 crashes < threshold 3")
	}
	if s.RecentCount != 2 {
		t.Errorf("RecentCount = %d, want 2 (crashes only)", s.RecentCount)
	}
}

// TestCheckCrashesStillTripWithVoluntaryNoise pins that voluntary rows
// neither shadow nor cancel real crashes once the threshold is crossed.
func TestCheckCrashesStillTripWithVoluntaryNoise(t *testing.T) {
	dir := t.TempDir()
	cfg := Config{StateDir: dir, MaxRespawns: 3, Window: time.Minute, Cooldown: time.Minute}

	now := time.Now().UTC()
	for i := 0; i < 5; i++ {
		Record(cfg, RespawnEvent{Timestamp: now.Add(-time.Duration(i) * time.Second), Kind: KindVoluntary})
	}
	for i := 0; i < 3; i++ {
		Record(cfg, RespawnEvent{Timestamp: now.Add(-time.Duration(i) * time.Second)})
	}

	s, err := Check(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if !s.Tripped {
		t.Fatal("expected tripped: 3 crash respawns >= threshold 3 regardless of voluntary noise")
	}
	if s.RecentCount != 3 {
		t.Errorf("RecentCount = %d, want 3 (crashes only)", s.RecentCount)
	}
}

func TestLedgerPath(t *testing.T) {
	got := ledgerPath("/tmp/state")
	want := filepath.Join("/tmp/state", ledgerName)
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}
