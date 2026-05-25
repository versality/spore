package linear

import (
	"context"
	"net/http/httptest"
	"strings"
	"testing"
)

// TestSyncFiltersByClaimLabel covers the multi-seat case: a host with
// claim_label set must only adopt issues bearing that label, and the
// outgoing GraphQL must carry the label filter clause.
func TestSyncFiltersByClaimLabel(t *testing.T) {
	stub := newStub(t)
	stub.addReady("u-a", "MAR-101", "Mine", "").Labels = []string{"claimed-by-A"}
	stub.addReady("u-b", "MAR-102", "Theirs", "").Labels = []string{"claimed-by-B"}
	stub.addReady("u-n", "MAR-103", "Unclaimed", "")

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
		ClaimLabel:      "claimed-by-A",
	})
	if err != nil {
		t.Fatalf("NewFromConfig: %v", err)
	}

	created, _, err := src.Sync(context.Background(), t.TempDir())
	if err != nil {
		t.Fatalf("Sync: %v", err)
	}
	if created != 1 {
		t.Errorf("created = %d, want 1 (only the claimed-by-A issue)", created)
	}
	if stub.findByIdentifier("MAR-101").StateID != stub.states["In Progress"] {
		t.Errorf("claimed-by-A issue should have been adopted")
	}
	if stub.findByIdentifier("MAR-102").StateID != stub.states["Ready"] {
		t.Errorf("claimed-by-B issue should have been skipped")
	}
	if stub.findByIdentifier("MAR-103").StateID != stub.states["Ready"] {
		t.Errorf("unlabeled issue should have been skipped")
	}
	if !strings.Contains(stub.lastIssuesQuery, "labels:") || !strings.Contains(stub.lastIssuesQuery, "$label") {
		t.Errorf("issues query missing label filter clause: %s", stub.lastIssuesQuery)
	}
}

// TestSyncWithoutClaimLabelIncludesAll is the back-compat side: empty
// ClaimLabel sends the original query and adopts every Ready issue
// regardless of label state.
func TestSyncWithoutClaimLabelIncludesAll(t *testing.T) {
	stub := newStub(t)
	stub.addReady("u-a", "MAR-201", "First", "").Labels = []string{"claimed-by-A"}
	stub.addReady("u-b", "MAR-202", "Second", "").Labels = []string{"claimed-by-B"}
	stub.addReady("u-n", "MAR-203", "Third", "")

	srv := httptest.NewServer(stub.handler())
	defer srv.Close()

	src := newSource(t, srv.URL)
	created, _, err := src.Sync(context.Background(), t.TempDir())
	if err != nil {
		t.Fatalf("Sync: %v", err)
	}
	if created != 3 {
		t.Errorf("created = %d, want 3", created)
	}
	if strings.Contains(stub.lastIssuesQuery, "labels:") || strings.Contains(stub.lastIssuesQuery, "$label") {
		t.Errorf("issues query should not carry label filter when ClaimLabel unset: %s", stub.lastIssuesQuery)
	}
}
