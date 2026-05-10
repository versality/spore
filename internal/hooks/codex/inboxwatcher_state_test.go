package codex

import (
	"path/filepath"
	"testing"
	"time"
)

var refTime = time.Date(2026, 5, 10, 12, 0, 0, 0, time.UTC)

func TestPlanInbox_FirstFile_TriggersWake(t *testing.T) {
	out := PlanInbox(InboxState{},
		InboxObservation{Newest: "100.json", UnreadCount: 1, Source: "agent"},
		WakeModeRespawn, 5*time.Minute, refTime)
	if !out.Wake {
		t.Fatalf("expected wake")
	}
	if out.Event != "wake-sent" {
		t.Errorf("event = %q", out.Event)
	}
	if out.NewState.Last != "100.json" {
		t.Errorf("Last = %q", out.NewState.Last)
	}
	if out.NewState.WakePending != "100.json" {
		t.Errorf("WakePending = %q", out.NewState.WakePending)
	}
}

func TestPlanInbox_SameNewest_DedupeOnce(t *testing.T) {
	prior := InboxState{Last: "100.json"}
	out1 := PlanInbox(prior,
		InboxObservation{Newest: "100.json", UnreadCount: 1},
		WakeModeRespawn, 5*time.Minute, refTime)
	if out1.Wake {
		t.Errorf("should not wake on same file")
	}
	if out1.Event != "wake-deduped" {
		t.Errorf("event = %q, want wake-deduped", out1.Event)
	}
	out2 := PlanInbox(out1.NewState,
		InboxObservation{Newest: "100.json", UnreadCount: 1},
		WakeModeRespawn, 5*time.Minute, refTime)
	if out2.Event != "" {
		t.Errorf("second pass should be silent, got %q", out2.Event)
	}
}

func TestPlanInbox_EmptyInbox_DrainedOnce(t *testing.T) {
	prior := InboxState{Last: "100.json"}
	out1 := PlanInbox(prior,
		InboxObservation{UnreadCount: 0},
		WakeModeRespawn, 5*time.Minute, refTime)
	if out1.Event != "processed/drained" {
		t.Errorf("event = %q", out1.Event)
	}
	if out1.NewState.Drained != "100.json" {
		t.Errorf("Drained = %q", out1.NewState.Drained)
	}
	out2 := PlanInbox(out1.NewState,
		InboxObservation{UnreadCount: 0},
		WakeModeRespawn, 5*time.Minute, refTime)
	if out2.Event != "" {
		t.Errorf("second drained pass should be silent")
	}
}

func TestPlanInbox_NewerFile_ClearsDedupe(t *testing.T) {
	prior := InboxState{Last: "100.json", Dedupe: "100.json"}
	out := PlanInbox(prior,
		InboxObservation{Newest: "200.json", UnreadCount: 1},
		WakeModeRespawn, 5*time.Minute, refTime)
	if !out.Wake {
		t.Errorf("expected wake on newer file")
	}
	if out.NewState.Dedupe != "" {
		t.Errorf("Dedupe should be cleared, got %q", out.NewState.Dedupe)
	}
	if out.NewState.Last != "200.json" {
		t.Errorf("Last not advanced")
	}
}

func TestPlanInbox_RecordOnly_NoWake(t *testing.T) {
	out := PlanInbox(InboxState{},
		InboxObservation{Newest: "100.json", UnreadCount: 1},
		WakeModeRecordOnly, 5*time.Minute, refTime)
	if out.Wake {
		t.Errorf("record-only mode should not wake")
	}
	if out.Event != "recorded-only" {
		t.Errorf("event = %q", out.Event)
	}
}

func TestPlanInbox_WakePendingThrottle(t *testing.T) {
	prior := InboxState{
		Last:             "100.json",
		WakePending:      "100.json",
		WakePendingMTime: refTime.Add(-30 * time.Second),
	}
	out := PlanInbox(prior,
		InboxObservation{Newest: "200.json", UnreadCount: 1},
		WakeModeRespawn, 5*time.Minute, refTime)
	if out.Wake {
		t.Errorf("wake-pending TTL should suppress wake")
	}
	if out.Event != "wake-pending" {
		t.Errorf("event = %q", out.Event)
	}
}

func TestPlanInbox_WakePendingExpired(t *testing.T) {
	prior := InboxState{
		Last:             "100.json",
		WakePending:      "100.json",
		WakePendingMTime: refTime.Add(-10 * time.Minute),
	}
	out := PlanInbox(prior,
		InboxObservation{Newest: "200.json", UnreadCount: 1},
		WakeModeRespawn, 5*time.Minute, refTime)
	if !out.Wake {
		t.Errorf("expired pending should not block wake")
	}
}

func TestSaveLoadInboxState_RoundTrip(t *testing.T) {
	dir := t.TempDir()
	pdir := filepath.Join(dir, "proj")
	state := InboxState{Last: "100.json", Dedupe: "", Drained: "99.json"}
	if err := SaveInboxState(pdir, state); err != nil {
		t.Fatal(err)
	}
	got := LoadInboxState(pdir)
	if got.Last != "100.json" || got.Drained != "99.json" {
		t.Errorf("got %+v", got)
	}
	if got.Dedupe != "" {
		t.Errorf("Dedupe should be empty")
	}
}
