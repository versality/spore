// Package linear is the Linear adapter for the matter plug point.
//
// One Sync pass:
//  1. Resolve the workflow state IDs for the configured team (one
//     GraphQL call, cached in the Source between passes since state
//     IDs are stable).
//  2. List issues in ready_state for team (optionally narrowed to a
//     claim_label and/or an assignee). For each issue not yet present
//     on disk (no tasks/<slug>.md carries `matter_id: <id>`), create a
//     tasks/<slug>.md and push the issue ready->in-progress. When
//     max_concurrent is set, only enough issues to fill the free slots
//     (max_concurrent - currently active tasks) are adopted per pass,
//     in board order, so the In Progress flip never outruns the fleet's
//     spawn capacity.
//  3. Walk tasks/. For every status=done task that carries
//     `matter_id: <id>` and is missing `linear_done: yes`, push the
//     issue to done_state and stamp `linear_done: yes`. This is the
//     safety-net path; the synchronous push happens via OnDone the
//     moment the task flips to done.
//  4. Drift-correct In Progress. For every status=active task carrying
//     `matter_id: <id>` whose ticket is not already in in_progress_state,
//     push it there. Step 2's push is edge-triggered (fires once on
//     adoption) so it misses tickets adopted before that push existed, a
//     push that failed once, and human drag-backs to Ready; this pass
//     re-projects task status -> Linear state every run. Idempotent and
//     claim-bound; it never unclaims.
//
// The adapter registers itself under the name "linear" via init(),
// so importing this package is enough to make `[matter.linear]` (or
// SPORE_MATTER_LINEAR__*) wiring activate.
//
// Frontmatter convention. Tasks created by this adapter carry the
// generic matter keys (matter, matter_id, matter_url) plus the
// adapter-private `linear_done` stamp once the upstream Done push
// has succeeded.
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
	"strconv"
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

	// matterBlockerPrefix marks a blocker reason owned by the matter
	// adapter: when a Linear ticket leaves Ready, future projection
	// passes may stamp `blocker: matter:<state>` so the local task is
	// no longer runnable. The same prefix is what lets a later pass
	// edge-trigger a resume when the ticket returns to Ready. Blockers
	// without this prefix are operator-set and stay sticky.
	matterBlockerPrefix = "matter:"
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
	// ClaimLabel, when non-empty, restricts the Ready-state projection
	// to issues bearing this Linear label. Lets multiple spore hosts
	// share one upstream queue: each host filters on its own label,
	// while a consumer-side process binds tickets to hosts by setting
	// the label. Matter only reads the label; it never creates or
	// modifies labels.
	ClaimLabel string
	// AssigneeEmail, when non-empty, restricts the Ready-state
	// projection to issues assigned to this user (by email). It scopes
	// auto-pickup to one operator's queue without a per-ticket claim
	// label: the reconciler adopts only this assignee's Ready tickets
	// and leaves everyone else's alone. Composes with ClaimLabel (both
	// clauses apply). Matter only reads the assignment.
	AssigneeEmail string
	// MaxConcurrent, when > 0, caps how many NEW issues a single Sync
	// adopts so the In Progress flip tracks the fleet's spawn capacity
	// rather than running ahead of it. At most max(0, MaxConcurrent -
	// <currently active adopted tasks>) issues are adopted per pass, in
	// board order (lowest sortOrder first); the rest stay in Ready for a
	// later pass as slots free. 0 (the default) keeps the original
	// unbounded behaviour. Set it to the fleet's max_workers so the
	// queue never flips more tickets to In Progress than it can run.
	MaxConcurrent int
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

	// Capacity-bound new adoptions so the In Progress flip never runs
	// ahead of the fleet's spawn capacity. adoptBudget < 0 means
	// unbounded (MaxConcurrent unset); otherwise it is the number of
	// free slots = max(0, MaxConcurrent - currently-active adopted
	// tasks). ready is board-sorted, so the lowest-sortOrder issues fill
	// the slots first and the rest stay in Ready for a later pass.
	adoptBudget := -1
	if s.cfg.MaxConcurrent > 0 {
		activeRows, err := activeAdoptedTasks(tasksDir)
		if err != nil {
			return 0, 0, err
		}
		if adoptBudget = s.cfg.MaxConcurrent - len(activeRows); adoptBudget < 0 {
			adoptBudget = 0
		}
	}
	for _, issue := range ready {
		if slug, dup := known[issue.Identifier]; dup {
			resumed, err := resumeIfMatterBlocked(tasksDir, slug)
			if err != nil {
				return created, updated, fmt.Errorf("matter.linear: resume %s: %w", issue.Identifier, err)
			}
			if resumed {
				updated++
			}
			continue
		}
		if isBlockedByOpenUpstream(issue) {
			continue
		}
		if adoptBudget == 0 {
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
		if adoptBudget > 0 {
			adoptBudget--
		}
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

	// Drift-correct In Progress. The adoption push above is edge-triggered:
	// it fires once, the moment an issue is first pulled out of Ready. It
	// misses tickets adopted before that push existed, a push that failed
	// once, and human drag-backs to Ready. Re-project every pass from task
	// status (the local truth) to Linear: any active, matter-linked task
	// whose ticket is not already In Progress is pushed there. Claim-bound
	// (status=active only) and idempotent; we never unclaim, since a task
	// blocked on PR review must keep its ticket In Progress rather than
	// bounce back to Ready.
	activeRows, err := activeAdoptedTasks(tasksDir)
	if err != nil {
		return created, updated, err
	}
	if len(activeRows) > 0 {
		inProgress, err := s.identifiersInState(inProgressID)
		if err != nil {
			return created, updated, err
		}
		for _, r := range activeRows {
			if inProgress[r.identifier] {
				continue
			}
			if err := s.transitionIssue(r.identifier, inProgressID); err != nil {
				return created, updated, fmt.Errorf("matter.linear: reproject %s -> in_progress: %w", r.identifier, err)
			}
			updated++
		}
	}
	return created, updated, nil
}

// OnDone is the synchronous push for a task that just flipped to
// done. Sync's done-walk is the fallback when this hook misses (the
// reconciler was off, the adapter was down, the task was edited
// outside spore task done). The push is idempotent on Linear's side,
// so the next Sync re-pushing as a no-op is harmless.
func (s *Source) OnDone(ctx context.Context, slug string, meta map[string]string) error {
	id := linearIDFromMeta(meta)
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
		ClaimLabel:      c.Option("claim_label", ""),
		AssigneeEmail:   c.Option("assignee", ""),
	}
	maxConc, err := parseNonNegativeInt(c.Option("max_concurrent", ""), "max_concurrent")
	if err != nil {
		return cfg, err
	}
	cfg.MaxConcurrent = maxConc
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

// parseNonNegativeInt parses an optional integer config value. An empty
// string yields 0 (the "unset" default); a non-numeric or negative
// value is a configuration error surfaced loudly at construction.
func parseNonNegativeInt(raw, key string) (int, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return 0, nil
	}
	n, err := strconv.Atoi(raw)
	if err != nil {
		return 0, fmt.Errorf("matter.linear: %s must be an integer, got %q", key, raw)
	}
	if n < 0 {
		return 0, fmt.Errorf("matter.linear: %s must be >= 0, got %d", key, n)
	}
	return n, nil
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
			matter.MatterKey:          sourceName,
			matter.MatterIDKey:        issue.Identifier,
			matter.MatterSortOrderKey: strconv.FormatFloat(issue.SortOrder, 'g', -1, 64),
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

// activeAdoptedTasks returns rows for tasks with status=active that
// carry a Linear matter id. The drift-correction pass re-projects any
// whose upstream ticket is not already In Progress. Blocked tasks are
// excluded on purpose: a matter-set blocker means the ticket left Ready
// (re-flipping it would stomp that), and a PR-review block already left
// the ticket In Progress at adoption.
func activeAdoptedTasks(tasksDir string) ([]linearTaskRow, error) {
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
		if !task.IsActive(m.Status) {
			continue
		}
		id := linearIDFromMeta(m.Extra)
		if id == "" {
			continue
		}
		rows = append(rows, linearTaskRow{slug: m.Slug, identifier: id})
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].slug < rows[j].slug })
	return rows, nil
}

