// Package prfinish implements M-finish-C of the rower ship-cycle
// contract (tasks/spore-rower-finish-contract.md section 5): a Stop
// hook that fires exit 2 when a rower idles on a wt/<slug> branch
// whose pushed PR needs a deterministic next action.
//
// Decision boundary, evaluated only after the universal idle / clean /
// no-mid-merge gates pass:
//
//   - no PR for wt/<slug>:                exit 0 (rower has not pushed yet).
//   - state=OPEN, mergeable=CONFLICTING:  exit 2 with a rebase prompt.
//   - state=OPEN, any check FAILURE:      exit 2 with the failing job names.
//   - state=OPEN, any check IN_PROGRESS:  exit 0 (CI still running; wait).
//   - state=OPEN, mergeable=MERGEABLE,
//     all checks SUCCESS:                 exit 2 with a `gh pr merge` prompt.
//   - state=OPEN, mergeable=UNKNOWN:      exit 0 (gh has not computed; wait).
//   - state=MERGED:                       exit 0 (M-finish-D consumer-claim
//     scan will fire on the next stop).
//   - state=CLOSED (not merged):          exit 0 (rower closed the PR; no
//     further automatic action).
//
// I9 covers the mergeable + green case; I10 covers the CI-red case.
// Both share the same gh dependency behind the GHClient seam.
package prfinish

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/versality/spore/internal/agentpane"
	"github.com/versality/spore/internal/gh"
	"github.com/versality/spore/internal/hooks"
	"github.com/versality/spore/internal/task"
	"github.com/versality/spore/internal/task/consumerclaim"
	"github.com/versality/spore/internal/task/frontmatter"
)

// Result is the hook's verdict.
type Result struct {
	ExitCode int
	Stderr   string
}

// PRState and CheckRun aliases keep test fixtures and decision-logic
// signatures stable now that the wire shape lives in internal/gh.
type (
	PRState  = gh.PRState
	CheckRun = gh.CheckRun
)

// GHClient is the test seam this hook needs. The shared internal/gh
// package's Real satisfies it; ship's wider interface is a superset
// and lives there.
type GHClient interface {
	ViewPR(projectRoot, branch string) (state PRState, found bool, err error)
}

// Deps are the test seams. Nil fields fall back to real implementations.
type Deps struct {
	LookupEnv   func(string) (string, bool)
	Capture     agentpane.CaptureFunc
	SessionName func(projectRoot, slug string) string
	GH          GHClient
	// Consumer carries the consumerclaim scanner seams. Zero value uses
	// real implementations.
	Consumer consumerclaim.Deps
	// ReadTaskMeta loads tasks/<slug>.md frontmatter. Defaults to a
	// real read from projectRoot/tasks/<slug>.md.
	ReadTaskMeta func(projectRoot, slug string) (frontmatter.Meta, error)
}

// Run evaluates the decision boundary and returns the Result.
func Run(req hooks.Request, deps Deps) Result {
	deps = deps.withDefaults()

	slug, ok := deps.LookupEnv("SPORE_TASK_SLUG")
	if !ok || slug == "" {
		return Result{}
	}

	projectRoot, _ := deps.LookupEnv("SPORE_PROJECT_ROOT")
	if projectRoot == "" {
		root, err := topLevel(req.CWD)
		if err != nil {
			return Result{}
		}
		if main, ok := mainCheckoutFromWorktree(root); ok {
			projectRoot = main
		} else {
			projectRoot = root
		}
	}
	worktree := req.CWD
	if worktree == "" {
		worktree = filepath.Join(projectRoot, ".worktrees", slug)
	}

	if !workingTreeClean(worktree) {
		return Result{}
	}
	if midMergeOrRebase(worktree) {
		return Result{}
	}

	session := deps.SessionName(projectRoot, slug)
	if session == "" {
		return Result{}
	}
	state, _ := agentpane.Classify(deps.Capture, session+":claude", "claude")
	if state != "idle" {
		return Result{}
	}

	branch := "wt/" + slug
	pr, found, err := deps.GH.ViewPR(projectRoot, branch)
	if err != nil || !found {
		// gh failures or no-PR both degrade to silent exit 0; the rower
		// will hit pushpending or wtmerge-mechanical first if work is
		// truly unshipped.
		return Result{}
	}

	switch pr.State {
	case "MERGED":
		return decideMerged(projectRoot, slug, pr, deps)
	case "CLOSED":
		return Result{}
	case "OPEN":
		return decideOpen(pr, branch)
	}
	return Result{}
}

