// Package prfinish implements M-finish-C of the worker ship-cycle
// contract (tasks/spore-worker-finish-contract.md section 5): a Stop
// hook that fires exit 2 when a worker idles on a wt/<slug> branch
// whose pushed PR (or pushed-direct-to-main commit) needs a
// deterministic next action.
//
// Decision boundary, evaluated only after the universal idle / clean /
// no-mid-merge gates pass:
//
//   - state=OPEN, mergeable=CONFLICTING:  exit 2 with a rebase prompt.
//   - state=OPEN, any check FAILURE:      exit 2 with the failing job names.
//   - state=OPEN, any check IN_PROGRESS:  exit 0 (CI still running; wait).
//   - state=OPEN, mergeable=MERGEABLE,
//     all checks SUCCESS:                 exit 2 with a `gh pr merge` prompt.
//   - state=OPEN, mergeable=UNKNOWN:      exit 0 (gh has not computed; wait).
//   - state=MERGED:                       exit 0 (M-finish-D consumer-claim
//     scan will fire on the next stop).
//   - state=CLOSED (not merged):          exit 0 (worker closed the PR; no
//     further automatic action).
//
// When `gh pr view wt/<slug>` finds no PR, the direct-push branch
// kicks in instead (see decideDirectPush): if the worktree's HEAD sha
// is reachable from origin/main, the worker has already merged + pushed
// without opening a PR, so the hook gates exit on GH Actions runs for
// that sha. Pre-push (sha not in origin/main) stays a silent exit 0
// because pushpending / wtmerge-mechanical already cover those states.
//
// I9 covers the mergeable + green case; I10 covers the CI-red case.
// Both share the same gh dependency behind the GHClient seam.
package prfinish

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/versality/spore/internal/agentpane"
	"github.com/versality/spore/internal/gh"
	"github.com/versality/spore/internal/hooks"
	"github.com/versality/spore/internal/hooks/wtgit"
	"github.com/versality/spore/internal/task"
	"github.com/versality/spore/internal/task/consumerclaim"
	"github.com/versality/spore/internal/task/frontmatter"
)

// Result is the hook's verdict.
type Result struct {
	ExitCode int
	Stderr   string
}

// PRState, CheckRun, and RunSummary aliases keep test fixtures and
// decision-logic signatures stable now that the wire shape lives in
// internal/gh.
type (
	PRState    = gh.PRState
	CheckRun   = gh.CheckRun
	RunSummary = gh.RunSummary
)

// GHClient is the test seam this hook needs. The shared internal/gh
// package's Real satisfies it; ship's wider interface is a superset
// and lives there.
type GHClient interface {
	ViewPR(projectRoot, branch string) (state PRState, found bool, err error)
	ListRunsForCommit(projectRoot, branch, sha string) ([]RunSummary, error)
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
	// HeadSHA returns the worktree's current HEAD sha. Defaults to
	// `git -C <worktree> rev-parse HEAD`.
	HeadSHA func(worktree string) (string, error)
	// IsAncestor reports whether sha is reachable from ref in
	// projectRoot. Defaults to `git -C <projectRoot> merge-base
	// --is-ancestor <sha> <ref>`.
	IsAncestor func(projectRoot, sha, ref string) (bool, error)
}

// Run evaluates the decision boundary and returns the Result.
func Run(req hooks.Request, deps Deps) Result {
	deps = deps.withDefaults()

	slug, ok := deps.LookupEnv("SPORE_TASK_SLUG")
	if !ok || slug == "" {
		return Result{}
	}

	projectRoot, worktree, ok := wtgit.IdleWorkerGate(req.CWD, wtgit.GateDeps{
		LookupEnv:   deps.LookupEnv,
		Capture:     deps.Capture,
		SessionName: deps.SessionName,
	})
	if !ok {
		return Result{}
	}

	branch := "wt/" + slug
	pr, found, err := deps.GH.ViewPR(projectRoot, branch)
	if err != nil {
		// gh failures degrade to silent exit 0; the worker will hit
		// pushpending or wtmerge-mechanical first if work is truly
		// unshipped.
		return Result{}
	}
	if !found {
		return decideDirectPush(projectRoot, worktree, deps)
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

func decideDirectPush(projectRoot, worktree string, deps Deps) Result {
	sha, err := deps.HeadSHA(worktree)
	if err != nil || sha == "" {
		return Result{}
	}
	inMain, err := deps.IsAncestor(projectRoot, sha, "origin/main")
	if err != nil || !inMain {
		// Sha not yet on origin/main: worker is pre-push (pushpending /
		// wtmerge-mechanical will fire instead) or has nothing to ship.
		return Result{}
	}

	runs, err := deps.GH.ListRunsForCommit(projectRoot, "main", sha)
	if err != nil {
		return Result{}
	}
	if len(runs) == 0 {
		// CI not scheduled yet; next Stop will re-check.
		return Result{}
	}

	short := sha
	if len(short) > 7 {
		short = short[:7]
	}

	var failed []RunSummary
	hasPending := false
	for _, r := range runs {
		if r.Status != "COMPLETED" {
			hasPending = true
			continue
		}
		switch r.Conclusion {
		case "SUCCESS", "SKIPPED", "NEUTRAL":
			// fine
		case "FAILURE", "CANCELLED", "TIMED_OUT", "ACTION_REQUIRED":
			failed = append(failed, r)
		default:
			// Unknown conclusion on a completed run: treat as failure
			// so the worker investigates rather than ships silently.
			failed = append(failed, r)
		}
	}

	if len(failed) > 0 {
		var b strings.Builder
		fmt.Fprintf(&b, "CI failed for %s on main and you have stopped. Fix and push:\n", short)
		for _, r := range failed {
			label := strings.ToLower(r.Conclusion)
			if label == "" {
				label = "failed"
			}
			if r.URL != "" {
				fmt.Fprintf(&b, "  - %s (%s): %s\n", r.Name, label, r.URL)
			} else {
				fmt.Fprintf(&b, "  - %s (%s)\n", r.Name, label)
			}
		}
		return Result{ExitCode: 2, Stderr: b.String()}
	}

	if hasPending {
		return Result{}
	}
	// All runs completed successfully: worker can stop cleanly.
	return Result{}
}

func decideMerged(projectRoot, slug string, pr PRState, deps Deps) Result {
	m, err := deps.ReadTaskMeta(projectRoot, slug)
	if err != nil {
		// No task file or unreadable: nothing to scrub; let the worker flip done.
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
	if d.HeadSHA == nil {
		d.HeadSHA = headSHAFromGit
	}
	if d.IsAncestor == nil {
		d.IsAncestor = isAncestorFromGit
	}
	return d
}

func headSHAFromGit(worktree string) (string, error) {
	out, err := exec.Command("git", "-C", worktree, "rev-parse", "HEAD").Output()
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}

func isAncestorFromGit(projectRoot, sha, ref string) (bool, error) {
	cmd := exec.Command("git", "-C", projectRoot, "merge-base", "--is-ancestor", sha, ref)
	err := cmd.Run()
	if err == nil {
		return true, nil
	}
	var ee *exec.ExitError
	if errors.As(err, &ee) {
		// Exit 1 = not ancestor; exit 128 = bad ref (e.g. origin/main
		// missing). Treat both as "not in main" and let the worker flow
		// degrade silently rather than spamming on a fresh repo.
		if ee.ExitCode() == 1 || ee.ExitCode() == 128 {
			return false, nil
		}
	}
	return false, err
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
