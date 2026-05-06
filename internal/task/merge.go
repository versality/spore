package task

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/versality/spore/internal/task/frontmatter"
)

// MergeOptions tunes Merge. ForceMergeRed bypasses the just check
// gate when non-empty; the reason is recorded to merge-override.jsonl
// so operators can audit who shipped red and why.
type MergeOptions struct {
	ForceMergeRed string
}

// MergeGateError is returned when the just check gate refuses a
// merge. It carries the recipe that failed and exposes ExitCode 2 so
// CLI callers can mirror the upstream bash gate's exit shape.
type MergeGateError struct {
	Recipe string
	Reason string
	Err    error
}

func (e *MergeGateError) Error() string {
	if e.Recipe == "" {
		return fmt.Sprintf("merge gate refused: %s", e.Reason)
	}
	return fmt.Sprintf("merge gate refused: just %s failed: %s", e.Recipe, e.Reason)
}

func (e *MergeGateError) Unwrap() error { return e.Err }

// ExitCode returns 2, matching `wt merge`'s upstream gate.
func (e *MergeGateError) ExitCode() int { return 2 }

// Merge fast-forward merges the wt/<slug> branch into main, then
// cleans up the worktree and branch. The task file's status is
// flipped to done as part of the merge. Refuses if the merge would
// not be a fast-forward or if the landed main cannot be pushed to
// origin.
func Merge(tasksDir, slug string) error {
	return MergeWithOptions(tasksDir, slug, MergeOptions{})
}

// MergeWithOptions is Merge with a knob for the just check gate.
// Pass MergeOptions{ForceMergeRed: reason} to bypass and log the
// override. Mirrors nix-config's wt cmd_merge --force-merge-red.
func MergeWithOptions(tasksDir, slug string, opts MergeOptions) error {
	projectRoot, err := projectRootFromTasksDir(tasksDir)
	if err != nil {
		return err
	}
	branch := "wt/" + slug
	if !branchExists(projectRoot, branch) {
		return fmt.Errorf("branch %s does not exist", branch)
	}
	if err := requireMainCheckout(projectRoot); err != nil {
		return err
	}

	if err := runJustCheckGate(projectRoot, slug, branch, opts.ForceMergeRed); err != nil {
		return err
	}

	// Fast-forward main to include wt/<slug>.
	out, err := gitCmd(projectRoot, "merge", "--ff-only", branch).CombinedOutput()
	if err != nil {
		return fmt.Errorf("merge --ff-only: %w: %s", err, strings.TrimSpace(string(out)))
	}

	if err := closeMergedTask(tasksDir, slug); err != nil {
		return err
	}
	if err := pushAndVerifyMain(projectRoot); err != nil {
		return err
	}

	worktree := filepath.Join(projectRoot, ".worktrees", slug)
	if _, statErr := os.Stat(worktree); statErr == nil {
		_ = gitCmd(projectRoot, "worktree", "remove", "--force", worktree).Run()
	}
	_ = gitCmd(projectRoot, "branch", "-d", branch).Run()

	session := taskTmuxSession(tasksDir, projectRoot, slug)
	_ = exec.Command("tmux", "kill-session", "-t", session).Run()

	return nil
}

func requireMainCheckout(projectRoot string) error {
	out, err := gitCmd(projectRoot, "branch", "--show-current").Output()
	if err != nil {
		return fmt.Errorf("git branch --show-current: %w", err)
	}
	current := strings.TrimSpace(string(out))
	if current != "main" {
		if current == "" {
			current = "detached HEAD"
		}
		return fmt.Errorf("merge must run from the main checkout; current branch is %q", current)
	}
	return nil
}

func pushAndVerifyMain(projectRoot string) error {
	out, err := gitCmd(projectRoot, "push", "origin", "main:main").CombinedOutput()
	if err != nil {
		return fmt.Errorf("git push origin main:main: %w: %s", err, strings.TrimSpace(string(out)))
	}

	localOut, err := gitCmd(projectRoot, "rev-parse", "main").Output()
	if err != nil {
		return fmt.Errorf("git rev-parse main: %w", err)
	}
	local := strings.TrimSpace(string(localOut))

	remoteOut, err := gitCmd(projectRoot, "ls-remote", "origin", "refs/heads/main").Output()
	if err != nil {
		return fmt.Errorf("git ls-remote origin refs/heads/main: %w", err)
	}
	fields := strings.Fields(string(remoteOut))
	if len(fields) == 0 {
		return fmt.Errorf("post-push verification failed: origin refs/heads/main not found")
	}
	if fields[0] != local {
		return fmt.Errorf("post-push verification failed: origin/main=%s, local main=%s", fields[0], local)
	}
	return nil
}

