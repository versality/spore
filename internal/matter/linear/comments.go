package linear

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/versality/spore/internal/task"
	"github.com/versality/spore/internal/task/frontmatter"
)

// projectComments fetches new Linear comments for every active task that
// carries a Linear matter id and drops one tell-envelope JSON per
// comment into that slug's spore inbox. The watch-inbox Stop hook
// drains the inbox and wakes the rover, so an operator commenting on a
// Linear ticket reaches the rover working it.
//
// Per-ticket cursor state lives at <StateDirForProject>/<slug>/linear-comments.cursor
// and holds the RFC3339Nano timestamp of the most recent comment we
// have surfaced. First observation seeds the cursor at "now" so we do
// not flood the inbox with the entire historical thread.
//
// Failures are scoped per-ticket: a single ticket's GraphQL miss does
// not abort projection for the rest of the active set. Sync still
// returns nil so the create / done passes that already succeeded keep
// counting; per-ticket errors are logged via stderr.
func (s *Source) projectComments(projectRoot, tasksDir string) error {
	active, err := activeLinearTasks(tasksDir)
	if err != nil {
		return err
	}
	if len(active) == 0 {
		return nil
	}
	for _, t := range active {
		if err := s.projectIssueComments(projectRoot, t.slug, t.identifier); err != nil {
			fmt.Fprintf(os.Stderr, "matter.linear: comments for %s (%s): %v\n", t.identifier, t.slug, err)
		}
	}
	return nil
}

// projectIssueComments handles one ticket: read cursor, fetch new
// comments, write per-comment envelopes, advance cursor.
func (s *Source) projectIssueComments(projectRoot, slug, identifier string) error {
	cursorPath, err := commentsCursorPath(projectRoot, slug)
	if err != nil {
		return err
	}
	cursor, err := readCommentCursor(cursorPath)
	if err != nil {
		return fmt.Errorf("read cursor: %w", err)
	}
	if cursor.IsZero() {
		// Seed at "now" so we do not surface historical comments;
		// the issue description already lives in the brief body.
		return writeCommentCursor(cursorPath, time.Now().UTC())
	}

	comments, err := s.fetchComments(identifier, cursor)
	if err != nil {
		return fmt.Errorf("fetch: %w", err)
	}
	if len(comments) == 0 {
		return nil
	}
	sort.SliceStable(comments, func(i, j int) bool {
		return comments[i].CreatedAt.Before(comments[j].CreatedAt)
	})

	inboxDir, err := task.InboxDirForProject(projectRoot, slug)
	if err != nil {
		return fmt.Errorf("inbox dir: %w", err)
	}
	if err := ensureInboxDirs(inboxDir); err != nil {
		return fmt.Errorf("ensure inbox: %w", err)
	}

	newest := cursor
	for _, c := range comments {
		if !c.CreatedAt.After(cursor) {
			continue
		}
		if err := writeCommentEnvelope(inboxDir, c); err != nil {
			return fmt.Errorf("write envelope %s: %w", c.ID, err)
		}
		if c.CreatedAt.After(newest) {
			newest = c.CreatedAt
		}
	}
	if newest.After(cursor) {
		if err := writeCommentCursor(cursorPath, newest); err != nil {
			return fmt.Errorf("advance cursor: %w", err)
		}
	}
	return nil
}

type linearActiveTask struct {
	slug       string
	identifier string
}

// activeLinearTasks returns rows for tasks where status is set
// (i.e. on disk and not archived) and the Linear matter id is set.
// Done tasks are excluded: by the time their identifier is on disk
// without linear_done stamped, pendingDonePushes has already
// surfaced them; comment-projection adds nothing for the rover (the
// rover process is gone).
func activeLinearTasks(tasksDir string) ([]linearActiveTask, error) {
	entries, err := os.ReadDir(tasksDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var rows []linearActiveTask
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".md") {
			continue
		}
		raw, err := os.ReadFile(filepath.Join(tasksDir, e.Name()))
		if err != nil {
			return nil, err
		}
		m, _, err := frontmatter.Parse(raw)
		if err != nil {
			continue
		}
		if m.Status == "done" {
			continue
		}
		id := linearIDFromMeta(m.Extra)
		if id == "" {
			continue
		}
		rows = append(rows, linearActiveTask{slug: m.Slug, identifier: id})
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].slug < rows[j].slug })
	return rows, nil
}

// commentsCursorPath returns the per-slug cursor file path. Lives
// next to the inbox dir under StateDir so a rover wipe (clear inbox)
// can reset the cursor by removing the slug dir.
func commentsCursorPath(projectRoot, slug string) (string, error) {
	stateDir, err := task.StateDirForProject(projectRoot)
	if err != nil {
		return "", err
	}
	return filepath.Join(stateDir, slug, "linear-comments.cursor"), nil
}

