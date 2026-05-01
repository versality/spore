package task

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/versality/spore/internal/evidence"
	"github.com/versality/spore/internal/task/frontmatter"
)

// EvidenceWarnOnlyEnv forces the evidence done-gate into warn-only
// mode regardless of the soak window. The soak window already gates
// warn-only behavior for the first 7 days after evidence.ContractStart;
// the env var stays as a permanent rollback override per the brief.
const EvidenceWarnOnlyEnv = "SPORE_EVIDENCE_WARN_ONLY"

// AgentBinaryEnv is the env var used to override the binary spawned in
// the per-task tmux session. Defaults to defaultAgentBinary when unset.
const AgentBinaryEnv = "SPORE_AGENT_BINARY"

const defaultAgentBinary = "claude-code"

// Start flips status to active and (when starting from draft) creates
// the worktree and wt/<slug> branch under <projectRoot>/.worktrees/.
// In every case it spawns a detached tmux session named
// "spore/<project>/<slug>" running ${SPORE_AGENT_BINARY:-claude-code}
// in the worktree, with SPORE_TASK_SLUG=<slug> in the session env.
// Returns the tmux session name on success.
func Start(tasksDir, slug string) (string, error) {
	path := filepath.Join(tasksDir, slug+".md")
	raw, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	m, body, err := frontmatter.Parse(raw)
	if err != nil {
		return "", fmt.Errorf("parse %s: %w", path, err)
	}
	prev := m.Status
	switch prev {
	case "draft", "paused", "blocked":
	case "active":
		return "", fmt.Errorf("task %s: already active", slug)
	case "done":
		return "", fmt.Errorf("task %s: already done", slug)
	default:
		return "", fmt.Errorf("task %s: unexpected status %q", slug, prev)
	}
	m.Status = "active"
	if err := os.WriteFile(path, frontmatter.Write(m, body), 0o644); err != nil {
		return "", err
	}

	projectRoot, err := projectRootFromTasksDir(tasksDir)
	if err != nil {
		return "", err
	}
	session := tmuxSessionName(projectRoot, slug)
	// Pause leaves the session alive for the operator; Start
	// replaces it so a resume gets a fresh agent and new-session
	// does not collide on the name.
	_ = exec.Command("tmux", "kill-session", "-t", session).Run()
	if _, err := ensureSession(tasksDir, slug); err != nil {
		return "", err
	}
	return session, nil
}

// Ensure makes sure the wt/<slug> branch, worktree, and tmux session
// for slug exist. Idempotent: missing pieces get created, present
// ones are left alone. Status is not touched. Used by the fleet
// reconciler to bring an active task into the running state without
// flipping its status.
func Ensure(tasksDir, slug string) (string, error) {
	return ensureSession(tasksDir, slug)
}

// Reap kills the tmux session for slug. Status, worktree, and branch
// are left untouched. Used by the fleet reconciler when a task
// leaves active.
func Reap(projectRoot, slug string) error {
	session := tmuxSessionName(projectRoot, slug)
	return exec.Command("tmux", "kill-session", "-t", session).Run()
}

// SpawnedSlugs lists slugs of every tmux session that matches the
// "spore/<project>/<slug>" pattern. Returns an empty slice (and a
// nil error) when no tmux server is running.
func SpawnedSlugs(projectRoot string) ([]string, error) {
	out, err := exec.Command("tmux", "list-sessions", "-F", "#{session_name}").Output()
	if err != nil {
		// tmux exits non-zero with "no server running" or no
		// sessions; treat both as empty.
		return nil, nil
	}
	prefix := tmuxSessionPrefix(projectRoot)
	var slugs []string
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || !strings.HasPrefix(line, prefix) {
			continue
		}
		slugs = append(slugs, strings.TrimPrefix(line, prefix))
	}
	sort.Strings(slugs)
	return slugs, nil
}

// Pause flips an active task to paused. The worktree and tmux session
// are left in place so the operator can stay attached.
func Pause(tasksDir, slug string) error {
	return flipStatus(tasksDir, slug, "active", "paused")
}

// Block flips an active task to blocked. Same teardown semantics as
// Pause: the worktree and tmux session are left in place.
func Block(tasksDir, slug string) error {
	return flipStatus(tasksDir, slug, "active", "blocked")
}

// Verify reads tasks/<slug>.md and runs the structural evidence
// verifier. Returns the verdict plus diagnostic lines. Used by
// `spore task verify` so the operator can preview the gate's decision
// without touching status.
func Verify(tasksDir, slug string) (evidence.Verdict, []string, error) {
	path := filepath.Join(tasksDir, slug+".md")
	raw, err := os.ReadFile(path)
	if err != nil {
		return "", nil, err
	}
	m, body, err := frontmatter.Parse(raw)
	if err != nil {
		return "", nil, fmt.Errorf("parse %s: %w", path, err)
	}
	verdict, diags := evidence.Verify(metaToAny(m), string(body))
	return verdict, diags, nil
}

