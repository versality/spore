package linear

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/versality/spore/internal/matter"
	"github.com/versality/spore/internal/task/frontmatter"
)

// commentsTestEnv builds the standard fixtures for the comment-projection
// suite: a temp project root, an isolated XDG_STATE_HOME so cursor and
// inbox land under the test's tempdir, and an active linear task on
// disk for `identifier`. Returns the project root, slug, and inbox dir.
func commentsTestEnv(t *testing.T, identifier, slug string) (root, slugRet, inbox string) {
	t.Helper()
	root = t.TempDir()
	state := t.TempDir()
	t.Setenv("XDG_STATE_HOME", state)

	tasksDir := filepath.Join(root, "tasks")
	if err := os.MkdirAll(tasksDir, 0o755); err != nil {
		t.Fatal(err)
	}
	m := frontmatter.Meta{
		Status: "active", Slug: slug, Title: "active task",
		Extra: map[string]string{
			matter.MatterKey:   "linear",
			matter.MatterIDKey: identifier,
		},
	}
	if err := os.WriteFile(filepath.Join(tasksDir, slug+".md"),
		frontmatter.Write(m, []byte("\nbody\n")), 0o644); err != nil {
		t.Fatal(err)
	}
	inbox = filepath.Join(state, "spore", filepath.Base(root), slug, "inbox")
	return root, slug, inbox
}

func readInboxEnvelopes(t *testing.T, inbox string) []map[string]string {
	t.Helper()
	entries, err := os.ReadDir(inbox)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		t.Fatal(err)
	}
	var names []string
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		names = append(names, e.Name())
	}
	sort.Strings(names)
	var out []map[string]string
	for _, n := range names {
		raw, err := os.ReadFile(filepath.Join(inbox, n))
		if err != nil {
			t.Fatal(err)
		}
		var ev map[string]string
		if err := json.Unmarshal(raw, &ev); err != nil {
			t.Fatalf("envelope %s: %v", n, err)
		}
		out = append(out, ev)
	}
	return out
}

func cursorFor(t *testing.T, root, slug string) (time.Time, bool) {
	t.Helper()
	path, err := commentsCursorPath(root, slug)
	if err != nil {
		t.Fatalf("commentsCursorPath: %v", err)
	}
	b, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return time.Time{}, false
	}
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := time.Parse(time.RFC3339Nano, strings.TrimSpace(string(b)))
	if err != nil {
		t.Fatalf("cursor parse: %v (raw=%q)", err, b)
	}
	return parsed, true
}

func TestProjectCommentsSeedsCursorOnFirstObservation(t *testing.T) {
	stub := newStub(t)
	stub.comments["MAR-100"] = []stubComment{
		{ID: "c1", Body: "old comment", AuthorName: "alice",
			CreatedAt: time.Now().Add(-1 * time.Hour).UTC()},
	}
	srv := httptest.NewServer(stub.handler())
	defer srv.Close()

	root, slug, inbox := commentsTestEnv(t, "MAR-100", "active-task")
	src := newSource(t, srv.URL)

	if _, _, err := src.Sync(context.Background(), root); err != nil {
		t.Fatalf("Sync: %v", err)
	}

	envs := readInboxEnvelopes(t, inbox)
	if len(envs) != 0 {
		t.Errorf("first sync should not surface old comments, got %d envelopes", len(envs))
	}
	if _, ok := cursorFor(t, root, slug); !ok {
		t.Error("first sync should seed cursor file")
	}
}

func TestProjectCommentsWritesEnvelopeForNewComment(t *testing.T) {
	stub := newStub(t)
	srv := httptest.NewServer(stub.handler())
	defer srv.Close()

	root, slug, inbox := commentsTestEnv(t, "MAR-101", "ship-checkout")
	src := newSource(t, srv.URL)

	// Pass 1: seed cursor (no comments yet, no envelopes either).
	if _, _, err := src.Sync(context.Background(), root); err != nil {
		t.Fatalf("Sync seed: %v", err)
	}
	cursor1, ok := cursorFor(t, root, slug)
	if !ok {
		t.Fatal("cursor not seeded")
	}

	// Pass 2: a comment arrives upstream after the seed.
	commentTS := cursor1.Add(5 * time.Second)
	stub.comments["MAR-101"] = []stubComment{
		{ID: "c-new", Body: "rover please rebase", URL: "https://linear.app/c/c-new",
			AuthorName: "operator", CreatedAt: commentTS},
	}
	if _, _, err := src.Sync(context.Background(), root); err != nil {
		t.Fatalf("Sync project: %v", err)
	}

	envs := readInboxEnvelopes(t, inbox)
	if len(envs) != 1 {
		t.Fatalf("want 1 envelope, got %d", len(envs))
	}
	ev := envs[0]
	if ev["body"] != "rover please rebase" {
		t.Errorf("body = %q", ev["body"])
	}
	if ev["source"] != "linear:operator" {
		t.Errorf("source = %q, want linear:operator", ev["source"])
	}
	if ev["url"] != "https://linear.app/c/c-new" {
		t.Errorf("url = %q", ev["url"])
	}
	if ev["comment_id"] != "c-new" {
		t.Errorf("comment_id = %q", ev["comment_id"])
	}
	if _, err := time.Parse(time.RFC3339Nano, ev["ts"]); err != nil {
		t.Errorf("ts not RFC3339Nano: %q", ev["ts"])
	}

	cursor2, ok := cursorFor(t, root, slug)
	if !ok {
		t.Fatal("cursor missing after projection")
	}
	if !cursor2.Equal(commentTS.UTC()) && !cursor2.After(commentTS.UTC()) {
		t.Errorf("cursor did not advance: cursor2=%v commentTS=%v", cursor2, commentTS)
	}

	// Pass 3: idempotent. Same upstream state, no new envelopes.
	if _, _, err := src.Sync(context.Background(), root); err != nil {
		t.Fatalf("Sync idempotent: %v", err)
	}
	envs = readInboxEnvelopes(t, inbox)
	if len(envs) != 1 {
		t.Errorf("idempotent pass should not duplicate envelope, got %d", len(envs))
	}
}

