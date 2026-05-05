package linear

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/versality/spore/internal/matter"
	"github.com/versality/spore/internal/task/frontmatter"
)

// stubLinear emulates the Linear GraphQL endpoint just well enough to
// drive the Sync paths. State and issues are mutable so a test can
// verify a transition mutation actually moved the issue.
type stubLinear struct {
	t      *testing.T
	team   string
	states map[string]string // name -> id
	issues map[string]*stubIssue
	calls  int
}

type stubIssue struct {
	ID          string
	Identifier  string
	Title       string
	Description string
	URL         string
	StateID     string
}

func newStub(t *testing.T) *stubLinear {
	return &stubLinear{
		t:    t,
		team: "MAR",
		states: map[string]string{
			"Backlog":     "state-backlog",
			"Ready":       "state-ready",
			"In Progress": "state-doing",
			"Done":        "state-done",
		},
		issues: map[string]*stubIssue{},
	}
}

func (s *stubLinear) addReady(id, identifier, title, desc string) *stubIssue {
	iss := &stubIssue{
		ID: id, Identifier: identifier, Title: title,
		Description: desc, URL: "https://linear.app/team/issue/" + identifier,
		StateID: s.states["Ready"],
	}
	s.issues[id] = iss
	return iss
}

func (s *stubLinear) handler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		s.calls++
		if got := r.Header.Get("Authorization"); got != "lin_test" {
			s.t.Errorf("Authorization = %q, want lin_test", got)
			http.Error(w, "bad auth", http.StatusUnauthorized)
			return
		}
		if got := r.Header.Get("Content-Type"); got != "application/json" {
			s.t.Errorf("Content-Type = %q", got)
		}

		raw, err := io.ReadAll(r.Body)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		var body struct {
			Query     string         `json:"query"`
			Variables map[string]any `json:"variables"`
		}
		if err := json.Unmarshal(raw, &body); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}

		switch {
		case strings.Contains(body.Query, "workflowStates"):
			s.respondStates(w)
		case strings.Contains(body.Query, "issueUpdate"):
			s.respondIssueUpdate(w, body.Variables)
		case strings.Contains(body.Query, "issues("):
			s.respondIssues(w, body.Variables)
		default:
			http.Error(w, "unrecognised query: "+body.Query, http.StatusBadRequest)
		}
	}
}

func (s *stubLinear) respondStates(w http.ResponseWriter) {
	type node struct {
		ID   string `json:"id"`
		Name string `json:"name"`
	}
	var nodes []node
	for name, id := range s.states {
		nodes = append(nodes, node{ID: id, Name: name})
	}
	sort.Slice(nodes, func(i, j int) bool { return nodes[i].Name < nodes[j].Name })
	resp := map[string]any{
		"data": map[string]any{
			"workflowStates": map[string]any{"nodes": nodes},
		},
	}
	_ = json.NewEncoder(w).Encode(resp)
}

func (s *stubLinear) respondIssues(w http.ResponseWriter, vars map[string]any) {
	stateID, _ := vars["stateId"].(string)
	type node struct {
		ID          string `json:"id"`
		Identifier  string `json:"identifier"`
		Title       string `json:"title"`
		Description string `json:"description"`
		URL         string `json:"url"`
	}
	var nodes []node
	for _, iss := range s.issues {
		if iss.StateID != stateID {
			continue
		}
		nodes = append(nodes, node{
			ID: iss.ID, Identifier: iss.Identifier, Title: iss.Title,
			Description: iss.Description, URL: iss.URL,
		})
	}
	sort.Slice(nodes, func(i, j int) bool { return nodes[i].Identifier < nodes[j].Identifier })
	resp := map[string]any{
		"data": map[string]any{
			"issues": map[string]any{"nodes": nodes},
		},
	}
	_ = json.NewEncoder(w).Encode(resp)
}

func (s *stubLinear) respondIssueUpdate(w http.ResponseWriter, vars map[string]any) {
	id, _ := vars["id"].(string)
	stateID, _ := vars["stateId"].(string)
	iss, ok := s.issues[id]
	if !ok {
		http.Error(w, "no such issue: "+id, http.StatusNotFound)
		return
	}
	iss.StateID = stateID
	resp := map[string]any{
		"data": map[string]any{
			"issueUpdate": map[string]any{"success": true},
		},
	}
	_ = json.NewEncoder(w).Encode(resp)
}

