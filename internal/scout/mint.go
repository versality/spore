// Package scout implements the healer-task minter: it consumes the
// JSONL ledger produced by `spore scout`, clusters findings, and
// writes one tasks/<slug>.md brief per cluster via the frontmatter
// package.
package scout

import (
	"bufio"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/versality/spore/internal/task"
	"github.com/versality/spore/internal/task/frontmatter"
)

// Finding mirrors one JSONL row produced by `spore scout`.
type Finding struct {
	Ts          string `json:"ts"`
	Lint        string `json:"lint"`
	Severity    string `json:"severity"`
	Path        string `json:"path"`
	Line        int    `json:"line"`
	Message     string `json:"message"`
	Fingerprint string `json:"fingerprint"`
}

// Cluster groups findings that share a lint plus a coarse path key
// (the directory of Path, or "." for repo-root files). One healer
// task is minted per cluster.
type Cluster struct {
	Lint      string
	PathKey   string
	Findings  []Finding
	FirstSeen time.Time
}

// SchedulerKey is the idempotency identifier for the cluster. The
// mint step records it in scout-minted.tsv after a successful write
// so subsequent runs skip the cluster.
func (c Cluster) SchedulerKey() string {
	h := sha256.New()
	h.Write([]byte(c.Lint))
	h.Write([]byte{0})
	h.Write([]byte(c.PathKey))
	h.Write([]byte{0})
	for _, f := range c.Findings {
		h.Write([]byte(f.Fingerprint))
		h.Write([]byte{0})
	}
	return "scout:" + c.Lint + ":" + hex.EncodeToString(h.Sum(nil)[:6])
}

// Options drives Mint. Zero values for optional paths resolve to the
// XDG defaults so a one-flag CLI is enough.
type Options struct {
	LedgerPath   string
	TasksDir     string
	Project      string
	MintedPath   string
	FalseposPath string
	Max          int
	Now          func() time.Time
	DryRun       bool
}

// MintResult summarises a Mint call for the CLI layer.
type MintResult struct {
	Minted     []string
	Skipped    int
	FalsePos   int
	Capped     int
	Considered int
}

// Mint reads the ledger, clusters findings, and writes one task brief
// per new cluster up to opts.Max. Returns the minted slugs and a
// per-bucket summary.
func Mint(opts Options) (MintResult, error) {
	if opts.LedgerPath == "" {
		return MintResult{}, fmt.Errorf("scout mint: ledger path required")
	}
	if opts.TasksDir == "" {
		return MintResult{}, fmt.Errorf("scout mint: tasks dir required")
	}
	if opts.Project == "" {
		opts.Project = "spore"
	}
	if opts.Max <= 0 {
		opts.Max = 10
	}
	if opts.Now == nil {
		opts.Now = func() time.Time { return time.Now().UTC() }
	}

	findings, err := readLedger(opts.LedgerPath)
	if err != nil {
		return MintResult{}, err
	}

	falsepos, err := readSet(opts.FalseposPath)
	if err != nil {
		return MintResult{}, fmt.Errorf("read falsepos: %w", err)
	}
	minted, err := readSet(opts.MintedPath)
	if err != nil {
		return MintResult{}, fmt.Errorf("read minted: %w", err)
	}

	res := MintResult{Considered: len(findings)}
	var kept []Finding
	for _, f := range findings {
		if falsepos[f.Fingerprint] {
			res.FalsePos++
			continue
		}
		kept = append(kept, f)
	}

	clusters := groupByLintAndDir(kept)

	// Sort clusters by earliest-finding ts so re-runs mint
	// deterministically and oldest issues land first.
	sort.Slice(clusters, func(i, j int) bool {
		if !clusters[i].FirstSeen.Equal(clusters[j].FirstSeen) {
			return clusters[i].FirstSeen.Before(clusters[j].FirstSeen)
		}
		if clusters[i].Lint != clusters[j].Lint {
			return clusters[i].Lint < clusters[j].Lint
		}
		return clusters[i].PathKey < clusters[j].PathKey
	})

	for _, c := range clusters {
		key := c.SchedulerKey()
		if minted[key] {
			res.Skipped++
			continue
		}
		if len(res.Minted) >= opts.Max {
			res.Capped++
			continue
		}
		slug, err := writeBrief(opts, c, key)
		if err != nil {
			return res, err
		}
		res.Minted = append(res.Minted, slug)
		if !opts.DryRun {
			if err := appendSet(opts.MintedPath, key); err != nil {
				return res, fmt.Errorf("record minted: %w", err)
			}
		}
	}
	return res, nil
}

func readLedger(path string) ([]Finding, error) {
	f, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	defer f.Close()
	return decodeFindings(f)
}

func decodeFindings(r io.Reader) ([]Finding, error) {
	var out []Finding
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		var f Finding
		if err := json.Unmarshal([]byte(line), &f); err != nil {
			return nil, fmt.Errorf("ledger parse: %w (line %q)", err, line)
		}
		out = append(out, f)
	}
	return out, sc.Err()
}

