package task

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/versality/spore/internal/codexpolicy"
	"github.com/versality/spore/internal/evidence"
	"github.com/versality/spore/internal/matter"
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

// CodexModelEnv optionally pins the model for `agent: codex` task
// launches. Empty lets the codex CLI use its own default.
const CodexModelEnv = "SPORE_CODEX_MODEL"

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
	session := taskTmuxSession(tasksDir, projectRoot, slug)
	// Pause leaves the session alive for the operator; Start
	// replaces it so a resume gets a fresh agent and new-session
	// does not collide on the name.
	_ = exec.Command("tmux", "kill-session", "-t", session).Run()
	return ensureSession(tasksDir, slug)
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
func Reap(tasksDir, projectRoot, slug string) error {
	session := taskTmuxSession(tasksDir, projectRoot, slug)
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
// are left in place so the operator can stay attached. Refuses when
// the inbox has unread messages.
func Pause(tasksDir, slug string) error {
	if err := inboxGate(slug); err != nil {
		return err
	}
	return flipStatus(tasksDir, slug, "active", "paused")
}

// Block flips an active task to blocked. Same teardown semantics as
// Pause: the worktree and tmux session are left in place. Refuses
// when the inbox has unread messages.
func Block(tasksDir, slug string) error {
	if err := inboxGate(slug); err != nil {
		return err
	}
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
//
// When force is true, the inbox-drain and unmerged-commit gates are
// bypassed; the evidence gate still runs (it has its own soak/env
// override).
func Done(tasksDir, slug string, force bool) error {
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

	if !force {
		if err := inboxGate(slug); err != nil {
			return err
		}
	}

	projectRoot, err := projectRootFromTasksDir(tasksDir)
	if err != nil {
		return err
	}

	branch := "wt/" + slug
	unmerged, err := UnmergedCommits(projectRoot, branch)
	if err != nil {
		return err
	}
	if unmerged > 0 {
		if force {
			fmt.Fprintf(os.Stderr, "spore task done %s: --force: discarding %d unmerged commit(s) on %s\n", slug, unmerged, branch)
		} else {
			return fmt.Errorf("done refused for %s: branch %q has %d unmerged commit(s); run 'spore task merge %s' first, or 'spore task done %s --force' to discard", slug, branch, unmerged, slug, slug)
		}
	}

	if err := evidenceGate(slug, m, body, os.Stderr); err != nil {
		return err
	}

	m.Status = "done"
	if err := os.WriteFile(path, frontmatter.Write(m, body), 0o644); err != nil {
		return err
	}

	notifyMatterDone(projectRoot, slug, m, os.Stderr)

	worktree := filepath.Join(projectRoot, ".worktrees", slug)
	session := taskTmuxSession(tasksDir, projectRoot, slug)

	_ = exec.Command("tmux", "kill-session", "-t", session).Run()
	_ = gitCmd(projectRoot, "worktree", "remove", "--force", worktree).Run()
	_ = gitCmd(projectRoot, "branch", "-D", branch).Run()
	return nil
}

// notifyMatterDone fires OnDone on the matter named in the task's
// frontmatter (Extra["matter"]). No-op when the key is absent or the
// adapter isn't configured for this project. Errors land on warnOut;
// the status flip remains the source of truth.
func notifyMatterDone(projectRoot, slug string, m frontmatter.Meta, warnOut io.Writer) {
	name := m.Extra[matter.MatterKey]
	if name == "" {
		return
	}
	configs, err := matter.LoadFromProject(projectRoot)
	if err != nil {
		fmt.Fprintf(warnOut, "spore task done %s: matter load: %v\n", slug, err)
		return
	}
	var cfg *matter.Config
	for i := range configs {
		if configs[i].Name == name && configs[i].Enabled {
			cfg = &configs[i]
			break
		}
	}
	if cfg == nil {
		return
	}
	matters, err := matter.FromConfig([]matter.Config{*cfg})
	if err != nil {
		fmt.Fprintf(warnOut, "spore task done %s: matter %s: %v\n", slug, name, err)
		return
	}
	if len(matters) == 0 {
		return
	}
	if err := matters[0].OnDone(context.Background(), slug, copyExtra(m.Extra)); err != nil {
		fmt.Fprintf(warnOut, "spore task done %s: matter %s OnDone: %v\n", slug, name, err)
	}
}

func copyExtra(in map[string]string) map[string]string {
	if in == nil {
		return nil
	}
	out := make(map[string]string, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
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

// inboxGate refuses the status flip when the slug's inbox has unread
// *.json files. Returns nil when the inbox is empty or missing.
func inboxGate(slug string) error {
	n, dir, err := CountUnreadInbox(slug)
	if err != nil {
		return err
	}
	if n > 0 {
		return fmt.Errorf("%d unread inbox message(s) at %s; read each, then mv to inbox/read/", n, dir)
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
	meta, err := readTaskMeta(tasksDir, slug)
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

	if external := meta.Session; external != "" && hasSession(external) {
		return external, nil
	}
	session := tmuxSessionName(projectRoot, slug)
	if hasSession(session) {
		return session, nil
	}
	agent, err := workerAgentCommand(meta)
	if err != nil {
		return "", err
	}
	project, err := ProjectName(projectRoot)
	if err != nil {
		return "", err
	}
	inbox, err := InboxDirForProject(projectRoot, slug)
	if err != nil {
		return "", err
	}
	coordinatorState, err := CoordinatorStateDir()
	if err != nil {
		return "", err
	}
	out, err := exec.Command(
		"tmux", "new-session", "-d",
		"-s", session,
		"-c", worktree,
		"-e", "SPORE_TASK_SLUG="+slug,
		"-e", "SPORE_PROJECT_ROOT="+projectRoot,
		"-e", "WT_PROJECT="+project,
		"-e", "SKYBOT_INBOX="+inbox,
		"-e", "SPORE_COORDINATOR_STATE_DIR="+coordinatorState,
		agent,
	).CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("tmux new-session: %v: %s", err, strings.TrimSpace(string(out)))
	}
	return session, nil
}

func readTaskMeta(tasksDir, slug string) (frontmatter.Meta, error) {
	path := filepath.Join(tasksDir, slug+".md")
	raw, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return frontmatter.Meta{}, nil
		}
		return frontmatter.Meta{}, err
	}
	m, _, err := frontmatter.Parse(raw)
	if err != nil {
		return frontmatter.Meta{}, fmt.Errorf("parse %s: %w", path, err)
	}
	return m, nil
}

func workerAgentCommand(m frontmatter.Meta) (string, error) {
	if override := os.Getenv(AgentBinaryEnv); override != "" {
		return override, nil
	}
	agent := m.Agent
	if agent == "" || agent == "claude" || agent == "claude-code" {
		return defaultAgentBinary, nil
	}
	if agent != "codex" {
		return agent, nil
	}

	effort, err := codexpolicy.EffortForTask(m.Extra["effort"], m.Extra["complexity"])
	if err != nil {
		return "", err
	}
	model := m.Extra["model"]
	if model == "" {
		model = os.Getenv(CodexModelEnv)
	}
	return shellJoin(codexpolicy.InteractiveArgs(model, effort)), nil
}

func shellJoin(args []string) string {
	quoted := make([]string, 0, len(args))
	for _, arg := range args {
		quoted = append(quoted, shellQuote(arg))
	}
	return strings.Join(quoted, " ")
}

func shellQuote(s string) string {
	if s == "" {
		return "''"
	}
	if strings.IndexFunc(s, func(r rune) bool { return !isShellBareChar(r) }) == -1 {
		return s
	}
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

func isShellBareChar(r rune) bool {
	return r >= 'a' && r <= 'z' ||
		r >= 'A' && r <= 'Z' ||
		r >= '0' && r <= '9' ||
		r == '_' || r == '-' || r == '.' || r == '/' || r == ':' || r == '='
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

// taskTmuxSession returns the tmux session name to target for slug
// when killing or probing the rower's session. The frontmatter
// `session:` field wins when set (the spawner registers the real
// session name there, e.g. "🐈 acme/my-task [opus]"); otherwise
// the kernel's computed "spore/<project>/<slug>" name is used. A
// task file that fails to read or parse falls back to the computed
// name so a corrupt brief never blocks cleanup.
func taskTmuxSession(tasksDir, projectRoot, slug string) string {
	if m, err := readTaskMeta(tasksDir, slug); err == nil && m.Session != "" {
		return m.Session
	}
	return tmuxSessionName(projectRoot, slug)
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
