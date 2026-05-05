// Package linear is the Linear adapter for the matter plug point.
//
// One Sync pass:
//  1. Resolve the workflow state IDs for the configured team (one
//     GraphQL call, cached in the Source between passes since state
//     IDs are stable).
//  2. List issues in ready_state for team. For each issue not yet
//     present on disk (no tasks/<slug>.md carries `linear: <id>`),
//     create a tasks/<slug>.md and push the issue ready->in-progress.
//  3. Walk tasks/. For every status=done task that carries
//     `linear: <id>` and is missing `linear_done: yes`, push the issue
//     to done_state and stamp `linear_done: yes`.
//
// All HTTP traffic flows through a single endpoint and a single
// Authorization header so a test can swap in an httptest server.
package linear

import (
	"bytes"
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

const sourceName = "linear"

// HTTPDoer is satisfied by *http.Client; tests can swap a stub.
type HTTPDoer interface {
	Do(*http.Request) (*http.Response, error)
}

// Source is the Linear adapter. Construct with New.
type Source struct {
	cfg    matter.LinearConfig
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

// New builds a Linear Source from a parsed matter.LinearConfig. The
// API key is resolved eagerly so a misconfiguration fails the
// reconcile pass loudly rather than at the first GraphQL call.
func New(cfg matter.LinearConfig, opts ...Option) (*Source, error) {
	key, err := cfg.ResolveAPIKey()
	if err != nil {
		return nil, err
	}
	s := &Source{
		cfg:    cfg,
		apiKey: key,
		client: &http.Client{Timeout: 30 * time.Second},
	}
	for _, opt := range opts {
		opt(s)
	}
	return s, nil
}

// Name returns "linear".
func (*Source) Name() string { return sourceName }

// Sync runs one full pass. See package doc for the sequence.
func (s *Source) Sync(projectRoot, tasksDir string) (matter.Stats, error) {
	stats := matter.Stats{Source: sourceName}
	absTasks := tasksDir
	if !filepath.IsAbs(absTasks) {
		absTasks = filepath.Join(projectRoot, tasksDir)
	}
	if err := os.MkdirAll(absTasks, 0o755); err != nil {
		return stats, err
	}

	if err := s.loadStateIDs(); err != nil {
		return stats, err
	}
	readyID, ok := s.stateIDs[s.cfg.ReadyState]
	if !ok {
		return stats, fmt.Errorf("matter.linear: ready_state %q not found in team %s", s.cfg.ReadyState, s.cfg.Team)
	}
	inProgressID, ok := s.stateIDs[s.cfg.InProgressState]
	if !ok {
		return stats, fmt.Errorf("matter.linear: in_progress_state %q not found in team %s", s.cfg.InProgressState, s.cfg.Team)
	}
	doneID, ok := s.stateIDs[s.cfg.DoneState]
	if !ok {
		return stats, fmt.Errorf("matter.linear: done_state %q not found in team %s", s.cfg.DoneState, s.cfg.Team)
	}

	known, err := indexLinearTasks(absTasks)
	if err != nil {
		return stats, err
	}

	ready, err := s.listIssuesByState(readyID)
	if err != nil {
		return stats, err
	}
	for _, issue := range ready {
		if _, dup := known[issue.Identifier]; dup {
			continue
		}
		slug, err := s.adoptIssue(absTasks, issue)
		if err != nil {
			return stats, fmt.Errorf("matter.linear: adopt %s: %w", issue.Identifier, err)
		}
		if err := s.transitionIssue(issue.ID, inProgressID); err != nil {
			return stats, fmt.Errorf("matter.linear: transition %s -> in_progress: %w", issue.Identifier, err)
		}
		stats.Created = append(stats.Created, slug)
		stats.AdoptedReady = append(stats.AdoptedReady, issue.Identifier)
		known[issue.Identifier] = slug
	}

	doneSlugs, err := pendingDonePushes(absTasks)
	if err != nil {
		return stats, err
	}
	for _, p := range doneSlugs {
		if err := s.transitionIssue(p.linearID, doneID); err != nil {
			return stats, fmt.Errorf("matter.linear: transition %s -> done: %w", p.identifier, err)
		}
		if err := stampLinearDone(absTasks, p.slug); err != nil {
			return stats, fmt.Errorf("matter.linear: stamp %s linear_done: %w", p.slug, err)
		}
		stats.PushedDone = append(stats.PushedDone, p.slug)
	}

	sort.Strings(stats.Created)
	sort.Strings(stats.AdoptedReady)
	sort.Strings(stats.PushedDone)
	return stats, nil
}

// adoptIssue writes a new tasks/<slug>.md for issue and returns the
// slug it allocated. Title-derived; collisions are resolved via
// task.Allocate. The Linear identifier (e.g. MAR-42) lands in the
// frontmatter `linear` extra so subsequent Sync passes know the issue
// is already on disk.
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
			"linear":     issue.Identifier,
			"linear_url": issue.URL,
		},
	}
	if issue.URL == "" {
		delete(m.Extra, "linear_url")
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
	linearID   string
}

// indexLinearTasks scans tasksDir and returns identifier -> slug for
// every tasks/<slug>.md that carries a `linear:` extra. Used to skip
// re-adopting issues already present on disk.
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
		id := m.Extra["linear"]
		if id == "" {
			continue
		}
		out[id] = m.Slug
	}
	return out, nil
}

// pendingDonePushes returns rows for tasks where status=done,
// linear is set, and linear_done is not yet stamped.
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
		ident := m.Extra["linear"]
		if ident == "" {
			continue
		}
		if m.Extra["linear_done"] == "yes" {
			continue
		}
		rows = append(rows, linearTaskRow{
			slug:       m.Slug,
			identifier: ident,
			linearID:   ident,
		})
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
	m.Extra["linear_done"] = "yes"
	return os.WriteFile(path, frontmatter.Write(m, body), 0o644)
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