// readCommentCursor returns the parsed cursor or zero time when the
// file is missing. A malformed or empty file is treated as missing so
// a corrupted cursor self-heals on the next pass.
func readCommentCursor(path string) (time.Time, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return time.Time{}, nil
		}
		return time.Time{}, err
	}
	s := strings.TrimSpace(string(b))
	if s == "" {
		return time.Time{}, nil
	}
	t, err := time.Parse(time.RFC3339Nano, s)
	if err != nil {
		return time.Time{}, nil
	}
	return t.UTC(), nil
}

// writeCommentCursor persists ts to path atomically. The directory is
// created on demand; a stale file is replaced via rename so a
// concurrent reader never sees a partial write.
func writeCommentCursor(path string, ts time.Time) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".cursor-*")
	if err != nil {
		return err
	}
	if _, err := tmp.WriteString(ts.UTC().Format(time.RFC3339Nano)); err != nil {
		_ = tmp.Close()
		_ = os.Remove(tmp.Name())
		return err
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(tmp.Name())
		return err
	}
	return os.Rename(tmp.Name(), path)
}

// writeCommentEnvelope renders one tell envelope and lands it in
// inboxDir via inboxDir/.tmp/ + rename so the watch-inbox drain never
// reads a partial write. Filename is "<unix-nanos>-<rand>-linear-comment.json"
// so two comments fetched in the same nanosecond do not collide.
//
// The body shape matches what hooks/watchinbox.go's readTellFile
// expects: ts, source, body (plus author and url for rover context).
func writeCommentEnvelope(inboxDir string, c linearComment) error {
	stamp := time.Now().UnixNano()
	rnd, err := randHex(4)
	if err != nil {
		return err
	}
	name := fmt.Sprintf("%d-%s-linear-comment.json", stamp, rnd)
	tmp := filepath.Join(inboxDir, ".tmp", name)
	final := filepath.Join(inboxDir, name)

	source := "linear-comment"
	if c.AuthorName != "" {
		source = "linear:" + c.AuthorName
	}
	payload := map[string]string{
		"ts":     c.CreatedAt.UTC().Format(time.RFC3339Nano),
		"source": source,
		"body":   c.Body,
	}
	if c.URL != "" {
		payload["url"] = c.URL
	}
	if c.ID != "" {
		payload["comment_id"] = c.ID
	}
	b, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	if err := os.WriteFile(tmp, b, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, final)
}

func ensureInboxDirs(inboxDir string) error {
	for _, sub := range []string{"", ".tmp", "read"} {
		if err := os.MkdirAll(filepath.Join(inboxDir, sub), 0o755); err != nil {
			return err
		}
	}
	return nil
}

func randHex(n int) (string, error) {
	buf := make([]byte, n)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return hex.EncodeToString(buf), nil
}

type linearComment struct {
	ID         string
	Body       string
	URL        string
	AuthorName string
	CreatedAt  time.Time
}

// fetchComments returns Linear comments for issueID with createdAt
// strictly greater than since. The GraphQL filter narrows the
// response server-side; client-side we still drop anything <= since
// to defend against clock skew between Linear and the host.
//
// The Linear comments edge defaults to a 50-row page; if a busy
// ticket accrues more than that between syncs, the older overflow is
// dropped. This is a deliberate tradeoff -- pagination doubles the
// surface area for an edge that, in practice, fires on operator
// drive-by comments. A future iteration can add a `pageInfo` loop.
func (s *Source) fetchComments(issueID string, since time.Time) ([]linearComment, error) {
	const q = `query IssueComments($id: String!, $since: DateTime) {
  issue(id: $id) {
    comments(filter: {createdAt: {gt: $since}}) {
      nodes {
        id
        body
        createdAt
        url
        user { name }
      }
    }
  }
}`
	vars := map[string]any{
		"id":    issueID,
		"since": since.UTC().Format(time.RFC3339Nano),
	}
	var resp struct {
		Data struct {
			Issue struct {
				Comments struct {
					Nodes []struct {
						ID        string    `json:"id"`
						Body      string    `json:"body"`
						URL       string    `json:"url"`
						CreatedAt time.Time `json:"createdAt"`
						User      struct {
							Name string `json:"name"`
						} `json:"user"`
					} `json:"nodes"`
				} `json:"comments"`
			} `json:"issue"`
		} `json:"data"`
	}
	if err := s.graphQL(q, vars, &resp); err != nil {
		return nil, err
	}
	out := make([]linearComment, 0, len(resp.Data.Issue.Comments.Nodes))
	for _, n := range resp.Data.Issue.Comments.Nodes {
		out = append(out, linearComment{
			ID:         n.ID,
			Body:       n.Body,
			URL:        n.URL,
			AuthorName: n.User.Name,
			CreatedAt:  n.CreatedAt.UTC(),
		})
	}
	return out, nil
}
