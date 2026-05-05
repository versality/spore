// Package linear is the Linear adapter for the matter plug point.
//
// One Sync pass:
//  1. Resolve the workflow state IDs for the configured team (one
//     GraphQL call, cached in the Source between passes since state
//     IDs are stable).
//  2. List issues in ready_state for team. For each issue not yet
//     present on disk (no tasks/<slug>.md carries `matter_id: <id>`),
//     create a tasks/<slug>.md and push the issue ready->in-progress.
//  3. Walk tasks/. For every status=done task that carries
//     `matter_id: <id>` and is missing `linear_done: yes`, push the
//     issue to done_state and stamp `linear_done: yes`. This is the
//     safety-net path; the synchronous push happens via OnDone the
//     moment the task flips to done.
//
// The adapter registers itself under the name "linear" via init(),
// so importing this package is enough to make `[matter.linear]` (or
// SPORE_MATTER_LINEAR__*) wiring activate.
//
// Frontmatter convention. Tasks created by this adapter carry the
// generic matter keys (matter, matter_id, matter_url) plus the
// adapter-private `linear_done` stamp once the upstream Done push
// has succeeded. Reads also accept the legacy `linear:` /
// `linear_url:` keys so tasks created before the rename keep working.
//
// All HTTP traffic flows through a single endpoint and a single
// Authorization header so a test can swap in an httptest server.
package linear

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/versality/spore/internal/matter"
	"github.com/versality/spore/internal/task"
	"github.com/versality/spore/internal/task/frontmatter"
)

const (
	sourceName     = "linear"
	defaultTimeout = 30 * time.Second

	// linearDoneKey is the per-task frontmatter stamp Sync writes
	// after a successful Done push so future passes skip the issue.
	linearDoneKey   = "linear_done"
	linearDoneValue = "yes"

	// legacy frontmatter keys, accepted on read for tasks that
	// pre-date the matter/matter_id rename.
	legacyIDKey  = "linear"
	legacyURLKey = "linear_url"
)

func init() {
	matter.Register(sourceName, New)
}

// HTTPDoer is satisfied by *http.Client; tests can swap a stub.
type HTTPDoer interface {
	Do(*http.Request) (*http.Response, error)
}

// Config is the parsed [matter.linear] section. It is a snapshot of
// the adapter-specific fields the plug-point loader hands us, after
// type-checking and defaulting. Tests build it directly to avoid the
// matter.Config -> parseConfig roundtrip.
type Config struct {
	Team            string
	ReadyState      string
	InProgressState string
	DoneState       string
	APIKeyEnv       string
	APIKeyFile      string
	Endpoint        string
}

// Source is the Linear adapter. Construct via New (with a parsed
// matter.Config) or NewFromConfig (with a typed Config).
type Source struct {
	cfg    Config
	client HTTPDoer
	apiKey string
	// stateIDs caches workflow-state name -> id for the configured
	// team, populated lazily on the first Sync.
	stateIDs map[string]string
}

// Option tweaks Source construction in tests.
type Option func(*Source)

// WithHTTPClient overrides the http.Client used for GraphQL calls.
// The default is http.DefaultClient with a 30s timeout copy.
func WithHTTPClient(c HTTPDoer) Option {
	return func(s *Source) { s.client = c }
}

// New is the matter.Factory entry point. It parses c.Options into a
// typed Config, resolves the API key eagerly so a misconfiguration
// fails the reconcile pass loudly, and returns a *Source.
func New(c matter.Config) (matter.Matter, error) {
	cfg, err := parseConfig(c)
	if err != nil {
		return nil, err
	}
	return NewFromConfig(cfg)
}

// NewFromConfig builds a Source from an already-typed Config. Useful
// for tests that want to skip the matter.Config plumbing.
func NewFromConfig(cfg Config, opts ...Option) (*Source, error) {
	key, err := resolveAPIKey(cfg)
	if err != nil {
		return nil, err
	}
	s := &Source{
		cfg:    cfg,
		apiKey: key,
		client: &http.Client{Timeout: defaultTimeout},
	}
	for _, opt := range opts {
		opt(s)
	}
	return s, nil
}

// Name returns "linear".
func (*Source) Name() string { return sourceName }