// identifiersInState returns the set of issue identifiers currently in
// stateID. Unlike listIssuesByState it never applies the claim label:
// the drift-correction pass needs the true In Progress membership, and
// an In Progress ticket is not required to still carry the Ready claim
// label.
func (s *Source) identifiersInState(stateID string) (map[string]bool, error) {
	const q = `query StateIssues($stateId: ID!) {
  issues(filter: {state: {id: {eq: $stateId}}}) { nodes { id identifier } }
}`
	var resp struct {
		Data struct {
			Issues struct {
				Nodes []struct {
					ID         string `json:"id"`
					Identifier string `json:"identifier"`
				} `json:"nodes"`
			} `json:"issues"`
		} `json:"data"`
	}
	if err := s.graphQL(q, map[string]any{"stateId": stateID}, &resp); err != nil {
		return nil, err
	}
	out := make(map[string]bool, len(resp.Data.Issues.Nodes))
	for _, n := range resp.Data.Issues.Nodes {
		out[n.Identifier] = true
	}
	return out, nil
}

// resumeIfMatterBlocked flips slug back to active when the local task
// is blocked with a matter-set blocker (one carrying matterBlockerPrefix).
// Operator-set blockers (no prefix) are left untouched: matter must not
// stomp a manual block reason. Edge-triggered: only fires when the local
// task is in matter-owned blocked state, never resets an already-active
// task. Returns true when a flip happened.
func resumeIfMatterBlocked(tasksDir, slug string) (bool, error) {
	path := filepath.Join(tasksDir, slug+".md")
	raw, err := os.ReadFile(path)
	if err != nil {
		return false, err
	}
	m, body, err := frontmatter.Parse(raw)
	if err != nil {
		return false, err
	}
	if !task.IsBlocked(m.Status) {
		return false, nil
	}
	if !strings.HasPrefix(m.Extra["blocker"], matterBlockerPrefix) {
		return false, nil
	}
	m.Status = task.StatusActive
	delete(m.Extra, "blocker")
	return true, os.WriteFile(path, frontmatter.Write(m, body), 0o644)
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
// task's frontmatter matter_id slot. Returns "" when the task has no
// Linear linkage or names a different matter.
func linearIDFromMeta(extra map[string]string) string {
	if extra == nil {
		return ""
	}
	if name := extra[matter.MatterKey]; name != "" && name != sourceName {
		return ""
	}
	return extra[matter.MatterIDKey]
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
	ID          string          `json:"id"`
	Identifier  string          `json:"identifier"`
	Title       string          `json:"title"`
	Description string          `json:"description"`
	URL         string          `json:"url"`
	SortOrder   float64         `json:"sortOrder"`
	Relations   linearRelations `json:"relations"`
}

type linearRelations struct {
	Nodes []linearRelation `json:"nodes"`
}

type linearRelation struct {
	Type         string             `json:"type"`
	RelatedIssue linearRelatedIssue `json:"relatedIssue"`
}

type linearRelatedIssue struct {
	ID    string                  `json:"id"`
	State linearRelatedIssueState `json:"state"`
}

type linearRelatedIssueState struct {
	Type string `json:"type"`
}

// isBlockedByOpenUpstream returns true when issue has at least one
// blocked_by relation pointing to an upstream issue whose state.type
// is not terminal. Linear's state.type enum tags terminal states
// "completed" and "canceled"; the workflow-state name is
// operator-customisable and not safe to key on.
func isBlockedByOpenUpstream(issue linearIssue) bool {
	for _, rel := range issue.Relations.Nodes {
		if rel.Type != "blocked_by" {
			continue
		}
		switch rel.RelatedIssue.State.Type {
		case "completed", "canceled":
			continue
		default:
			return true
		}
	}
	return false
}

// listIssuesByState returns the issues in stateID ordered by Linear's
// kanban sortOrder (ascending; lower values appear higher in the
// column). The GraphQL `issues` query has no manual-order knob - its
// `orderBy` only takes createdAt/updatedAt - so we fetch sortOrder and
// sort client-side. Operators expect "drag a ticket to the top of
// Ready, get it picked next"; relying on default ordering would honour
// createdAt instead.
func (s *Source) listIssuesByState(stateID string) ([]linearIssue, error) {
	// Build the filter from optional clauses so claim_label and assignee
	// compose: each adds its own variable declaration and filter
	// fragment, and an unset filter leaves no trace in the query (the
	// back-compat path is byte-for-byte the bare state filter).
	decls := []string{"$stateId: ID!"}
	filters := []string{"state: {id: {eq: $stateId}}"}
	vars := map[string]any{"stateId": stateID}
	if s.cfg.ClaimLabel != "" {
		decls = append(decls, "$label: String!")
		filters = append(filters, "labels: {some: {name: {eq: $label}}}")
		vars["label"] = s.cfg.ClaimLabel
	}
	if s.cfg.AssigneeEmail != "" {
		decls = append(decls, "$assignee: String!")
		filters = append(filters, "assignee: {email: {eq: $assignee}}")
		vars["assignee"] = s.cfg.AssigneeEmail
	}
	q := fmt.Sprintf(`query StateIssues(%s) {
  issues(filter: {%s}) {
    nodes {
      id identifier title description url sortOrder
      relations {
        nodes {
          type
          relatedIssue { id state { type } }
        }
      }
    }
  }
}`, strings.Join(decls, ", "), strings.Join(filters, ", "))
	var resp struct {
		Data struct {
			Issues struct {
				Nodes []linearIssue `json:"nodes"`
			} `json:"issues"`
		} `json:"data"`
	}
	if err := s.graphQL(q, vars, &resp); err != nil {
		return nil, err
	}
	nodes := resp.Data.Issues.Nodes
	sort.SliceStable(nodes, func(i, j int) bool {
		if nodes[i].SortOrder != nodes[j].SortOrder {
			return nodes[i].SortOrder < nodes[j].SortOrder
		}
		return nodes[i].Identifier < nodes[j].Identifier
	})
	return nodes, nil
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
