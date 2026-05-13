// Package ship implements the canonical streamlined `spore task ship`
// verb described in tasks/spore-rower-finish-contract.md section 6.
//
// One Go entry point per rower-side tool call: just check -> push
// wt/<slug> -> gh pr create -> wait for checks -> gh pr merge --squash
// --delete-branch -> fetch + ff local main -> task.Done (which runs
// the I11 consumer-claim gate plus the standard inbox / evidence
// gates and the worktree / branch / tmux cleanup).
//
// Each external dependency is a seam on Deps so tests run with fakes:
// GH (gh CLI), Git (raw git invocations on projectRoot), RunJustCheck
// (the preflight gate), Sleep (poll cadence), Done (the macro flip).
// Run is idempotent on a per-step basis: re-running ship after a
// network blip picks up wherever the previous attempt stopped.
package ship

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/versality/spore/internal/gh"
	"github.com/versality/spore/internal/task"
	"github.com/versality/spore/internal/task/consumerclaim"
	"github.com/versality/spore/internal/task/cutover"
	"github.com/versality/spore/internal/task/frontmatter"
)

const (
	DefaultPollInterval = 30 * time.Second
	DefaultMaxPolls     = 60
	DefaultStrategy     = "squash"
	DefaultBase         = "main"
)

// GHClient is the narrow gh surface ship needs. gh.Real satisfies it.
type GHClient interface {
	ViewPR(projectRoot, branch string) (gh.PRState, bool, error)
	CreatePR(projectRoot, branch, base, title, body string) (int, error)
	MergePR(projectRoot string, number int, strategy string, deleteBranch bool) error
}

// Deps are test seams. Zero values fall back to real implementations.
type Deps struct {
	GH           GHClient
	Git          func(projectRoot string, args ...string) ([]byte, error)
	RunJustCheck func(worktree string) error
	Sleep        func(time.Duration)
	Done         func(tasksDir, slug string) error

	// ReadTaskMeta loads tasks/<slug>.md frontmatter; default reads
	// from <projectRoot>/tasks/<slug>.md.
	ReadTaskMeta func(projectRoot, slug string) (frontmatter.Meta, error)
	// ConsumerScan defaults to consumerclaim.Scan with real deps.
	ConsumerScan func([]consumerclaim.Claim) []consumerclaim.Result
	// MintCutover defaults to cutover.Mint with real deps.
	MintCutover func(cutover.Options) (cutover.Result, error)
	// ProjectName resolves the source repo name for cutover origin
	// metadata; defaults to task.ProjectName(projectRoot).
	ProjectName func(projectRoot string) (string, error)

	PollInterval time.Duration
	MaxPolls     int

	Out, ErrOut io.Writer
}

// Options control the ship run.
type Options struct {
	TasksDir string
	Slug     string
	Strategy string // squash|merge|rebase; default squash
	Base     string // base branch; default main
}

// Run executes the full ship cycle. Returns nil on a clean ship-and-Done;
// any step's failure aborts and is returned verbatim so the caller (CLI,
// future ship-hook recovery) can surface it.
func Run(opts Options, deps Deps) error {
	deps = deps.withDefaults()
	if opts.Strategy == "" {
		opts.Strategy = DefaultStrategy
	}
	if opts.Base == "" {
		opts.Base = DefaultBase
	}
	if opts.Slug == "" {
		return fmt.Errorf("ship: slug required")
	}
	if opts.TasksDir == "" {
		return fmt.Errorf("ship: tasksDir required")
	}

	projectRoot, err := projectRootFromTasksDir(opts.TasksDir)
	if err != nil {
		return err
	}
	branch := "wt/" + opts.Slug
	worktree := filepath.Join(projectRoot, ".worktrees", opts.Slug)

	fmt.Fprintln(deps.Out, "ship: running just check")
	if err := deps.RunJustCheck(worktree); err != nil {
		return fmt.Errorf("ship: preflight just check failed: %w", err)
	}

	fmt.Fprintf(deps.Out, "ship: pushing %s to origin\n", branch)
	if out, err := deps.Git(projectRoot, "push", "origin", branch); err != nil {
		return fmt.Errorf("ship: push %s: %w: %s", branch, err, strings.TrimSpace(string(out)))
	}

	fmt.Fprintln(deps.Out, "ship: ensuring PR exists")
	n, err := deps.GH.CreatePR(projectRoot, branch, opts.Base, "", "")
	if err != nil {
		return fmt.Errorf("ship: create PR: %w", err)
	}
	fmt.Fprintf(deps.Out, "ship: PR #%d\n", n)

	state, err := waitChecks(deps, projectRoot, branch)
	if err != nil {
		return err
	}

	if state.State == "MERGED" {
		fmt.Fprintf(deps.Out, "ship: PR #%d already merged; skipping merge\n", state.Number)
	} else {
		fmt.Fprintf(deps.Out, "ship: merging PR #%d (%s)\n", n, opts.Strategy)
		if err := deps.GH.MergePR(projectRoot, n, opts.Strategy, true); err != nil {
			return fmt.Errorf("ship: merge PR: %w", err)
		}
	}

	fmt.Fprintln(deps.Out, "ship: advancing local main")
	if out, err := deps.Git(projectRoot, "fetch", "origin", opts.Base); err != nil {
		return fmt.Errorf("ship: fetch origin %s: %w: %s", opts.Base, err, strings.TrimSpace(string(out)))
	}
	if out, err := deps.Git(projectRoot, "merge", "--ff-only", "origin/"+opts.Base); err != nil {
		return fmt.Errorf("ship: ff %s: %w: %s", opts.Base, err, strings.TrimSpace(string(out)))
	}

	// Squash-merge means the original commits on wt/<slug> are not
	// reachable from the squashed commit on main. Delete the local
	// branch before task.Done so its UnmergedCommits gate (I2) sees
	// the branch gone and returns 0. -D (force) is correct here: the
	// work HAS shipped; the topology mismatch is a squash artefact.
	if out, err := deps.Git(projectRoot, "branch", "-D", branch); err != nil {
		// Branch may already be gone (re-run, or never created locally).
		// We do not surface this; task.Done's gate will pick up any real
		// problem.
		_ = out
	}

	if err := scanAndMintCutovers(deps, projectRoot, opts.Slug, n); err != nil {
		return err
	}

	fmt.Fprintln(deps.Out, "ship: flipping task done")
	if err := deps.Done(opts.TasksDir, opts.Slug); err != nil {
		return fmt.Errorf("ship: task done: %w", err)
	}
	fmt.Fprintln(deps.Out, "ship: done")
	return nil
}