// Sync runs one full pass. See package doc for the sequence. The
// returned counters cover this pass only (re-syncing an unchanged
// upstream reports 0 / 0).
func (s *Source) Sync(ctx context.Context, projectRoot string) (created, updated int, err error) {
	tasksDir := filepath.Join(projectRoot, "tasks")
	if err := os.MkdirAll(tasksDir, 0o755); err != nil {
		return 0, 0, err
	}

	if err := s.loadStateIDs(); err != nil {
		return 0, 0, err
	}
	readyID, ok := s.stateIDs[s.cfg.ReadyState]
	if !ok {
		return 0, 0, fmt.Errorf("matter.linear: ready_state %q not found in team %s", s.cfg.ReadyState, s.cfg.Team)
	}
	inProgressID, ok := s.stateIDs[s.cfg.InProgressState]
	if !ok {
		return 0, 0, fmt.Errorf("matter.linear: in_progress_state %q not found in team %s", s.cfg.InProgressState, s.cfg.Team)
	}
	doneID, ok := s.stateIDs[s.cfg.DoneState]
	if !ok {
		return 0, 0, fmt.Errorf("matter.linear: done_state %q not found in team %s", s.cfg.DoneState, s.cfg.Team)
	}

	known, err := indexLinearTasks(tasksDir)
	if err != nil {
		return 0, 0, err
	}

	ready, err := s.listIssuesByState(readyID)
	if err != nil {
		return 0, 0, err
	}
	for _, issue := range ready {
		if _, dup := known[issue.Identifier]; dup {
			continue
		}
		slug, err := s.adoptIssue(tasksDir, issue)
		if err != nil {
			return created, updated, fmt.Errorf("matter.linear: adopt %s: %w", issue.Identifier, err)
		}
		if err := s.transitionIssue(issue.ID, inProgressID); err != nil {
			return created, updated, fmt.Errorf("matter.linear: transition %s -> in_progress: %w", issue.Identifier, err)
		}
		created++
		known[issue.Identifier] = slug
	}

	donePending, err := pendingDonePushes(tasksDir)
	if err != nil {
		return created, updated, err
	}
	for _, p := range donePending {
		if err := s.transitionIssue(p.identifier, doneID); err != nil {
			return created, updated, fmt.Errorf("matter.linear: transition %s -> done: %w", p.identifier, err)
		}
		if err := stampLinearDone(tasksDir, p.slug); err != nil {
			return created, updated, fmt.Errorf("matter.linear: stamp %s linear_done: %w", p.slug, err)
		}
		updated++
	}
	return created, updated, nil
}

// OnDone is the synchronous push for a task that just flipped to
// done. Sync's done-walk is the fallback when this hook misses (the
// reconciler was off, the adapter was down, the task was edited
// outside spore task done). The push is idempotent on Linear's side,
// so the next Sync re-pushing as a no-op is harmless.
func (s *Source) OnDone(ctx context.Context, slug string, meta map[string]string) error {
	id := issueIDFromMeta(meta)
	if id == "" {
		return nil
	}
	if err := s.loadStateIDs(); err != nil {
		return err
	}
	doneID, ok := s.stateIDs[s.cfg.DoneState]
	if !ok {
		return fmt.Errorf("matter.linear: done_state %q not found in team %s", s.cfg.DoneState, s.cfg.Team)
	}
	return s.transitionIssue(id, doneID)
}

// parseConfig translates a generic matter.Config into the typed
// Config. credential_api_key is the storage shape the NixOS module's
// matters.linear.credentialFiles.api_key option renders into via
// SPORE_MATTER_LINEAR__CREDENTIAL_API_KEY=$CREDENTIALS_DIRECTORY/...,
// so it counts as an api_key_file source.
func parseConfig(c matter.Config) (Config, error) {
	cfg := Config{
		Team:            c.Option("team", ""),
		ReadyState:      c.Option("ready_state", "Ready"),
		InProgressState: c.Option("in_progress_state", "In Progress"),
		DoneState:       c.Option("done_state", "Done"),
		APIKeyEnv:       c.Option("api_key_env", ""),
		APIKeyFile:      c.Option("api_key_file", ""),
		Endpoint:        c.Option("endpoint", "https://api.linear.app/graphql"),
	}
	if cfg.APIKeyFile == "" {
		cfg.APIKeyFile = c.Option("credential_api_key", "")
	}
	if cfg.Team == "" {
		return cfg, fmt.Errorf("matter.linear: team is required (e.g. team = \"MAR\")")
	}
	if cfg.APIKeyEnv == "" && cfg.APIKeyFile == "" {
		return cfg, fmt.Errorf("matter.linear: api_key_env, api_key_file, or credential_api_key is required")
	}
	return cfg, nil
}