func readSet(path string) (map[string]bool, error) {
	out := map[string]bool{}
	if path == "" {
		return out, nil
	}
	f, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return out, nil
		}
		return nil, err
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		// One token per line; tolerate inline comments after a tab/space.
		if i := strings.IndexAny(line, "\t "); i >= 0 {
			line = strings.TrimSpace(line[:i])
		}
		if line != "" {
			out[line] = true
		}
	}
	return out, sc.Err()
}

func appendSet(path, token string) error {
	if path == "" {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = fmt.Fprintln(f, token)
	return err
}

func groupByLintAndDir(findings []Finding) []Cluster {
	byKey := map[string]*Cluster{}
	for _, f := range findings {
		dir := filepath.Dir(f.Path)
		if dir == "" {
			dir = "."
		}
		k := f.Lint + "\x00" + dir
		c, ok := byKey[k]
		if !ok {
			c = &Cluster{Lint: f.Lint, PathKey: dir}
			byKey[k] = c
		}
		c.Findings = append(c.Findings, f)
		if t, err := time.Parse(time.RFC3339, f.Ts); err == nil {
			if c.FirstSeen.IsZero() || t.Before(c.FirstSeen) {
				c.FirstSeen = t
			}
		}
	}
	out := make([]Cluster, 0, len(byKey))
	for _, c := range byKey {
		// Deduplicate findings within a cluster by fingerprint so a
		// re-scanned ledger does not double-count.
		seen := map[string]bool{}
		var uniq []Finding
		for _, f := range c.Findings {
			if seen[f.Fingerprint] {
				continue
			}
			seen[f.Fingerprint] = true
			uniq = append(uniq, f)
		}
		sort.SliceStable(uniq, func(i, j int) bool {
			if uniq[i].Path != uniq[j].Path {
				return uniq[i].Path < uniq[j].Path
			}
			return uniq[i].Line < uniq[j].Line
		})
		c.Findings = uniq
		out = append(out, *c)
	}
	return out
}

func writeBrief(opts Options, c Cluster, key string) (string, error) {
	slugBase := task.Slugify(fmt.Sprintf("heal-%s-%s", c.Lint, shortKey(key)))
	if slugBase == "" {
		return "", fmt.Errorf("scout mint: empty slug for cluster %s", key)
	}
	if err := os.MkdirAll(opts.TasksDir, 0o755); err != nil {
		return "", err
	}
	slug, err := task.Allocate(opts.TasksDir, slugBase)
	if err != nil {
		return "", err
	}

	title := fmt.Sprintf("heal: %d %s finding(s) under %s", len(c.Findings), c.Lint, c.PathKey)
	body := briefBody(c, key, opts.FalseposPath)

	m := frontmatter.Meta{
		Status:   "draft",
		Slug:     slug,
		Title:    title,
		Created:  opts.Now().Format(time.RFC3339),
		Project:  opts.Project,
		Priority: "medium",
		Extra: map[string]string{
			"scheduler_key": key,
			"source":        "scout",
			"lint":          c.Lint,
		},
	}
	out := frontmatter.Write(m, []byte(body))
	if opts.DryRun {
		return slug, nil
	}
	path := filepath.Join(opts.TasksDir, slug+".md")
	if err := os.WriteFile(path, out, 0o644); err != nil {
		return "", err
	}
	return slug, nil
}

func shortKey(key string) string {
	// scheduler keys are "scout:<lint>:<12-hex>"; pull the hex.
	if i := strings.LastIndex(key, ":"); i >= 0 && i+1 < len(key) {
		return key[i+1:]
	}
	return key
}

func briefBody(c Cluster, key, falseposPath string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "\nAuto-minted by `spore scout mint-healers`.\n\n")
	fmt.Fprintf(&b, "## Findings (%d)\n\n", len(c.Findings))
	for _, f := range c.Findings {
		loc := f.Path
		if f.Line > 0 {
			loc = fmt.Sprintf("%s:%d", f.Path, f.Line)
		}
		fmt.Fprintf(&b, "- `%s` - %s\n", loc, f.Message)
	}
	fmt.Fprintf(&b, "\n## Fingerprints\n\n")
	for _, f := range c.Findings {
		fmt.Fprintf(&b, "- %s\n", f.Fingerprint)
	}
	fmt.Fprintf(&b, "\n## How to handle\n\n")
	fmt.Fprintf(&b, "Mechanical fix, one commit. Run `spore lint %s --root .` after\n", c.Lint)
	fmt.Fprintf(&b, "the edit; the affected lint must be clean.\n\n")
	fmt.Fprintf(&b, "If a finding is wrong (false positive), append its fingerprint to\n")
	if falseposPath != "" {
		fmt.Fprintf(&b, "`%s` and delete this brief without committing code.\n", falseposPath)
	} else {
		fmt.Fprintf(&b, "the scout-falsepos.tsv ledger and delete this brief without committing code.\n")
	}
	fmt.Fprintf(&b, "\nScheduler key: `%s`\n", key)
	return b.String()
}
