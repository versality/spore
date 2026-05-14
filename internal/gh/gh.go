// Package gh wraps the gh CLI behind a small typed surface so spore
// callers (prfinish Stop hook, ship orchestrator) share one set of
// types and one shell-out path.
//
// Real is the production implementation; tests build their own narrow
// fakes against the methods they exercise.
package gh

import (
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"strconv"
	"strings"
)

// PRState is the subset of `gh pr view --json ...` callers need.
type PRState struct {
	Number    int
	State     string // OPEN, CLOSED, MERGED
	Mergeable string // MERGEABLE, CONFLICTING, UNKNOWN
	Checks    []CheckRun
}

// CheckRun is one entry from statusCheckRollup.
type CheckRun struct {
	Name       string
	Conclusion string // SUCCESS, FAILURE, CANCELLED, SKIPPED, NEUTRAL, or "" when pending
	Status     string // COMPLETED, IN_PROGRESS, QUEUED
	URL        string
}

// RunSummary is one entry from `gh run list --json ...`. Used by the
// prfinish direct-push branch to gate stop on CI green for a sha that
// was pushed straight to main (no PR).
//
// Note vs CheckRun: the `gh run list` JSON returns Status / Conclusion
// in lower case ("completed", "success"), where the PR rollup uses
// upper case. ListRunsForCommit normalises to upper case so downstream
// switches can use the same constants for both shapes.
type RunSummary struct {
	DatabaseID int64
	Name       string // workflow name, e.g. "CI"
	Status     string // COMPLETED, IN_PROGRESS, QUEUED
	Conclusion string // SUCCESS, FAILURE, CANCELLED, TIMED_OUT, NEUTRAL, ACTION_REQUIRED, SKIPPED, or "" when pending
	URL        string
	HeadSHA    string
}

// Real shells out to the gh CLI. Each method shells out once; tests
// inject their own implementation of the narrow interface they need.
type Real struct{}

// ViewPR returns the PR for the given branch under projectRoot.
// found=false when there is no open PR. Closed/merged PRs still
// return found=true with the corresponding State.
func (Real) ViewPR(projectRoot, branch string) (PRState, bool, error) {
	cmd := exec.Command("gh", "pr", "view", branch,
		"--json", "number,state,mergeable,statusCheckRollup")
	cmd.Dir = projectRoot
	out, err := cmd.Output()
	if err != nil {
		var ee *exec.ExitError
		if errors.As(err, &ee) {
			stderr := string(ee.Stderr)
			if strings.Contains(stderr, "no pull requests found") ||
				strings.Contains(stderr, "no open pull requests found") {
				return PRState{}, false, nil
			}
		}
		return PRState{}, false, err
	}
	return ParseViewJSON(out)
}

// CreatePR opens a PR for branch against base. Returns the PR number.
// Idempotent: if a PR already exists for branch, returns its number
// instead of erroring. Empty title/body delegates to `gh pr create
// --fill` which derives them from commits.
func (r Real) CreatePR(projectRoot, branch, base, title, body string) (int, error) {
	args := []string{"pr", "create", "--head", branch, "--base", base}
	if title == "" && body == "" {
		args = append(args, "--fill")
	} else {
		if title != "" {
			args = append(args, "--title", title)
		}
		if body != "" {
			args = append(args, "--body", body)
		}
	}
	cmd := exec.Command("gh", args...)
	cmd.Dir = projectRoot
	out, err := cmd.Output()
	if err != nil {
		var ee *exec.ExitError
		if errors.As(err, &ee) {
			stderr := string(ee.Stderr)
			if strings.Contains(stderr, "already exists") {
				existing, _, lookupErr := r.ViewPR(projectRoot, branch)
				if lookupErr != nil {
					return 0, fmt.Errorf("gh pr create: PR already exists but lookup failed: %w", lookupErr)
				}
				return existing.Number, nil
			}
			return 0, fmt.Errorf("gh pr create: %w: %s", err, strings.TrimSpace(stderr))
		}
		return 0, fmt.Errorf("gh pr create: %w", err)
	}
	n, perr := parseCreateOutput(out)
	if perr != nil {
		// Fallback: look up via ViewPR. `gh pr create` always prints the
		// URL on stdout, but parsing rules drift; ViewPR is the source
		// of truth.
		existing, found, lookupErr := r.ViewPR(projectRoot, branch)
		if lookupErr != nil || !found {
			return 0, fmt.Errorf("gh pr create: parse %q: %w", strings.TrimSpace(string(out)), perr)
		}
		return existing.Number, nil
	}
	return n, nil
}

