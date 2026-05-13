// Package cutover mints a draft task brief in a consumer repo asking
// it to catch up to a spore lift. Peer of internal/task/ship per
// tasks/spore-rower-finish-contract.md section 6b.
//
// One Mint per unresolved consumer-claim. The minted task lives at
// <consumer-root>/tasks/consume-<source>-<feature>.md (or
// tasks/consume-<feature>.md when source is empty) and carries the
// origin pointer back to the spore task plus the verbatim claim
// expression. Status is draft; the consumer's coordinator picks it up
// on its normal schedule.
//
// Mint is idempotent: re-running with the same slug returns the
// existing slug+path with skipped=true. No edits to an existing
// brief; the operator owns its body.
package cutover

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/versality/spore/internal/task/consumerclaim"
	"github.com/versality/spore/internal/task/frontmatter"
)

// Options control one mint.
type Options struct {
	Consumer   string // consumer repo name (resolves via consumerclaim.ResolveRepoPath)
	Feature    string // feature name; typically the originating spore slug
	SourceRepo string // origin repo name (e.g., "spore"); optional
	SourceSlug string // origin task slug; optional
	SourcePR   int    // origin PR number; 0 to omit
	Claim      string // raw claim expression like "nix-config:path:foo.sh"
	Reason     string // one-line operator-facing justification
}

// Deps are the test seams.
type Deps struct {
	Consumer  consumerclaim.Deps
	WriteFile func(path string, data []byte, perm fs.FileMode) error
	Stat      func(string) (os.FileInfo, error)
	MkdirAll  func(string, fs.FileMode) error
	Now       func() time.Time
}

// Result describes a mint outcome.
type Result struct {
	Slug    string
	Path    string
	Skipped bool // true when a task with the same slug already exists
}

// Mint writes the cutover task brief. Returns Result describing the
// slug, the absolute path of the brief, and whether an existing brief
// was preserved.
func Mint(opts Options, deps Deps) (Result, error) {
	deps = deps.withDefaults()
	if opts.Consumer == "" {
		return Result{}, fmt.Errorf("cutover: consumer repo required")
	}
	if opts.Feature == "" {
		return Result{}, fmt.Errorf("cutover: feature required")
	}

	repoPath, err := consumerclaim.ResolveRepoPath(opts.Consumer, deps.Consumer)
	if err != nil {
		return Result{}, fmt.Errorf("cutover: resolve %s: %w", opts.Consumer, err)
	}
	if _, err := deps.Stat(repoPath); err != nil {
		return Result{}, fmt.Errorf("cutover: consumer checkout absent at %s: %w", repoPath, err)
	}

	slug := buildSlug(opts.SourceRepo, opts.Feature)
	tasksDir := filepath.Join(repoPath, "tasks")
	if err := deps.MkdirAll(tasksDir, 0o755); err != nil {
		return Result{}, fmt.Errorf("cutover: mkdir %s: %w", tasksDir, err)
	}
	path := filepath.Join(tasksDir, slug+".md")
	if _, err := deps.Stat(path); err == nil {
		return Result{Slug: slug, Path: path, Skipped: true}, nil
	}

	title := buildTitle(opts.SourceRepo, opts.Feature, opts.Claim)
	extra := map[string]string{}
	if opts.SourceRepo != "" {
		extra["source-repo"] = opts.SourceRepo
	}
	if opts.SourceSlug != "" {
		extra["source-slug"] = opts.SourceSlug
	}
	if opts.SourcePR != 0 {
		extra["source-pr"] = strconv.Itoa(opts.SourcePR)
	}
	if opts.Claim != "" {
		extra["claim"] = opts.Claim
	}
	if opts.Reason != "" {
		extra["reason"] = opts.Reason
	}

	meta := frontmatter.Meta{
		Status:   "draft",
		Slug:     slug,
		Title:    title,
		Created:  deps.Now().UTC().Format(time.RFC3339),
		Project:  opts.Consumer,
		Priority: "medium",
		Extra:    extra,
	}
	body := buildBody(opts)
	out := frontmatter.Write(meta, body)
	if err := deps.WriteFile(path, out, 0o644); err != nil {
		return Result{}, fmt.Errorf("cutover: write %s: %w", path, err)
	}
	return Result{Slug: slug, Path: path}, nil
}

func buildSlug(sourceRepo, feature string) string {
	if sourceRepo == "" {
		return "consume-" + feature
	}
	return "consume-" + sourceRepo + "-" + feature
}

func buildTitle(sourceRepo, feature, claim string) string {
	var b strings.Builder
	b.WriteString("consume ")
	if sourceRepo != "" {
		b.WriteString(sourceRepo)
		b.WriteString(" ")
	}
	b.WriteString(feature)
	if claim != "" {
		b.WriteString(": ")
		b.WriteString(claim)
	}
	return b.String()
}

func buildBody(opts Options) []byte {
	var b strings.Builder
	b.WriteString("\n")
	b.WriteString("**Done line**\n\n")
	b.WriteString("Scrub the consumer reference obsoleted by the upstream lift, then ship.\n\n")
	if opts.SourceRepo != "" || opts.SourceSlug != "" {
		b.WriteString("## Origin\n\n")
		if opts.SourceRepo != "" {
			fmt.Fprintf(&b, "- repo: %s\n", opts.SourceRepo)
		}
		if opts.SourceSlug != "" {
			fmt.Fprintf(&b, "- slug: %s\n", opts.SourceSlug)
		}
		if opts.SourcePR != 0 {
			fmt.Fprintf(&b, "- PR: #%d\n", opts.SourcePR)
		}
		b.WriteString("\n")
	}
	if opts.Claim != "" {
		fmt.Fprintf(&b, "## Claim\n\n```\n%s\n```\n\n", opts.Claim)
	}
	if opts.Reason != "" {
		fmt.Fprintf(&b, "## Reason\n\n%s\n", opts.Reason)
	}
	return []byte(b.String())
}

func (d Deps) withDefaults() Deps {
	if d.WriteFile == nil {
		d.WriteFile = os.WriteFile
	}
	if d.Stat == nil {
		d.Stat = os.Stat
	}
	if d.MkdirAll == nil {
		d.MkdirAll = os.MkdirAll
	}
	if d.Now == nil {
		d.Now = time.Now
	}
	return d
}