// waitChecks polls gh until the PR is either mergeable+green, MERGED,
// CLOSED, conflicting, or has a failed check. Pending checks loop with
// deps.Sleep(deps.PollInterval) up to deps.MaxPolls iterations.
func waitChecks(deps Deps, projectRoot, branch string) (gh.PRState, error) {
	for i := 0; i < deps.MaxPolls; i++ {
		pr, found, err := deps.GH.ViewPR(projectRoot, branch)
		if err != nil {
			return gh.PRState{}, fmt.Errorf("ship: view PR: %w", err)
		}
		if !found {
			return gh.PRState{}, fmt.Errorf("ship: no PR found for %s", branch)
		}

		if pr.State == "MERGED" || pr.State == "CLOSED" {
			return pr, nil
		}

		var failed []gh.CheckRun
		pending := false
		for _, c := range pr.Checks {
			switch strings.ToUpper(c.Conclusion) {
			case "FAILURE", "CANCELLED", "TIMED_OUT", "ACTION_REQUIRED":
				failed = append(failed, c)
			case "SUCCESS", "SKIPPED", "NEUTRAL":
			default:
				if strings.ToUpper(c.Status) != "COMPLETED" {
					pending = true
				}
			}
		}
		if len(failed) > 0 {
			var b strings.Builder
			fmt.Fprintf(&b, "ship: PR #%d has %d failing check(s):\n", pr.Number, len(failed))
			for _, c := range failed {
				if c.URL != "" {
					fmt.Fprintf(&b, "  - %s (%s): %s\n", c.Name, strings.ToLower(c.Conclusion), c.URL)
				} else {
					fmt.Fprintf(&b, "  - %s (%s)\n", c.Name, strings.ToLower(c.Conclusion))
				}
			}
			return pr, fmt.Errorf("%s", strings.TrimRight(b.String(), "\n"))
		}
		if pr.Mergeable == "CONFLICTING" {
			return pr, fmt.Errorf("ship: PR #%d has merge conflicts; rebase wt/<slug> on origin/main and re-run ship", pr.Number)
		}
		if !pending && pr.Mergeable == "MERGEABLE" {
			return pr, nil
		}
		fmt.Fprintf(deps.Out, "ship: PR #%d not ready (mergeable=%s, pending=%v); waiting %s (poll %d/%d)\n",
			pr.Number, pr.Mergeable, pending, deps.PollInterval, i+1, deps.MaxPolls)
		deps.Sleep(deps.PollInterval)
	}
	return gh.PRState{}, fmt.Errorf("ship: timed out waiting for PR checks after %d polls", deps.MaxPolls)
}