func TestProjectCommentsRoutesByIdentifier(t *testing.T) {
	stub := newStub(t)
	srv := httptest.NewServer(stub.handler())
	defer srv.Close()

	// Two active tasks, both Linear-owned, different identifiers.
	root := t.TempDir()
	state := t.TempDir()
	t.Setenv("XDG_STATE_HOME", state)
	tasksDir := filepath.Join(root, "tasks")
	if err := os.MkdirAll(tasksDir, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, tt := range []struct{ id, slug string }{
		{"MAR-200", "alpha"}, {"MAR-201", "bravo"},
	} {
		m := frontmatter.Meta{
			Status: "active", Slug: tt.slug, Title: tt.slug,
			Extra: map[string]string{
				matter.MatterKey:   "linear",
				matter.MatterIDKey: tt.id,
			},
		}
		if err := os.WriteFile(filepath.Join(tasksDir, tt.slug+".md"),
			frontmatter.Write(m, []byte("\n")), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	src := newSource(t, srv.URL)
	if _, _, err := src.Sync(context.Background(), root); err != nil {
		t.Fatalf("Sync seed: %v", err)
	}

	now := time.Now().Add(2 * time.Second)
	stub.comments["MAR-200"] = []stubComment{
		{ID: "a1", Body: "for alpha", AuthorName: "op", CreatedAt: now},
	}
	stub.comments["MAR-201"] = []stubComment{
		{ID: "b1", Body: "for bravo", AuthorName: "op", CreatedAt: now},
	}
	if _, _, err := src.Sync(context.Background(), root); err != nil {
		t.Fatalf("Sync project: %v", err)
	}

	for _, tt := range []struct{ slug, want string }{
		{"alpha", "for alpha"}, {"bravo", "for bravo"},
	} {
		inbox := filepath.Join(state, "spore", filepath.Base(root), tt.slug, "inbox")
		envs := readInboxEnvelopes(t, inbox)
		if len(envs) != 1 {
			t.Fatalf("%s: want 1 envelope, got %d", tt.slug, len(envs))
		}
		if envs[0]["body"] != tt.want {
			t.Errorf("%s body = %q, want %q", tt.slug, envs[0]["body"], tt.want)
		}
	}
}

func TestProjectCommentsSkipsDoneTasks(t *testing.T) {
	stub := newStub(t)
	stub.comments["MAR-300"] = []stubComment{
		{ID: "c1", Body: "should not surface", AuthorName: "op",
			CreatedAt: time.Now().Add(time.Hour)},
	}
	srv := httptest.NewServer(stub.handler())
	defer srv.Close()

	root := t.TempDir()
	state := t.TempDir()
	t.Setenv("XDG_STATE_HOME", state)
	tasksDir := filepath.Join(root, "tasks")
	if err := os.MkdirAll(tasksDir, 0o755); err != nil {
		t.Fatal(err)
	}
	m := frontmatter.Meta{
		Status: "done", Slug: "old-task", Title: "old",
		Extra: map[string]string{
			matter.MatterKey:   "linear",
			matter.MatterIDKey: "MAR-300",
			"linear_done":      "yes",
		},
	}
	if err := os.WriteFile(filepath.Join(tasksDir, "old-task.md"),
		frontmatter.Write(m, []byte("\n")), 0o644); err != nil {
		t.Fatal(err)
	}

	src := newSource(t, srv.URL)
	if _, _, err := src.Sync(context.Background(), root); err != nil {
		t.Fatalf("Sync: %v", err)
	}

	inbox := filepath.Join(state, "spore", filepath.Base(root), "old-task", "inbox")
	envs := readInboxEnvelopes(t, inbox)
	if len(envs) != 0 {
		t.Errorf("done tasks should not project comments, got %d envelopes", len(envs))
	}
}

func TestProjectCommentsTolerantOfPerTicketFetchError(t *testing.T) {
	// One stub returns 500 for a specific identifier; the other ticket
	// must still get its envelope. This covers the "scoped per-ticket"
	// invariant in projectComments.
	mux := http.NewServeMux()
	stub := newStub(t)
	stub.comments["MAR-401"] = []stubComment{
		{ID: "ok1", Body: "ok ticket", AuthorName: "op",
			CreatedAt: time.Now().Add(time.Hour)},
	}
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		// Read body to inspect the issue id.
		var body struct {
			Query     string         `json:"query"`
			Variables map[string]any `json:"variables"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		// Replay through the canonical stub via a fresh request.
		// For the "MAR-400" ticket's IssueComments, fail; otherwise
		// delegate to the stub handler.
		if strings.Contains(body.Query, "IssueComments") {
			id, _ := body.Variables["id"].(string)
			if id == "MAR-400" {
				http.Error(w, "boom", http.StatusInternalServerError)
				return
			}
		}
		// Re-marshal and dispatch to stub. The simplest cheat: call
		// the stub's switch table directly via a synthetic request.
		// We rebuild the request body so stub's handler can read it.
		raw, _ := json.Marshal(body)
		r2 := httptest.NewRequest("POST", "/", strings.NewReader(string(raw)))
		r2.Header = r.Header
		stub.handler()(w, r2)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	root := t.TempDir()
	state := t.TempDir()
	t.Setenv("XDG_STATE_HOME", state)
	tasksDir := filepath.Join(root, "tasks")
	if err := os.MkdirAll(tasksDir, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, tt := range []struct{ id, slug string }{
		{"MAR-400", "broken"}, {"MAR-401", "ok"},
	} {
		m := frontmatter.Meta{
			Status: "active", Slug: tt.slug, Title: tt.slug,
			Extra: map[string]string{
				matter.MatterKey:   "linear",
				matter.MatterIDKey: tt.id,
			},
		}
		if err := os.WriteFile(filepath.Join(tasksDir, tt.slug+".md"),
			frontmatter.Write(m, []byte("\n")), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	src := newSource(t, srv.URL)
	// Seed pass.
	if _, _, err := src.Sync(context.Background(), root); err != nil {
		t.Fatalf("Sync seed: %v", err)
	}
	// Project pass: MAR-400 fetch fails, MAR-401 succeeds.
	if _, _, err := src.Sync(context.Background(), root); err != nil {
		t.Fatalf("Sync project: %v", err)
	}

	okInbox := filepath.Join(state, "spore", filepath.Base(root), "ok", "inbox")
	envs := readInboxEnvelopes(t, okInbox)
	if len(envs) != 1 {
		t.Fatalf("ok ticket should still project despite peer failure, got %d envelopes", len(envs))
	}
	if envs[0]["body"] != "ok ticket" {
		t.Errorf("body = %q", envs[0]["body"])
	}
}

func TestReadCommentCursorMalformedReturnsZero(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "linear-comments.cursor")
	if err := os.WriteFile(path, []byte("not-a-timestamp"), 0o644); err != nil {
		t.Fatal(err)
	}
	got, err := readCommentCursor(path)
	if err != nil {
		t.Fatalf("readCommentCursor: %v", err)
	}
	if !got.IsZero() {
		t.Errorf("malformed cursor should parse as zero time, got %v", got)
	}
}

func TestWriteCommentCursorRoundtrip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "nested", "linear-comments.cursor")
	want := time.Now().UTC().Truncate(time.Nanosecond)
	if err := writeCommentCursor(path, want); err != nil {
		t.Fatalf("write: %v", err)
	}
	got, err := readCommentCursor(path)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if !got.Equal(want) {
		t.Errorf("roundtrip: got %v, want %v", got, want)
	}
}

func TestCommentEnvelopeFilenameShape(t *testing.T) {
	dir := t.TempDir()
	if err := ensureInboxDirs(dir); err != nil {
		t.Fatal(err)
	}
	c := linearComment{
		ID: "c1", Body: "hi", URL: "https://linear.app/c/c1",
		AuthorName: "op", CreatedAt: time.Now().UTC(),
	}
	if err := writeCommentEnvelope(dir, c); err != nil {
		t.Fatalf("writeCommentEnvelope: %v", err)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	var fname string
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(e.Name(), ".json") {
			fname = e.Name()
			break
		}
	}
	if fname == "" {
		t.Fatal("no envelope written")
	}
	if !strings.HasSuffix(fname, "-linear-comment.json") {
		t.Errorf("filename should end with -linear-comment.json, got %q", fname)
	}
}
