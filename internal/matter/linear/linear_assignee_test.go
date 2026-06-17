package linear

import (
	"context"
	"net/http/httptest"
	"strings"
	"testing"
)

// TestSyncFiltersByAssignee covers the assignee-scoped pickup: a host
// with AssigneeEmail set must only adopt issues assigned to that user,
// and the outgoing GraphQL must carry the assignee filter clause.
func TestSyncFiltersByAssignee(t *testing.T) {
	stub := newStub(t)
	stub.addReady("u-a", "MAR-101", "Mine", "").Assignee = "artyom@marketer.tech"
	stub.addReady("u-b", "MAR-102", "Theirs", "").Assignee = "someone@else.tech"
	stub.addReady("u-n", "MAR-103", "Unassigned", "")

	srv := httptest.NewServer(stub.handler())
	defer srv.Close()

	t.Setenv("LINEAR_API_KEY", "lin_test")
	src, err := NewFromConfig(Config{
		Team:            "MAR",
		ReadyState:      "Ready",
		InProgressState: "In Progress",
		DoneState:       "Done",
		APIKeyEnv:       "LINEAR_API_KEY",
		Endpoint:        srv.URL,
		AssigneeEmail:   "artyom@marketer.tech",
	})
	if err != nil {
		t.Fatalf("NewFromConfig: %v", err)
	}

	created, _, err := src.Sync(context.Background(), t.TempDir())
	if err != nil {
		t.Fatalf("Sync: %v", err)
	}
	if created != 1 {
		t.Errorf("created = %d, want 1 (only artyom's issue)", created)
	}
	if stub.findByIdentifier("MAR-101").StateID != stub.states["In Progress"] {
		t.Errorf("artyom's issue should have been adopted")
	}
	if stub.findByIdentifier("MAR-102").StateID != stub.states["Ready"] {
		t.Errorf("another assignee's issue should have been skipped")
	}
	if stub.findByIdentifier("MAR-103").StateID != stub.states["Ready"] {
		t.Errorf("unassigned issue should have been skipped")
	}
	if !strings.Contains(stub.lastIssuesQuery, "assignee:") || !strings.Contains(stub.lastIssuesQuery, "$assignee") {
		t.Errorf("issues query missing assignee filter clause: %s", stub.lastIssuesQuery)
	}
}

// TestSyncWithoutAssigneeIncludesAll is the back-compat side: empty
// AssigneeEmail sends no assignee clause and adopts every Ready issue.
func TestSyncWithoutAssigneeIncludesAll(t *testing.T) {
	stub := newStub(t)
	stub.addReady("u-a", "MAR-201", "First", "").Assignee = "artyom@marketer.tech"
	stub.addReady("u-b", "MAR-202", "Second", "").Assignee = "someone@else.tech"

	srv := httptest.NewServer(stub.handler())
	defer srv.Close()

	src := newSource(t, srv.URL)
	created, _, err := src.Sync(context.Background(), t.TempDir())
	if err != nil {
		t.Fatalf("Sync: %v", err)
	}
	if created != 2 {
		t.Errorf("created = %d, want 2", created)
	}
	if strings.Contains(stub.lastIssuesQuery, "assignee:") || strings.Contains(stub.lastIssuesQuery, "$assignee") {
		t.Errorf("issues query should not carry assignee filter when unset: %s", stub.lastIssuesQuery)
	}
}

// TestSyncMaxConcurrentBoundsAdoption verifies the In Progress flip
// tracks spawn capacity: with MaxConcurrent=2 only the two
// lowest-sortOrder Ready issues are adopted, and a second pass adopts
// nothing more while the first two stay active (slots full).
func TestSyncMaxConcurrentBoundsAdoption(t *testing.T) {
	stub := newStub(t)
	a := stub.addReady("u-a", "MAR-301", "First", "")
	a.SortOrder = 1
	b := stub.addReady("u-b", "MAR-302", "Second", "")
	b.SortOrder = 2
	c := stub.addReady("u-c", "MAR-303", "Third", "")
	c.SortOrder = 3
	d := stub.addReady("u-d", "MAR-304", "Fourth", "")
	d.SortOrder = 4

	srv := httptest.NewServer(stub.handler())
	defer srv.Close()

	t.Setenv("LINEAR_API_KEY", "lin_test")
	cfg := Config{
		Team:            "MAR",
		ReadyState:      "Ready",
		InProgressState: "In Progress",
		DoneState:       "Done",
		APIKeyEnv:       "LINEAR_API_KEY",
		Endpoint:        srv.URL,
		MaxConcurrent:   2,
	}
	src, err := NewFromConfig(cfg)
	if err != nil {
		t.Fatalf("NewFromConfig: %v", err)
	}

	root := t.TempDir()
	created, _, err := src.Sync(context.Background(), root)
	if err != nil {
		t.Fatalf("Sync: %v", err)
	}
	if created != 2 {
		t.Fatalf("created = %d, want 2 (capacity-bounded)", created)
	}
	if stub.findByIdentifier("MAR-301").StateID != stub.states["In Progress"] {
		t.Errorf("MAR-301 (lowest sortOrder) should have been adopted")
	}
	if stub.findByIdentifier("MAR-302").StateID != stub.states["In Progress"] {
		t.Errorf("MAR-302 should have been adopted")
	}
	if stub.findByIdentifier("MAR-303").StateID != stub.states["Ready"] {
		t.Errorf("MAR-303 should have stayed Ready (over capacity)")
	}
	if stub.findByIdentifier("MAR-304").StateID != stub.states["Ready"] {
		t.Errorf("MAR-304 should have stayed Ready (over capacity)")
	}

	// Second pass with both slots still active: nothing new adopted.
	src2, err := NewFromConfig(cfg)
	if err != nil {
		t.Fatalf("NewFromConfig: %v", err)
	}
	created2, _, err := src2.Sync(context.Background(), root)
	if err != nil {
		t.Fatalf("Sync (pass 2): %v", err)
	}
	if created2 != 0 {
		t.Errorf("created (pass 2) = %d, want 0 (slots full)", created2)
	}
	if stub.findByIdentifier("MAR-303").StateID != stub.states["Ready"] {
		t.Errorf("MAR-303 should still be Ready after pass 2")
	}
}
