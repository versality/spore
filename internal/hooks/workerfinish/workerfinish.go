// Package workerfinish marks a worker-owned active task as awaiting
// operator review when the worker has reached a clean, evidenced stop.
package workerfinish

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/versality/spore/evidence"
	"github.com/versality/spore/internal/hooks/wtgit"
	"github.com/versality/spore/internal/task"
	"github.com/versality/spore/internal/task/frontmatter"
)

const (
	WorkerStateKey       = "worker-state"
	WorkerStateAwaiting  = "awaiting-operator"
	WorkerStateCleanup   = "needs-cleanup"
	WorkerResultKey      = "worker-result"
	WorkerResultMerge    = "ready-to-merge"
	WorkerResultNoChange = "no-code-change"
	WorkerResultDirty    = "dirty-worktree"
)

// Config parameterizes a Run call. Empty fields fall back to env-driven
// defaults; tests inject every field explicitly.
type Config struct {
	Slug        string
	Worktree    string
	ProjectRoot string
	Unmerged    func(projectRoot, branch string) (int, error)
	Status      func(worktree string) (string, error)
}

// Result reports the hook verdict.
type Result struct {
	ExitCode int
	Stderr   string
	Reason   string
}

// RunEnv reads worker context from the environment and evaluates the
// finish boundary. It is a silent no-op outside a worker context.
func RunEnv() (Result, error) {
	slug := os.Getenv("SPORE_TASK_SLUG")
	if slug == "" {
		return Result{Reason: "noop-context"}, nil
	}
	wd, err := os.Getwd()
	if err != nil {
		return Result{Reason: "noop-context"}, nil
	}
	return Run(Config{
		Slug:        slug,
		Worktree:    wd,
		ProjectRoot: os.Getenv("SPORE_PROJECT_ROOT"),
	})
}

// Run marks tasks/<slug>.md with worker-state: awaiting-operator when
// the worktree is clean and evidence is structurally valid.
func Run(cfg Config) (Result, error) {
	cfg = cfg.defaults()
	if cfg.Slug == "" || cfg.Worktree == "" {
		return Result{Reason: "noop-context"}, nil
	}

	taskFile := filepath.Join(cfg.Worktree, "tasks", cfg.Slug+".md")
	raw, err := os.ReadFile(taskFile)
	if err != nil {
		if os.IsNotExist(err) {
			return Result{Reason: "missing-task"}, nil
		}
		return Result{}, fmt.Errorf("read task: %w", err)
	}
	meta, body, err := frontmatter.Parse(raw)
	if err != nil {
		return Result{Reason: "parse-error"}, nil
	}
	if meta.Status != task.StatusActive {
		return Result{Reason: "not-active"}, nil
	}
	if meta.Extra != nil && meta.Extra[WorkerStateKey] == WorkerStateAwaiting {
		return Result{Reason: "already-awaiting"}, nil
	}

	status, err := cfg.Status(cfg.Worktree)
	if err != nil {
		return Result{}, fmt.Errorf("git status: %w", err)
	}
	if meaningfulDirty(status, cfg.Slug) {
		if err := writeWorkerResult(taskFile, meta, body, WorkerStateCleanup, WorkerResultDirty); err != nil {
			return Result{}, fmt.Errorf("write task: %w", err)
		}
		return Result{
			ExitCode: 2,
			Stderr:   "WORKER FINISH BLOCKED: worktree has uncommitted changes. Commit/revert them and update Evidence before stopping.\n",
			Reason:   "dirty-worktree",
		}, nil
	}

	projectRoot := cfg.ProjectRoot
	if projectRoot == "" {
		projectRoot = deriveProjectRoot(cfg.Worktree)
	}
	unmerged, err := cfg.Unmerged(projectRoot, "wt/"+cfg.Slug)
	if err != nil {
		return Result{}, fmt.Errorf("unmerged commits: %w", err)
	}

	verdict, diags := evidence.Verify(metaToAny(meta), string(body))
	if evidence.Blocks(verdict) {
		return Result{
			ExitCode: 2,
			Stderr:   fmt.Sprintf("WORKER FINISH BLOCKED: Evidence is not sufficient (%s): %s. Add ## Evidence or continue.\n", verdict, strings.Join(diags, "; ")),
			Reason:   "evidence-blocked",
		}, nil
	}

	result := WorkerResultNoChange
	if unmerged > 0 {
		result = WorkerResultMerge
	}
	if meta.Extra == nil {
		meta.Extra = map[string]string{}
	}
	if err := writeWorkerResult(taskFile, meta, body, WorkerStateAwaiting, result); err != nil {
		return Result{}, fmt.Errorf("write task: %w", err)
	}
	return Result{Reason: result}, nil
}

func writeWorkerResult(taskFile string, meta frontmatter.Meta, body []byte, state, result string) error {
	if meta.Extra == nil {
		meta.Extra = map[string]string{}
	}
	meta.Extra[WorkerStateKey] = state
	meta.Extra[WorkerResultKey] = result
	return task.WriteAtomic(taskFile, frontmatter.Write(meta, body), 0o644)
}

func (c Config) defaults() Config {
	if c.Unmerged == nil {
		c.Unmerged = task.UnmergedCommits
	}
	if c.Status == nil {
		c.Status = gitStatus
	}
	return c
}

func metaToAny(m frontmatter.Meta) map[string]any {
	out := map[string]any{
		"status":   m.Status,
		"slug":     m.Slug,
		"title":    m.Title,
		"created":  m.Created,
		"project":  m.Project,
		"host":     m.Host,
		"agent":    m.Agent,
		"priority": m.Priority,
		"session":  m.Session,
		"gate":     m.Gate,
	}
	for k, v := range m.Extra {
		out[k] = v
	}
	return out
}

func deriveProjectRoot(worktree string) string {
	if root, err := wtgit.TopLevel(worktree); err == nil {
		if main, ok := wtgit.MainCheckoutFromWorktree(root); ok {
			return main
		}
		return root
	}
	return worktree
}

func gitStatus(worktree string) (string, error) {
	cmd := exec.Command("git", "-c", "safe.directory="+worktree, "status", "--porcelain")
	cmd.Dir = worktree
	var out bytes.Buffer
	cmd.Stdout = &out
	err := cmd.Run()
	return out.String(), err
}

func meaningfulDirty(status, slug string) bool {
	for _, line := range strings.Split(status, "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		path := statusPath(line)
		if path == "" {
			return true
		}
		if allowedRuntimePath(path, slug) {
			continue
		}
		return true
	}
	return false
}

func statusPath(line string) string {
	if len(line) < 4 {
		return ""
	}
	path := strings.TrimSpace(line[3:])
	if i := strings.LastIndex(path, " -> "); i >= 0 {
		path = path[i+4:]
	}
	return filepath.ToSlash(path)
}

func allowedRuntimePath(path, slug string) bool {
	switch {
	case path == ".claude/settings.local.json":
		return true
	case strings.HasPrefix(path, ".codex/"):
		return true
	case strings.HasPrefix(path, ".wt/"):
		return true
	case path == "tasks/"+slug+".md":
		return true
	default:
		return false
	}
}
