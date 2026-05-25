package linear

import (
	"context"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestSyncSkipsTicketBlockedByOpenUpstream covers the bug shape: a
// Ready ticket whose blocked_by upstream sits in a non-terminal state
// must not get projected, so the fleet never spawns a worker that
// would immediately self-block.
func TestSyncSkipsTicketBlockedByOpenUpstream(t *testing.T) {
	stub := newStub(t)
	blocker := stub.addReady("u-blocker", "MAR-100", "Upstream still cooking", "")
	blocker.StateID = stub.states["In Progress"]
	dependent := stub.addReady("u-dependent", "MAR-101", "Cannot start yet", "")
	dependent.Relations = []stubRelation{
		{Type: "blocked_by", RelatedID: blocker.ID, RelatedStateType: "started"},
	}

	srv := httptest.NewServer(stub.handler())
	defer srv.Close()

	root := t.TempDir()
	src := newSource(t, srv.URL)

	created, _, err := src.Sync(context.Background(), root)
	if err != nil {
		t.Fatalf("Sync: %v", err)
	}
	if created != 0 {
		t.Errorf("created = %d, want 0 (dependent must be skipped)", created)
	}
	if dependent.StateID != stub.states["Ready"] {
		t.Errorf("dependent state = %q, want Ready (no transition)", dependent.StateID)
	}

	tasksDir := filepath.Join(root, "tasks")
	files, _ := os.ReadDir(tasksDir)
	for _, f := range files {
		if strings.Contains(f.Name(), "cannot-start") {
			t.Errorf("dependent task file leaked to disk: %s", f.Name())
		}
	}
}

// TestSyncProjectsTicketWhenBlockerDone is the positive case: once the
// upstream lands in a terminal state, the next sync pass picks the
// dependent up like any other Ready ticket.
func TestSyncProjectsTicketWhenBlockerDone(t *testing.T) {
	stub := newStub(t)
	blocker := stub.addReady("u-blocker", "MAR-110", "Upstream shipped", "")
	blocker.StateID = stub.states["Done"]
	dependent := stub.addReady("u-dependent", "MAR-111", "Unblocked dependent", "")
	dependent.Relations = []stubRelation{
		{Type: "blocked_by", RelatedID: blocker.ID, RelatedStateType: "completed"},
	}

	srv := httptest.NewServer(stub.handler())
	defer srv.Close()

	root := t.TempDir()
	src := newSource(t, srv.URL)

	created, _, err := src.Sync(context.Background(), root)
	if err != nil {
		t.Fatalf("Sync: %v", err)
	}
	if created != 1 {
		t.Errorf("created = %d, want 1", created)
	}
	if dependent.StateID != stub.states["In Progress"] {
		t.Errorf("dependent state = %q, want In Progress", dependent.StateID)
	}
}

// TestSyncProjectsTicketWithoutRelations guards the no-relations path:
// a ticket with an empty relations connection must continue to project
// just like before the filter landed.
func TestSyncProjectsTicketWithoutRelations(t *testing.T) {
	stub := newStub(t)
	lone := stub.addReady("u-lone", "MAR-120", "Standalone ticket", "")

	srv := httptest.NewServer(stub.handler())
	defer srv.Close()

	root := t.TempDir()
	src := newSource(t, srv.URL)

	created, _, err := src.Sync(context.Background(), root)
	if err != nil {
		t.Fatalf("Sync: %v", err)
	}
	if created != 1 {
		t.Errorf("created = %d, want 1", created)
	}
	if lone.StateID != stub.states["In Progress"] {
		t.Errorf("lone state = %q, want In Progress", lone.StateID)
	}
}

// TestSyncIgnoresRelatedAndDuplicateRelations confirms the filter only
// honors blocked_by; related and duplicate edges, even pointing at
// open upstreams, do not gate projection.
func TestSyncIgnoresRelatedAndDuplicateRelations(t *testing.T) {
	stub := newStub(t)
	other := stub.addReady("u-other", "MAR-130", "Tangential", "")
	other.StateID = stub.states["In Progress"]
	candidate := stub.addReady("u-candidate", "MAR-131", "Has loose ties", "")
	candidate.Relations = []stubRelation{
		{Type: "related", RelatedID: other.ID, RelatedStateType: "started"},
		{Type: "duplicate", RelatedID: other.ID, RelatedStateType: "started"},
	}

	srv := httptest.NewServer(stub.handler())
	defer srv.Close()

	root := t.TempDir()
	src := newSource(t, srv.URL)

	created, _, err := src.Sync(context.Background(), root)
	if err != nil {
		t.Fatalf("Sync: %v", err)
	}
	if created != 1 {
		t.Errorf("created = %d, want 1 (only the blocked_by edge gates projection)", created)
	}
}