// resolveAPIKey reads the Linear API key from APIKeyFile when set
// (joined under $CREDENTIALS_DIRECTORY when the path is relative),
// else from APIKeyEnv. Returns the trimmed key.
func resolveAPIKey(cfg Config) (string, error) {
	if cfg.APIKeyFile != "" {
		p := cfg.APIKeyFile
		if !filepath.IsAbs(p) {
			if dir := os.Getenv("CREDENTIALS_DIRECTORY"); dir != "" {
				p = filepath.Join(dir, p)
			}
		}
		b, err := os.ReadFile(p)
		if err != nil {
			return "", fmt.Errorf("matter.linear: read api_key_file %s: %w", p, err)
		}
		key := strings.TrimSpace(string(b))
		if key == "" {
			return "", fmt.Errorf("matter.linear: api_key_file %s is empty", p)
		}
		return key, nil
	}
	key := strings.TrimSpace(os.Getenv(cfg.APIKeyEnv))
	if key == "" {
		return "", fmt.Errorf("matter.linear: env %s is empty or unset", cfg.APIKeyEnv)
	}
	return key, nil
}

// adoptIssue writes a new tasks/<slug>.md for issue and returns the
// slug it allocated. Title-derived; collisions are resolved via
// task.Allocate. The Linear identifier (e.g. MAR-42) lands in the
// generic matter_id frontmatter slot so the next Sync (and OnDone)
// can find the issue without scanning.
func (s *Source) adoptIssue(absTasks string, issue linearIssue) (string, error) {
	base := task.Slugify(issue.Title)
	if base == "" {
		base = task.Slugify(issue.Identifier)
	}
	slug, err := task.Allocate(absTasks, base)
	if err != nil {
		return "", err
	}
	m := frontmatter.Meta{
		Status:  "active",
		Slug:    slug,
		Title:   issue.Title,
		Created: time.Now().UTC().Format("2006-01-02"),
		Extra: map[string]string{
			matter.MatterKey:   sourceName,
			matter.MatterIDKey: issue.Identifier,
		},
	}
	if issue.URL != "" {
		m.Extra[matter.MatterURLKey] = issue.URL
	}
	body := buildBriefBody(issue)
	path := filepath.Join(absTasks, slug+".md")
	return slug, os.WriteFile(path, frontmatter.Write(m, body), 0o644)
}

func buildBriefBody(issue linearIssue) []byte {
	var b strings.Builder
	b.WriteString("\n# ")
	b.WriteString(issue.Identifier)
	if issue.Title != "" {
		b.WriteString(" - ")
		b.WriteString(issue.Title)
	}
	b.WriteString("\n\n")
	if issue.URL != "" {
		b.WriteString("Linear: ")
		b.WriteString(issue.URL)
		b.WriteString("\n\n")
	}
	desc := strings.TrimSpace(issue.Description)
	if desc == "" {
		desc = "(no description on the Linear issue)"
	}
	b.WriteString(desc)
	b.WriteString("\n")
	return []byte(b.String())
}

type linearTaskRow struct {
	slug       string
	identifier string
}

// indexLinearTasks scans tasksDir and returns identifier -> slug for
// every task that carries a Linear matter id. Used to skip
// re-adopting issues already present on disk. Reads both the
// generic matter_id key and the legacy linear: key so a kernel
// upgrade does not orphan pre-rename tasks.
func indexLinearTasks(tasksDir string) (map[string]string, error) {
	out := map[string]string{}
	entries, err := os.ReadDir(tasksDir)
	if err != nil {
		if os.IsNotExist(err) {
			return out, nil
		}
		return nil, err
	}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".md") {
			continue
		}
		raw, err := os.ReadFile(filepath.Join(tasksDir, e.Name()))
		if err != nil {
			continue
		}
		m, _, err := frontmatter.Parse(raw)
		if err != nil {
			continue
		}
		id := linearIDFromMeta(m.Extra)
		if id == "" {
			continue
		}
		out[id] = m.Slug
	}
	return out, nil
}

// pendingDonePushes returns rows for tasks where status=done, the
// linear matter id is set, and linear_done is not yet stamped.
func pendingDonePushes(tasksDir string) ([]linearTaskRow, error) {
	var rows []linearTaskRow
	entries, err := os.ReadDir(tasksDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
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
		if m.Status != "done" {
			continue
		}
		id := linearIDFromMeta(m.Extra)
		if id == "" {
			continue
		}
		if m.Extra[linearDoneKey] == linearDoneValue {
			continue
		}
		rows = append(rows, linearTaskRow{slug: m.Slug, identifier: id})
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].slug < rows[j].slug })
	return rows, nil
}

// stampLinearDone re-writes tasks/<slug>.md with `linear_done: yes`
// in the frontmatter so a future pass does not push the same issue
// again. The body is preserved byte-for-byte.
func stampLinearDone(tasksDir, slug string) error {
	path := filepath.Join(tasksDir, slug+".md")
	raw, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	m, body, err := frontmatter.Parse(raw)
	if err != nil {
		return err
	}
	if m.Extra == nil {
		m.Extra = map[string]string{}
	}
	m.Extra[linearDoneKey] = linearDoneValue
	return os.WriteFile(path, frontmatter.Write(m, body), 0o644)
}