func TestSyncCreatesTasksAndPushesReady(t *testing.T) {
	stub := newStub(t)
	stub.addReady("issue-uuid-1", "MAR-12", "Wire up onboarding email", "Send welcome email on signup.")
	stub.addReady("issue-uuid-2", "MAR-13", "Crash on empty cart", "Repro: open cart without items.")

	srv := httptest.NewServer(stub.handler())
	defer srv.Close()

	root := t.TempDir()
	tasksDir := filepath.Join(root, "tasks")
	if err := os.MkdirAll(tasksDir, 0o755); err != nil {
		t.Fatal(err)
	}

	t.Setenv("LINEAR_API_KEY", "lin_test")
	cfg := matter.LinearConfig{
		Team:            "MAR",
		ReadyState:      "Ready",
		InProgressState: "In Progress",
		DoneState:       "Done",
		APIKeyEnv:       "LINEAR_API_KEY",
		Endpoint:        srv.URL,
	}
	src, err := New(cfg)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	stats, err := src.Sync(root, "tasks")
	if err != nil {
		t.Fatalf("Sync: %v", err)
	}
	if got, want := len(stats.Created), 2; got != want {
		t.Fatalf("Created = %d, want %d (got %v)", got, want, stats.Created)
	}
	if got, want := stats.AdoptedReady, []string{"MAR-12", "MAR-13"}; !equal(got, want) {
		t.Errorf("AdoptedReady = %v, want %v", got, want)
	}

	for id, iss := range stub.issues {
		if iss.StateID != stub.states["In Progress"] {
			t.Errorf("issue %s state = %s, want In Progress", id, iss.StateID)
		}
	}

	for _, slug := range stats.Created {
		raw, err := os.ReadFile(filepath.Join(tasksDir, slug+".md"))
		if err != nil {
			t.Fatalf("read %s: %v", slug, err)
		}
		m, body, err := frontmatter.Parse(raw)
		if err != nil {
			t.Fatalf("parse %s: %v", slug, err)
		}
		if m.Status != "active" {
			t.Errorf("%s: status = %q, want active", slug, m.Status)
		}
		if !strings.HasPrefix(m.Extra["linear"], "MAR-") {
			t.Errorf("%s: linear extra = %q", slug, m.Extra["linear"])
		}
		if !strings.Contains(string(body), "Linear: https://linear.app/") {
			t.Errorf("%s: body missing Linear URL: %s", slug, body)
		}
	}

	stats2, err := src.Sync(root, "tasks")
	if err != nil {
		t.Fatalf("Sync (idempotent pass): %v", err)
	}
	if len(stats2.Created)+len(stats2.AdoptedReady) != 0 {
		t.Errorf("expected no-op second pass, got %+v", stats2)
	}
}

func TestSyncPushesDoneTasks(t *testing.T) {
	stub := newStub(t)
	iss := stub.addReady("issue-uuid-7", "MAR-21", "Ship checkout", "")
	iss.StateID = stub.states["In Progress"]

	srv := httptest.NewServer(stub.handler())
	defer srv.Close()

	root := t.TempDir()
	tasksDir := filepath.Join(root, "tasks")
	if err := os.MkdirAll(tasksDir, 0o755); err != nil {
		t.Fatal(err)
	}
	m := frontmatter.Meta{
		Status: "done", Slug: "ship-checkout", Title: "Ship checkout",
		Extra: map[string]string{"linear": "issue-uuid-7"},
	}
	if err := os.WriteFile(filepath.Join(tasksDir, "ship-checkout.md"),
		frontmatter.Write(m, []byte("\nbody\n")), 0o644); err != nil {
		t.Fatal(err)
	}

	t.Setenv("LINEAR_API_KEY", "lin_test")
	src, err := New(matter.LinearConfig{
		Team:            "MAR",
		ReadyState:      "Ready",
		InProgressState: "In Progress",
		DoneState:       "Done",
		APIKeyEnv:       "LINEAR_API_KEY",
		Endpoint:        srv.URL,
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	stats, err := src.Sync(root, "tasks")
	if err != nil {
		t.Fatalf("Sync: %v", err)
	}
	if got, want := stats.PushedDone, []string{"ship-checkout"}; !equal(got, want) {
		t.Errorf("PushedDone = %v, want %v", got, want)
	}
	if iss.StateID != stub.states["Done"] {
		t.Errorf("issue state = %q, want Done", iss.StateID)
	}

	raw, err := os.ReadFile(filepath.Join(tasksDir, "ship-checkout.md"))
	if err != nil {
		t.Fatal(err)
	}
	m2, _, err := frontmatter.Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	if m2.Extra["linear_done"] != "yes" {
		t.Errorf("linear_done = %q, want yes", m2.Extra["linear_done"])
	}

	stats2, err := src.Sync(root, "tasks")
	if err != nil {
		t.Fatalf("Sync second pass: %v", err)
	}
	if len(stats2.PushedDone) != 0 {
		t.Errorf("second pass should not push again, got %v", stats2.PushedDone)
	}
}

func TestSyncErrorsOnUnknownState(t *testing.T) {
	stub := newStub(t)
	delete(stub.states, "Ready")

	srv := httptest.NewServer(stub.handler())
	defer srv.Close()

	t.Setenv("LINEAR_API_KEY", "lin_test")
	src, err := New(matter.LinearConfig{
		Team:            "MAR",
		ReadyState:      "Ready",
		InProgressState: "In Progress",
		DoneState:       "Done",
		APIKeyEnv:       "LINEAR_API_KEY",
		Endpoint:        srv.URL,
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	root := t.TempDir()
	if _, err := src.Sync(root, "tasks"); err == nil {
		t.Fatal("expected error for missing Ready state")
	}
}

func TestGraphQLSurfacesGraphErrors(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"errors": []map[string]any{{"message": "rate limited"}},
		})
	}))
	defer srv.Close()

	t.Setenv("LINEAR_API_KEY", "lin_test")
	src, err := New(matter.LinearConfig{
		Team:      "MAR",
		APIKeyEnv: "LINEAR_API_KEY",
		Endpoint:  srv.URL,
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	root := t.TempDir()
	_, err = src.Sync(root, "tasks")
	if err == nil || !strings.Contains(err.Error(), "rate limited") {
		t.Fatalf("expected rate-limited error, got %v", err)
	}
}

func equal(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
