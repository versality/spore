package linear

import (
	"context"
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
	SortOrder   float64
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

// findByIdentifier returns the stubIssue matching the human
// identifier (e.g. "MAR-12"). issueUpdate is called with the
// identifier in adoptIssue->transitionIssue and OnDone, but the stub
// keys its map by ID; the lookup walks the map.
func (s *stubLinear) findByIdentifier(ident string) *stubIssue {
	for _, iss := range s.issues {
		if iss.Identifier == ident {
			return iss
		}
	}
	return nil
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
		ID          string  `json:"id"`
		Identifier  string  `json:"identifier"`
		Title       string  `json:"title"`
		Description string  `json:"description"`
		URL         string  `json:"url"`
		SortOrder   float64 `json:"sortOrder"`
	}
	var nodes []node
	for _, iss := range s.issues {
		if iss.StateID != stateID {
			continue
		}
		nodes = append(nodes, node{
			ID: iss.ID, Identifier: iss.Identifier, Title: iss.Title,
			Description: iss.Description, URL: iss.URL,
			SortOrder: iss.SortOrder,
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
		// adoptIssue passes the human identifier (e.g. MAR-12) when
		// transitioning new issues to in_progress, so fall back to
		// an identifier lookup before erroring.
		iss = s.findByIdentifier(id)
	}
	if iss == nil {
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

func newSource(t *testing.T, srvURL string) *Source {
	t.Helper()
	t.Setenv("LINEAR_API_KEY", "lin_test")
	src, err := NewFromConfig(Config{
		Team:            "MAR",
		ReadyState:      "Ready",
		InProgressState: "In Progress",
		DoneState:       "Done",
		APIKeyEnv:       "LINEAR_API_KEY",
		Endpoint:        srvURL,
	})
	if err != nil {
		t.Fatalf("NewFromConfig: %v", err)
	}
	return src
}

func TestSyncProjectsTasksWithoutFlippingState(t *testing.T) {
	stub := newStub(t)
	stub.addReady("issue-uuid-1", "MAR-12", "Wire up onboarding email", "Send welcome email on signup.")
	stub.addReady("issue-uuid-2", "MAR-13", "Crash on empty cart", "Repro: open cart without items.")

	srv := httptest.NewServer(stub.handler())
	defer srv.Close()

	root := t.TempDir()
	src := newSource(t, srv.URL)

	created, updated, err := src.Sync(context.Background(), root)
	if err != nil {
		t.Fatalf("Sync: %v", err)
	}
	if created != 2 || updated != 0 {
		t.Errorf("Sync = (created=%d, updated=%d), want (2, 0)", created, updated)
	}

	// Sync MUST leave Linear state alone: the rover-claim signal
	// (OnSpawn) owns the Ready -> In Progress transition. A pre-
	// MCOM-85 Sync flipped issues here at projection time and the
	// kanban no longer matched reality.
	for id, iss := range stub.issues {
		if iss.StateID != stub.states["Ready"] {
			t.Errorf("issue %s state = %s, want Ready (Sync must not flip)", id, iss.StateID)
		}
	}

	tasksDir := filepath.Join(root, "tasks")
	files, err := os.ReadDir(tasksDir)
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 2 {
		t.Fatalf("want 2 task files, got %d", len(files))
	}
	for _, f := range files {
		raw, err := os.ReadFile(filepath.Join(tasksDir, f.Name()))
		if err != nil {
			t.Fatal(err)
		}
		m, body, err := frontmatter.Parse(raw)
		if err != nil {
			t.Fatalf("parse %s: %v", f.Name(), err)
		}
		if m.Status != "active" {
			t.Errorf("%s: status = %q, want active", f.Name(), m.Status)
		}
		if m.Extra[matter.MatterKey] != "linear" {
			t.Errorf("%s: matter = %q, want linear", f.Name(), m.Extra[matter.MatterKey])
		}
		if !strings.HasPrefix(m.Extra[matter.MatterIDKey], "MAR-") {
			t.Errorf("%s: matter_id = %q", f.Name(), m.Extra[matter.MatterIDKey])
		}
		if !strings.HasPrefix(m.Extra[matter.MatterURLKey], "https://linear.app/") {
			t.Errorf("%s: matter_url = %q", f.Name(), m.Extra[matter.MatterURLKey])
		}
		if !strings.Contains(string(body), "Linear: https://linear.app/") {
			t.Errorf("%s: body missing Linear URL: %s", f.Name(), body)
		}
	}

	c2, u2, err := src.Sync(context.Background(), root)
	if err != nil {
		t.Fatalf("Sync (idempotent pass): %v", err)
	}
	if c2 != 0 || u2 != 0 {
		t.Errorf("expected no-op second pass, got (created=%d, updated=%d)", c2, u2)
	}
}

func TestSyncProjectsInLinearSortOrder(t *testing.T) {
	stub := newStub(t)
	// Identifier order is MAR-50 < MAR-51 < MAR-52, but sortOrder
	// inverts it: MAR-52 sits at the top of the kanban column. The stub
	// responds in identifier order; the production code must re-sort
	// client-side before iterating in adoptIssue.
	stub.addReady("u-50", "MAR-50", "Bottom of Ready", "").SortOrder = 99.0
	stub.addReady("u-51", "MAR-51", "Middle of Ready", "").SortOrder = 50.0
	stub.addReady("u-52", "MAR-52", "Top of Ready", "").SortOrder = -10.0

	srv := httptest.NewServer(stub.handler())
	defer srv.Close()

	root := t.TempDir()
	src := newSource(t, srv.URL)

	if _, _, err := src.Sync(context.Background(), root); err != nil {
		t.Fatalf("Sync: %v", err)
	}

	tasksDir := filepath.Join(root, "tasks")
	files, err := os.ReadDir(tasksDir)
	if err != nil {
		t.Fatal(err)
	}
	type stamped struct {
		identifier string
		sortOrder  string
	}
	var got []stamped
	for _, f := range files {
		raw, err := os.ReadFile(filepath.Join(tasksDir, f.Name()))
		if err != nil {
			t.Fatal(err)
		}
		m, _, err := frontmatter.Parse(raw)
		if err != nil {
			t.Fatalf("parse %s: %v", f.Name(), err)
		}
		got = append(got, stamped{
			identifier: m.Extra[matter.MatterIDKey],
			sortOrder:  m.Extra[matter.MatterSortOrderKey],
		})
	}

	wantOrder := map[string]string{
		"MAR-52": "-10",
		"MAR-51": "50",
		"MAR-50": "99",
	}
	for _, g := range got {
		want, ok := wantOrder[g.identifier]
		if !ok {
			t.Errorf("unexpected identifier %q stamped", g.identifier)
			continue
		}
		if g.sortOrder != want {
			t.Errorf("%s: matter_sort_order = %q, want %q", g.identifier, g.sortOrder, want)
		}
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
		Extra: map[string]string{
			matter.MatterKey:   "linear",
			matter.MatterIDKey: "MAR-21",
		},
	}
	if err := os.WriteFile(filepath.Join(tasksDir, "ship-checkout.md"),
		frontmatter.Write(m, []byte("\nbody\n")), 0o644); err != nil {
		t.Fatal(err)
	}

	src := newSource(t, srv.URL)
	created, updated, err := src.Sync(context.Background(), root)
	if err != nil {
		t.Fatalf("Sync: %v", err)
	}
	if created != 0 || updated != 1 {
		t.Errorf("Sync = (created=%d, updated=%d), want (0, 1)", created, updated)
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

	c2, u2, err := src.Sync(context.Background(), root)
	if err != nil {
		t.Fatalf("Sync second pass: %v", err)
	}
	if c2 != 0 || u2 != 0 {
		t.Errorf("second pass should not push again, got (created=%d, updated=%d)", c2, u2)
	}
}

func TestSyncRecognisesLegacyLinearKey(t *testing.T) {
	stub := newStub(t)
	iss := stub.addReady("issue-uuid-8", "MAR-30", "Legacy task", "")
	iss.StateID = stub.states["In Progress"]

	srv := httptest.NewServer(stub.handler())
	defer srv.Close()

	root := t.TempDir()
	tasksDir := filepath.Join(root, "tasks")
	if err := os.MkdirAll(tasksDir, 0o755); err != nil {
		t.Fatal(err)
	}
	// Pre-rename frontmatter shape: only the legacy `linear:` key.
	m := frontmatter.Meta{
		Status: "done", Slug: "legacy-task", Title: "Legacy task",
		Extra: map[string]string{"linear": "MAR-30"},
	}
	if err := os.WriteFile(filepath.Join(tasksDir, "legacy-task.md"),
		frontmatter.Write(m, []byte("\nbody\n")), 0o644); err != nil {
		t.Fatal(err)
	}

	src := newSource(t, srv.URL)
	if _, _, err := src.Sync(context.Background(), root); err != nil {
		t.Fatalf("Sync: %v", err)
	}
	if iss.StateID != stub.states["Done"] {
		t.Errorf("legacy task should have pushed Done, got state %q", iss.StateID)
	}
}

func TestOnSpawnFlipsReadyToInProgress(t *testing.T) {
	stub := newStub(t)
	iss := stub.addReady("issue-uuid-50", "MAR-50", "Claim me", "")

	srv := httptest.NewServer(stub.handler())
	defer srv.Close()

	src := newSource(t, srv.URL)
	err := src.OnSpawn(context.Background(), "claim-me", map[string]string{
		matter.MatterKey:   "linear",
		matter.MatterIDKey: "MAR-50",
	})
	if err != nil {
		t.Fatalf("OnSpawn: %v", err)
	}
	if iss.StateID != stub.states["In Progress"] {
		t.Errorf("OnSpawn should have flipped issue to In Progress, got state %q", iss.StateID)
	}
}

func TestOnSpawnIdempotentForAlreadyInProgress(t *testing.T) {
	stub := newStub(t)
	iss := stub.addReady("issue-uuid-51", "MAR-51", "Already claimed", "")
	iss.StateID = stub.states["In Progress"]

	srv := httptest.NewServer(stub.handler())
	defer srv.Close()

	src := newSource(t, srv.URL)
	err := src.OnSpawn(context.Background(), "already-claimed", map[string]string{
		matter.MatterKey:   "linear",
		matter.MatterIDKey: "MAR-51",
	})
	if err != nil {
		t.Fatalf("OnSpawn (idempotent): %v", err)
	}
	if iss.StateID != stub.states["In Progress"] {
		t.Errorf("issue should remain In Progress, got %q", iss.StateID)
	}
}

func TestOnSpawnIgnoresUnrelatedMatter(t *testing.T) {
	stub := newStub(t)
	stub.addReady("issue-uuid-52", "MAR-52", "Wrong adapter", "")

	srv := httptest.NewServer(stub.handler())
	defer srv.Close()

	src := newSource(t, srv.URL)
	err := src.OnSpawn(context.Background(), "wrong-adapter", map[string]string{
		matter.MatterKey:   "jira",
		matter.MatterIDKey: "MAR-52",
	})
	if err != nil {
		t.Fatalf("OnSpawn: %v", err)
	}
	if stub.calls != 0 {
		t.Errorf("OnSpawn should make 0 GraphQL calls when matter != linear, got %d", stub.calls)
	}
}

func TestOnSpawnNoOpWithoutID(t *testing.T) {
	stub := newStub(t)
	srv := httptest.NewServer(stub.handler())
	defer srv.Close()

	src := newSource(t, srv.URL)
	if err := src.OnSpawn(context.Background(), "no-id", map[string]string{}); err != nil {
		t.Fatalf("OnSpawn: %v", err)
	}
	if stub.calls != 0 {
		t.Errorf("want 0 calls, got %d", stub.calls)
	}
}

func TestOnDonePushesImmediately(t *testing.T) {
	stub := newStub(t)
	iss := stub.addReady("issue-uuid-9", "MAR-42", "Ship matter plugin", "")
	iss.StateID = stub.states["In Progress"]

	srv := httptest.NewServer(stub.handler())
	defer srv.Close()

	src := newSource(t, srv.URL)
	err := src.OnDone(context.Background(), "ship-matter-plugin", map[string]string{
		matter.MatterKey:   "linear",
		matter.MatterIDKey: "MAR-42",
	})
	if err != nil {
		t.Fatalf("OnDone: %v", err)
	}
	if iss.StateID != stub.states["Done"] {
		t.Errorf("OnDone should have moved issue to Done, got %q", iss.StateID)
	}
}

func TestOnDoneIgnoresUnrelatedMatter(t *testing.T) {
	stub := newStub(t)
	stub.addReady("issue-uuid-10", "MAR-99", "Wrong adapter", "")

	srv := httptest.NewServer(stub.handler())
	defer srv.Close()

	src := newSource(t, srv.URL)
	// matter=jira: not us, even though matter_id is set.
	err := src.OnDone(context.Background(), "wrong-adapter", map[string]string{
		matter.MatterKey:   "jira",
		matter.MatterIDKey: "MAR-99",
	})
	if err != nil {
		t.Fatalf("OnDone: %v", err)
	}
	if stub.calls != 0 {
		t.Errorf("OnDone should make 0 GraphQL calls when matter != linear, got %d", stub.calls)
	}
}

func TestOnDoneNoOpWithoutID(t *testing.T) {
	stub := newStub(t)
	srv := httptest.NewServer(stub.handler())
	defer srv.Close()

	src := newSource(t, srv.URL)
	if err := src.OnDone(context.Background(), "no-id", map[string]string{}); err != nil {
		t.Fatalf("OnDone: %v", err)
	}
	if stub.calls != 0 {
		t.Errorf("want 0 calls, got %d", stub.calls)
	}
}

func TestSyncErrorsOnUnknownState(t *testing.T) {
	stub := newStub(t)
	delete(stub.states, "Ready")

	srv := httptest.NewServer(stub.handler())
	defer srv.Close()

	src := newSource(t, srv.URL)
	if _, _, err := src.Sync(context.Background(), t.TempDir()); err == nil {
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

	src := newSource(t, srv.URL)
	_, _, err := src.Sync(context.Background(), t.TempDir())
	if err == nil || !strings.Contains(err.Error(), "rate limited") {
		t.Fatalf("expected rate-limited error, got %v", err)
	}
}

func TestParseConfigDefaultsAndValidation(t *testing.T) {
	// missing team -> error
	if _, err := parseConfig(matter.Config{
		Name: "linear",
		Options: map[string]string{
			"api_key_env": "LINEAR_API_KEY",
		},
	}); err == nil {
		t.Error("missing team should error")
	}

	// missing api_key source -> error
	if _, err := parseConfig(matter.Config{
		Name:    "linear",
		Options: map[string]string{"team": "MAR"},
	}); err == nil {
		t.Error("missing api_key_* should error")
	}

	// credential_api_key counts as api_key_file
	cfg, err := parseConfig(matter.Config{
		Name: "linear",
		Options: map[string]string{
			"team":               "MAR",
			"credential_api_key": "/run/credentials/x",
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if cfg.APIKeyFile != "/run/credentials/x" {
		t.Errorf("credential_api_key not adopted: %#v", cfg)
	}
	if cfg.ReadyState != "Ready" || cfg.DoneState != "Done" {
		t.Errorf("defaults not applied: %#v", cfg)
	}
}

func TestRegisteredViaInit(t *testing.T) {
	found := false
	for _, n := range matter.Registered() {
		if n == "linear" {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("linear should self-register via init(); registered = %v", matter.Registered())
	}
}