func closeMergedTask(tasksDir, slug string) error {
	path := filepath.Join(tasksDir, slug+".md")
	raw, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	m, body, err := frontmatter.Parse(raw)
	if err != nil {
		return fmt.Errorf("parse %s: %w", path, err)
	}
	if m.Status == "done" {
		return nil
	}
	if err := inboxGate(slug); err != nil {
		return err
	}
	if err := evidenceGate(slug, m, body, os.Stderr); err != nil {
		return err
	}

	m.Status = "done"
	if err := os.WriteFile(path, frontmatter.Write(m, body), 0o644); err != nil {
		return err
	}

	projectRoot, err := projectRootFromTasksDir(tasksDir)
	if err != nil {
		return err
	}
	rel, err := filepath.Rel(projectRoot, path)
	if err != nil {
		rel = path
	}
	if out, err := gitCmd(projectRoot, "add", "--", rel).CombinedOutput(); err != nil {
		return fmt.Errorf("git add task close: %w: %s", err, strings.TrimSpace(string(out)))
	}
	if out, err := gitCmd(projectRoot, "commit", "-m", fmt.Sprintf("tasks/%s: status -> done", slug), "--", rel).CombinedOutput(); err != nil {
		if strings.Contains(string(out), "nothing to commit") {
			return nil
		}
		return fmt.Errorf("git commit task close: %w: %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}

// runJustCheckGate runs `just check` from the wt/<slug> worktree
// before the fast-forward. Skips silently when there is no worktree,
// no justfile, no `just` on PATH, or no `check` recipe; otherwise
// refuses with a typed *MergeGateError on red. forceReason bypasses
// the gate after appending an override row to merge-override.jsonl.
// Hard-coded `check` recipe mirrors nix/packages/wt/wt's
// _run_just_check_gate; per-project configuration is documented as
// out-of-scope in docs/worker-dispatch.md.
func runJustCheckGate(projectRoot, slug, branch, forceReason string) error {
	if forceReason != "" {
		if err := logMergeOverride(projectRoot, slug, branch, forceReason); err != nil {
			fmt.Fprintf(os.Stderr, "spore task merge: log override failed: %v\n", err)
		}
		fmt.Fprintf(os.Stderr, "spore task merge: --force-merge-red set (reason: %s); skipping just check gate\n", forceReason)
		return nil
	}
	worktree := filepath.Join(projectRoot, ".worktrees", slug)
	if _, err := os.Stat(worktree); err != nil {
		return nil
	}
	if _, err := os.Stat(filepath.Join(worktree, "justfile")); err != nil {
		return nil
	}
	if _, err := exec.LookPath("just"); err != nil {
		return nil
	}
	show := exec.Command("just", "--show", "check")
	show.Dir = worktree
	if err := show.Run(); err != nil {
		return nil
	}
	fmt.Fprintln(os.Stderr, "spore task merge: running just check (merge gate)")
	cmd := exec.Command("just", "check")
	cmd.Dir = worktree
	cmd.Stdout = os.Stderr
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return &MergeGateError{
			Recipe: "check",
			Reason: "just check failed; refusing to land. Fix the failing recipe, or pass --force-merge-red <reason> for genuine emergencies.",
			Err:    err,
		}
	}
	return nil
}

// logMergeOverride appends a JSONL row to
// $STATE_DIR/merge-override.jsonl recording a --force-merge-red
// bypass. Operators tail the ledger to spot rowers shipping red.
// SPORE_MERGE_OVERRIDE_LOG overrides the path (used by tests).
func logMergeOverride(projectRoot, slug, branch, reason string) error {
	path := os.Getenv("SPORE_MERGE_OVERRIDE_LOG")
	if path == "" {
		stateDir, err := StateDirForProject(projectRoot)
		if err != nil {
			return err
		}
		path = filepath.Join(stateDir, "merge-override.jsonl")
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	project, _ := ProjectName(projectRoot)
	row := struct {
		TS      string `json:"ts"`
		Slug    string `json:"slug"`
		Project string `json:"project"`
		Branch  string `json:"branch"`
		Reason  string `json:"reason"`
	}{
		TS:      time.Now().UTC().Format(time.RFC3339),
		Slug:    slug,
		Project: project,
		Branch:  branch,
		Reason:  reason,
	}
	line, err := json.Marshal(row)
	if err != nil {
		return err
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()
	if _, err := f.Write(append(line, '\n')); err != nil {
		return err
	}
	return nil
}