// linearIDFromMeta returns the upstream issue identifier from the
// task's frontmatter, preferring the generic matter_id slot but
// falling back to the legacy `linear:` key. Returns "" when the task
// has no Linear linkage or names a different matter.
func linearIDFromMeta(extra map[string]string) string {
	if extra == nil {
		return ""
	}
	if name := extra[matter.MatterKey]; name != "" && name != sourceName {
		return ""
	}
	if id := extra[matter.MatterIDKey]; id != "" {
		return id
	}
	return extra[legacyIDKey]
}

// issueIDFromMeta is the OnDone counterpart of linearIDFromMeta. It
// is identical today but kept separate so future divergences (e.g.
// preferring matter_url over matter_id for upstreams that key on
// URL) stay scoped to one call site.
func issueIDFromMeta(extra map[string]string) string {
	return linearIDFromMeta(extra)
}

// transitionIssue maps the issueUpdate mutation. Linear treats the
// id field on issueUpdate as the issue ID, which can be either the
// canonical UUID or the human identifier (e.g. "MAR-42"); we pass
// whichever the caller hands us.
func (s *Source) transitionIssue(issueID, stateID string) error {
	const mutation = `mutation IssueUpdate($id: String!, $stateId: String!) {
  issueUpdate(id: $id, input: {stateId: $stateId}) { success }
}`
	var resp struct {
		Data struct {
			IssueUpdate struct {
				Success bool `json:"success"`
			} `json:"issueUpdate"`
		} `json:"data"`
	}
	vars := map[string]any{"id": issueID, "stateId": stateID}
	if err := s.graphQL(mutation, vars, &resp); err != nil {
		return err
	}
	if !resp.Data.IssueUpdate.Success {
		return fmt.Errorf("issueUpdate returned success=false for %s", issueID)
	}
	return nil
}

func (s *Source) loadStateIDs() error {
	if s.stateIDs != nil {
		return nil
	}
	const q = `query States($team: String!) {
  workflowStates(filter: {team: {key: {eq: $team}}}) {
    nodes { id name }
  }
}`
	var resp struct {
		Data struct {
			WorkflowStates struct {
				Nodes []struct {
					ID   string `json:"id"`
					Name string `json:"name"`
				} `json:"nodes"`
			} `json:"workflowStates"`
		} `json:"data"`
	}
	if err := s.graphQL(q, map[string]any{"team": s.cfg.Team}, &resp); err != nil {
		return err
	}
	out := map[string]string{}
	for _, n := range resp.Data.WorkflowStates.Nodes {
		out[n.Name] = n.ID
	}
	if len(out) == 0 {
		return fmt.Errorf("matter.linear: workflowStates returned no nodes for team %s", s.cfg.Team)
	}
	s.stateIDs = out
	return nil
}

type linearIssue struct {
	ID          string `json:"id"`
	Identifier  string `json:"identifier"`
	Title       string `json:"title"`
	Description string `json:"description"`
	URL         string `json:"url"`
}

func (s *Source) listIssuesByState(stateID string) ([]linearIssue, error) {
	const q = `query StateIssues($stateId: ID!) {
  issues(filter: {state: {id: {eq: $stateId}}}) {
    nodes { id identifier title description url }
  }
}`
	var resp struct {
		Data struct {
			Issues struct {
				Nodes []linearIssue `json:"nodes"`
			} `json:"issues"`
		} `json:"data"`
	}
	if err := s.graphQL(q, map[string]any{"stateId": stateID}, &resp); err != nil {
		return nil, err
	}
	return resp.Data.Issues.Nodes, nil
}

// graphQL sends one POST to the configured endpoint and decodes the
// response into out. A 200 with a non-empty `errors` array surfaces
// as an error so a partial GraphQL failure does not look successful.
func (s *Source) graphQL(query string, vars map[string]any, out any) error {
	body, err := json.Marshal(map[string]any{"query": query, "variables": vars})
	if err != nil {
		return err
	}
	req, err := http.NewRequest("POST", s.cfg.Endpoint, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", s.apiKey)
	resp, err := s.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return err
	}
	if resp.StatusCode/100 != 2 {
		return fmt.Errorf("linear: HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(raw)))
	}
	var probe struct {
		Errors []struct {
			Message string `json:"message"`
		} `json:"errors"`
	}
	if err := json.Unmarshal(raw, &probe); err == nil && len(probe.Errors) > 0 {
		msgs := make([]string, 0, len(probe.Errors))
		for _, e := range probe.Errors {
			msgs = append(msgs, e.Message)
		}
		return fmt.Errorf("linear graphql: %s", strings.Join(msgs, "; "))
	}
	if out == nil {
		return nil
	}
	return json.Unmarshal(raw, out)
}