// MergePR merges the PR via the given strategy ("squash", "merge",
// "rebase"). When deleteBranch is true, the head branch is deleted on
// both local and remote. Idempotent on MERGED state (treats as success
// with a noop).
func (r Real) MergePR(projectRoot string, number int, strategy string, deleteBranch bool) error {
	args := []string{"pr", "merge", strconv.Itoa(number)}
	switch strategy {
	case "squash":
		args = append(args, "--squash")
	case "merge":
		args = append(args, "--merge")
	case "rebase":
		args = append(args, "--rebase")
	default:
		return fmt.Errorf("gh pr merge: unknown strategy %q (want squash|merge|rebase)", strategy)
	}
	if deleteBranch {
		args = append(args, "--delete-branch")
	}
	cmd := exec.Command("gh", args...)
	cmd.Dir = projectRoot
	out, err := cmd.CombinedOutput()
	if err != nil {
		if strings.Contains(string(out), "already merged") ||
			strings.Contains(string(out), "Pull request is already merged") {
			return nil
		}
		return fmt.Errorf("gh pr merge: %w: %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}

// ListRunsForCommit returns the latest GH Actions runs for the given
// sha on the given branch. Empty slice (no error) means GH has not
// scheduled any runs yet; the caller should treat that as "wait" not
// "green". Filtered to runs whose headSha matches `sha` exactly:
// `gh run list --commit` is best-effort filter on GH side, so we
// re-check headSha here to avoid stray runs for sibling commits.
func (Real) ListRunsForCommit(projectRoot, branch, sha string) ([]RunSummary, error) {
	cmd := exec.Command("gh", "run", "list",
		"--branch", branch,
		"--commit", sha,
		"--json", "databaseId,name,status,conclusion,url,headSha",
		"--limit", "10")
	cmd.Dir = projectRoot
	out, err := cmd.Output()
	if err != nil {
		var ee *exec.ExitError
		if errors.As(err, &ee) {
			return nil, fmt.Errorf("gh run list: %w: %s", err, strings.TrimSpace(string(ee.Stderr)))
		}
		return nil, fmt.Errorf("gh run list: %w", err)
	}
	return ParseRunListJSON(out, sha)
}

// ParseRunListJSON parses the JSON output of `gh run list --json
// databaseId,name,status,conclusion,url,headSha`, filters to entries
// whose headSha matches `sha`, and normalises Status / Conclusion to
// upper case so callers can share decision logic with the PR rollup.
func ParseRunListJSON(b []byte, sha string) ([]RunSummary, error) {
	var raw []struct {
		DatabaseID int64  `json:"databaseId"`
		Name       string `json:"name"`
		Status     string `json:"status"`
		Conclusion string `json:"conclusion"`
		URL        string `json:"url"`
		HeadSHA    string `json:"headSha"`
	}
	if err := json.Unmarshal(b, &raw); err != nil {
		return nil, fmt.Errorf("unmarshal gh run list json: %w", err)
	}
	out := make([]RunSummary, 0, len(raw))
	for _, r := range raw {
		if sha != "" && r.HeadSHA != sha {
			continue
		}
		out = append(out, RunSummary{
			DatabaseID: r.DatabaseID,
			Name:       r.Name,
			Status:     strings.ToUpper(r.Status),
			Conclusion: strings.ToUpper(r.Conclusion),
			URL:        r.URL,
			HeadSHA:    r.HeadSHA,
		})
	}
	return out, nil
}

// ParseViewJSON parses the JSON output of `gh pr view --json
// number,state,mergeable,statusCheckRollup`. Exposed for callers that
// have their own gh invocation but want spore's normalisation.
func ParseViewJSON(b []byte) (PRState, bool, error) {
	var raw struct {
		Number            int    `json:"number"`
		State             string `json:"state"`
		Mergeable         string `json:"mergeable"`
		StatusCheckRollup []struct {
			Name         string `json:"name"`
			Conclusion   string `json:"conclusion"`
			Status       string `json:"status"`
			DetailsURL   string `json:"detailsUrl"`
			WorkflowName string `json:"workflowName"`
		} `json:"statusCheckRollup"`
	}
	if err := json.Unmarshal(b, &raw); err != nil {
		return PRState{}, false, fmt.Errorf("unmarshal gh json: %w", err)
	}
	pr := PRState{
		Number:    raw.Number,
		State:     raw.State,
		Mergeable: raw.Mergeable,
	}
	for _, c := range raw.StatusCheckRollup {
		name := c.Name
		if c.WorkflowName != "" && c.WorkflowName != name {
			name = c.WorkflowName + " / " + name
		}
		pr.Checks = append(pr.Checks, CheckRun{
			Name:       name,
			Conclusion: c.Conclusion,
			Status:     c.Status,
			URL:        c.DetailsURL,
		})
	}
	return pr, true, nil
}

// parseCreateOutput pulls the PR number off the trailing /\d+ of a
// PR URL printed by `gh pr create`. Returns an error if no integer
// suffix is found.
func parseCreateOutput(b []byte) (int, error) {
	for _, line := range strings.Split(strings.TrimSpace(string(b)), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		if i := strings.LastIndex(line, "/"); i >= 0 && i < len(line)-1 {
			n, err := strconv.Atoi(line[i+1:])
			if err == nil {
				return n, nil
			}
		}
	}
	return 0, fmt.Errorf("no PR number in output")
}