// Done flips a task to done and best-effort cleans up the tmux
// session, worktree, and wt/<slug> branch. Errors from cleanup are
// swallowed; the status flip is the source of truth. Calling Done on
// an already-done task is a no-op.
func Done(tasksDir, slug string) error {
	path := filepath.Join(tasksDir, slug+".md")
	raw, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	m, body, err := frontmatter.Parse(raw)
	if err != nil {
		return fmt.Errorf("parse %s: %w", path, err)
	}
	if m.Status == "done" {
		return nil
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
	worktree := filepath.Join(projectRoot, ".worktrees", slug)
	branch := "wt/" + slug
	session := tmuxSessionName(projectRoot, slug)

	_ = exec.Command("tmux", "kill-session", "-t", session).Run()
	_ = gitCmd(projectRoot, "worktree", "remove", "--force", worktree).Run()
	_ = gitCmd(projectRoot, "branch", "-D", branch).Run()
	return nil
}

// evidenceGate runs the structural evidence verifier on the task body
// and refuses the done flip when the verdict blocks. Pre-contract
// tasks (no evidence_required declared) are skipped silently. During
// the soak window or when SPORE_EVIDENCE_WARN_ONLY=1 is set, blocking
// verdicts are reduced to a stderr warning.
func evidenceGate(slug string, m frontmatter.Meta, body []byte, warnOut *os.File) error {
	meta := metaToAny(m)
	if len(evidence.Required(meta)) == 0 {
		return nil
	}
	verdict, diags := evidence.Verify(meta, string(body))
	if !evidence.Blocks(verdict) {
		return nil
	}
	msg := fmt.Sprintf("evidence verdict: %s", verdict)
	for _, d := range diags {
		msg += "\n  " + d
	}
	warnOnly := os.Getenv(EvidenceWarnOnlyEnv) == "1" || evidence.InSoakWindow(time.Now())
	if !warnOnly {
		return fmt.Errorf("done refused for %s: %s", slug, msg)
	}
	if warnOut != nil {
		fmt.Fprintf(warnOut, "spore task done %s: warn-only: %s\n", slug, msg)
	}
	return nil
}

// metaToAny lifts frontmatter.Meta into the map[string]any shape
// evidence.Required and evidence.Verify accept. Spore's parser only
// stores strings, so this is just a key-by-key copy.
func metaToAny(m frontmatter.Meta) map[string]any {
	out := map[string]any{}
	if m.Status != "" {
		out["status"] = m.Status
	}
	if m.Slug != "" {
		out["slug"] = m.Slug
	}
	if m.Title != "" {
		out["title"] = m.Title
	}
	if m.Created != "" {
		out["created"] = m.Created
	}
	if m.Project != "" {
		out["project"] = m.Project
	}
	if m.Host != "" {
		out["host"] = m.Host
	}
	if m.Agent != "" {
		out["agent"] = m.Agent
	}
	for k, v := range m.Extra {
		out[k] = v
	}
	return out
}

// ensureSession is the shared idempotent path for Start and Ensure.
// It creates the worktree + branch when missing (re-attaching to an
// existing branch when the worktree was removed) and (re)spawns the
// tmux session when not already alive.
func ensureSession(tasksDir, slug string) (string, error) {
	projectRoot, err := projectRootFromTasksDir(tasksDir)
	if err != nil {
		return "", err
	}
	worktree := filepath.Join(projectRoot, ".worktrees", slug)
	branch := "wt/" + slug

	if _, err := os.Stat(worktree); os.IsNotExist(err) {
		args := []string{"worktree", "add", worktree}
		if branchExists(projectRoot, branch) {
			args = append(args, branch)
		} else {
			args = append(args, "-b", branch)
		}
		out, err := gitCmd(projectRoot, args...).CombinedOutput()
		if err != nil {
			return "", fmt.Errorf("git worktree add: %v: %s", err, strings.TrimSpace(string(out)))
		}
		// Copy the brief into the new worktree so headless workers
		// can read it. The worktree forks from the source branch's
		// HEAD which often does not yet include this brief: Start
		// rewrites it just before this call (status flip), and the
		// operator may not have committed it on the source branch
		// either. Soft-fails on a missing source brief; the worker
		// falls back to interactive mode there.
		if err := copyBriefToWorktree(tasksDir, worktree, slug); err != nil {
			return "", fmt.Errorf("copy brief: %w", err)
		}
	}

	session := tmuxSessionName(projectRoot, slug)
	if hasSession(session) {
		return session, nil
	}
	agent := os.Getenv(AgentBinaryEnv)
	if agent == "" {
		agent = defaultAgentBinary
	}
	out, err := exec.Command(
		"tmux", "new-session", "-d",
		"-s", session,
		"-c", worktree,
		"-e", "SPORE_TASK_SLUG="+slug,
		agent,
	).CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("tmux new-session: %v: %s", err, strings.TrimSpace(string(out)))
	}
	return session, nil
}

func flipStatus(tasksDir, slug, from, to string) error {
	path := filepath.Join(tasksDir, slug+".md")
	raw, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	m, body, err := frontmatter.Parse(raw)
	if err != nil {
		return fmt.Errorf("parse %s: %w", path, err)
	}
	if m.Status != from {
		return fmt.Errorf("task %s: status %q (want %q)", slug, m.Status, from)
	}
	m.Status = to
	return os.WriteFile(path, frontmatter.Write(m, body), 0o644)
}

func projectRootFromTasksDir(tasksDir string) (string, error) {
	abs, err := filepath.Abs(tasksDir)
	if err != nil {
		return "", err
	}
	return filepath.Dir(abs), nil
}

func copyBriefToWorktree(tasksDir, worktree, slug string) error {
	src := filepath.Join(tasksDir, slug+".md")
	body, err := os.ReadFile(src)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	rel := filepath.Base(filepath.Clean(tasksDir))
	dst := filepath.Join(worktree, rel, slug+".md")
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	return os.WriteFile(dst, body, 0o644)
}

func tmuxSessionName(projectRoot, slug string) string {
	return tmuxSessionPrefix(projectRoot) + slug
}

func tmuxSessionPrefix(projectRoot string) string {
	return fmt.Sprintf("spore/%s/", filepath.Base(projectRoot))
}

func hasSession(name string) bool {
	return exec.Command("tmux", "has-session", "-t", name).Run() == nil
}

func branchExists(projectRoot, branch string) bool {
	return gitCmd(projectRoot, "show-ref", "--verify", "--quiet", "refs/heads/"+branch).Run() == nil
}