func (d Deps) withDefaults() Deps {
	if d.GH == nil {
		d.GH = gh.Real{}
	}
	if d.Git == nil {
		d.Git = realGit
	}
	if d.RunJustCheck == nil {
		d.RunJustCheck = realRunJustCheck
	}
	if d.Sleep == nil {
		d.Sleep = time.Sleep
	}
	if d.Done == nil {
		d.Done = func(tasksDir, slug string) error { return task.Done(tasksDir, slug, false) }
	}
	if d.ReadTaskMeta == nil {
		d.ReadTaskMeta = readTaskMetaFromDisk
	}
	if d.ConsumerScan == nil {
		d.ConsumerScan = func(claims []consumerclaim.Claim) []consumerclaim.Result {
			return consumerclaim.Scan(claims, consumerclaim.Deps{})
		}
	}
	if d.MintCutover == nil {
		d.MintCutover = func(opts cutover.Options) (cutover.Result, error) {
			return cutover.Mint(opts, cutover.Deps{})
		}
	}
	if d.ProjectName == nil {
		d.ProjectName = task.ProjectName
	}
	if d.PollInterval <= 0 {
		d.PollInterval = DefaultPollInterval
	}
	if d.MaxPolls <= 0 {
		d.MaxPolls = DefaultMaxPolls
	}
	if d.Out == nil {
		d.Out = os.Stderr
	}
	if d.ErrOut == nil {
		d.ErrOut = os.Stderr
	}
	return d
}

// scanAndMintCutovers reads the task's consumer-claims, scans each,
// and mints one cutover task per unresolved or skipped claim. Returns
// nil on success, even when claims remain unresolved: the I11 gate in
// task.Done is the source of truth for refusal, and surfaces the
// per-claim list. Only mint failures surface here.
func scanAndMintCutovers(deps Deps, projectRoot, slug string, prNumber int) error {
	m, err := deps.ReadTaskMeta(projectRoot, slug)
	if err != nil {
		// No task file or unreadable; nothing to scan. task.Done will
		// fail later if the file is genuinely needed.
		return nil
	}
	if len(m.ConsumerClaims) == 0 {
		return nil
	}
	var claims []consumerclaim.Claim
	for _, raw := range m.ConsumerClaims {
		c, perr := consumerclaim.ParseClaim(raw)
		if perr != nil {
			// Malformed claim: surface as a parse error; the I11 gate
			// will also catch it but ship's stderr makes the source
			// obvious.
			fmt.Fprintf(deps.ErrOut, "ship: skipping malformed claim %q: %v\n", raw, perr)
			continue
		}
		claims = append(claims, c)
	}
	if len(claims) == 0 {
		return nil
	}
	results := deps.ConsumerScan(claims)
	if !consumerclaim.AnyUnresolved(results) {
		fmt.Fprintln(deps.Out, "ship: consumer-claims all resolved")
		return nil
	}
	srcRepo, _ := deps.ProjectName(projectRoot)
	for _, r := range results {
		if r.Status == consumerclaim.StatusResolved {
			continue
		}
		raw := fmt.Sprintf("%s:%s:%s", r.Claim.Repo, r.Claim.Kind, r.Claim.Value)
		out, err := deps.MintCutover(cutover.Options{
			Consumer:   r.Claim.Repo,
			Feature:    slug,
			SourceRepo: srcRepo,
			SourceSlug: slug,
			SourcePR:   prNumber,
			Claim:      raw,
		})
		if err != nil {
			fmt.Fprintf(deps.ErrOut, "ship: cutover mint %s: %v\n", raw, err)
			continue
		}
		if out.Skipped {
			fmt.Fprintf(deps.Out, "ship: cutover %s (existing) for %s\n", out.Slug, raw)
		} else {
			fmt.Fprintf(deps.Out, "ship: minted cutover %s for %s -> %s\n", out.Slug, raw, out.Path)
		}
	}
	return nil
}

func readTaskMetaFromDisk(projectRoot, slug string) (frontmatter.Meta, error) {
	path := filepath.Join(projectRoot, "tasks", slug+".md")
	b, err := os.ReadFile(path)
	if err != nil {
		return frontmatter.Meta{}, err
	}
	m, _, err := frontmatter.Parse(b)
	return m, err
}

func realGit(projectRoot string, args ...string) ([]byte, error) {
	full := append([]string{"-C", projectRoot}, args...)
	return exec.Command("git", full...).CombinedOutput()
}

// realRunJustCheck runs `just check` in dir. Skips silently when there
// is no justfile or no `just` on PATH or no `check` recipe; errors on
// a real red.
func realRunJustCheck(dir string) error {
	if _, err := os.Stat(filepath.Join(dir, "justfile")); err != nil {
		return nil
	}
	if _, err := exec.LookPath("just"); err != nil {
		return nil
	}
	show := exec.Command("just", "--show", "check")
	show.Dir = dir
	if err := show.Run(); err != nil {
		return nil
	}
	cmd := exec.Command("just", "check")
	cmd.Dir = dir
	cmd.Stdout = os.Stderr
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

func projectRootFromTasksDir(tasksDir string) (string, error) {
	abs, err := filepath.Abs(tasksDir)
	if err != nil {
		return "", err
	}
	return filepath.Dir(abs), nil
}