func decideMerged(projectRoot, slug string, pr PRState, deps Deps) Result {
	m, err := deps.ReadTaskMeta(projectRoot, slug)
	if err != nil {
		// No task file or unreadable: nothing to scrub; let the rower flip done.
		return Result{}
	}
	if len(m.ConsumerClaims) == 0 {
		return Result{}
	}
	var claims []consumerclaim.Claim
	for _, raw := range m.ConsumerClaims {
		c, err := consumerclaim.ParseClaim(raw)
		if err != nil {
			// Malformed claim; the I11 gate at done time will surface it.
			// Skip here so the hook does not double-prompt on parse errors.
			continue
		}
		claims = append(claims, c)
	}
	if len(claims) == 0 {
		return Result{}
	}
	results := consumerclaim.Scan(claims, deps.Consumer)
	if !consumerclaim.AnyUnresolved(results) {
		return Result{}
	}
	var b strings.Builder
	stale := 0
	for _, r := range results {
		if r.Status != consumerclaim.StatusResolved {
			stale++
		}
	}
	fmt.Fprintf(&b, "PR #%d is merged but %d consumer-claim(s) still unresolved and you have stopped. Scrub the consumer or mint `spore task cutover` per claim:\n", pr.Number, stale)
	for _, r := range results {
		if r.Status == consumerclaim.StatusResolved {
			continue
		}
		fmt.Fprintf(&b, "  - %s:%s:%s [%s] %s\n", r.Claim.Repo, r.Claim.Kind, r.Claim.Value, r.Status, r.Detail)
	}
	return Result{ExitCode: 2, Stderr: b.String()}
}

func decideOpen(pr PRState, branch string) Result {
	if pr.Mergeable == "CONFLICTING" {
		msg := fmt.Sprintf(
			"PR #%d on %s has merge conflicts and you have stopped. Rebase: `git fetch origin && git rebase origin/main`, then `git push --force-with-lease`.\n",
			pr.Number, branch)
		return Result{ExitCode: 2, Stderr: msg}
	}

	var failed []CheckRun
	hasPending := false
	for _, c := range pr.Checks {
		switch strings.ToUpper(c.Conclusion) {
		case "FAILURE", "CANCELLED", "TIMED_OUT", "ACTION_REQUIRED":
			failed = append(failed, c)
		case "SUCCESS", "SKIPPED", "NEUTRAL":
			// fine
		default:
			// No conclusion yet; defer to status.
			if strings.ToUpper(c.Status) != "COMPLETED" {
				hasPending = true
			}
		}
	}

	if len(failed) > 0 {
		var b strings.Builder
		fmt.Fprintf(&b, "PR #%d on %s has %d failing check(s) and you have stopped. Fix and push:\n", pr.Number, branch, len(failed))
		for _, c := range failed {
			if c.URL != "" {
				fmt.Fprintf(&b, "  - %s (%s): %s\n", c.Name, strings.ToLower(c.Conclusion), c.URL)
			} else {
				fmt.Fprintf(&b, "  - %s (%s)\n", c.Name, strings.ToLower(c.Conclusion))
			}
		}
		return Result{ExitCode: 2, Stderr: b.String()}
	}

	if hasPending {
		return Result{}
	}

	if pr.Mergeable == "MERGEABLE" {
		msg := fmt.Sprintf(
			"PR #%d on %s is mergeable and green, and you have stopped. Run `gh pr merge %d --squash --delete-branch`.\n",
			pr.Number, branch, pr.Number)
		return Result{ExitCode: 2, Stderr: msg}
	}

	// UNKNOWN or any other state: gh has not computed yet; wait.
	return Result{}
}

func (d Deps) withDefaults() Deps {
	if d.LookupEnv == nil {
		d.LookupEnv = os.LookupEnv
	}
	if d.Capture == nil {
		d.Capture = agentpane.RealCapture
	}
	if d.SessionName == nil {
		d.SessionName = func(projectRoot, slug string) string {
			return task.TaskTmuxSession(filepath.Join(projectRoot, "tasks"), projectRoot, slug)
		}
	}
	if d.GH == nil {
		d.GH = gh.Real{}
	}
	if d.ReadTaskMeta == nil {
		d.ReadTaskMeta = readTaskMetaFromDisk
	}
	return d
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

func workingTreeClean(worktree string) bool {
	cmd := exec.Command("git", "-C", worktree, "status", "--porcelain")
	out, err := cmd.Output()
	if err != nil {
		return false
	}
	return strings.TrimSpace(string(out)) == ""
}

func midMergeOrRebase(worktree string) bool {
	gd, err := gitDir(worktree)
	if err != nil {
		return false
	}
	for _, name := range []string{"MERGE_HEAD", "rebase-merge", "rebase-apply"} {
		if _, err := os.Stat(filepath.Join(gd, name)); err == nil {
			return true
		}
	}
	return false
}

func gitDir(worktree string) (string, error) {
	out, err := exec.Command("git", "-C", worktree, "rev-parse", "--git-dir").Output()
	if err != nil {
		return "", err
	}
	dir := strings.TrimSpace(string(out))
	if !filepath.IsAbs(dir) {
		dir = filepath.Join(worktree, dir)
	}
	return dir, nil
}

func topLevel(dir string) (string, error) {
	if dir == "" {
		return "", fmt.Errorf("empty cwd")
	}
	out, err := exec.Command("git", "-C", dir, "rev-parse", "--show-toplevel").Output()
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}

func mainCheckoutFromWorktree(worktree string) (string, bool) {
	b, err := os.ReadFile(filepath.Join(worktree, ".git"))
	if err != nil {
		return "", false
	}
	line := strings.TrimSpace(string(b))
	const prefix = "gitdir: "
	if !strings.HasPrefix(line, prefix) {
		return "", false
	}
	gd := strings.TrimSpace(strings.TrimPrefix(line, prefix))
	parent := filepath.Dir(filepath.Dir(filepath.Dir(gd)))
	if parent == "" || parent == "/" {
		return "", false
	}
	return parent, true
}
