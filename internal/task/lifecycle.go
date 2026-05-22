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

	"github.com/versality/spore/evidence"
	"github.com/versality/spore/internal/hooks/inject"
	"github.com/versality/spore/internal/matter"
	"github.com/versality/spore/internal/task/consumerclaim"
	"github.com/versality/spore/internal/task/frontmatter"
	"github.com/versality/spore/internal/tmuxsess"
)

// EvidenceWarnOnlyEnv forces the evidence done-gate into warn-only
// mode regardless of the soak window. The soak window already gates
// warn-only behavior for the first 7 days after evidence.ContractStart;
// the env var stays as a permanent rollback override per the brief.
const EvidenceWarnOnlyEnv = "SPORE_EVIDENCE_WARN_ONLY"

// AgentBinaryEnv is the env var used to override the binary spawned in
// the per-task tmux session. Defaults to claude when unset.
const AgentBinaryEnv = "SPORE_AGENT_BINARY"

// CodexModelEnv optionally pins the model for `agent: codex` task
// launches. Empty lets the codex CLI use its own default.
const CodexModelEnv = "SPORE_CODEX_MODEL"

// Start flips status to active and (when starting from backlog) creates
// the worktree and wt/<slug> branch under <projectRoot>/.worktrees/.
// In every case it spawns a detached wt-style tmux session running
// ${SPORE_AGENT_BINARY:-claude} in the worktree, with
// SPORE_TASK_SLUG=<slug> in the session env. extraEnv
// adds KEY=VAL pairs to the tmux session env (mirrors `-e KEY=VAL`
// repeats on tmux new-session). Returns the tmux session name on
// success.
func Start(tasksDir, slug string, extraEnv []string) (string, error) {
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
	switch CanonicalStatus(prev) {
	case StatusDraft, StatusBlocked:
	case StatusActive:
		return "", fmt.Errorf("task %s: already active", slug)
	case StatusDone:
		return "", fmt.Errorf("task %s: already done", slug)
	default:
		return "", fmt.Errorf("task %s: unexpected status %q", slug, prev)
	}
	m.Status = StatusActive
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
	return ensureSession(tasksDir, slug, extraEnv)
}

// Ensure makes sure the wt/<slug> branch, worktree, and tmux session
// exist. Idempotent and status-preserving. Refuses when the task is
// done. Used by the fleet reconciler to revive an active+no-tmux
// worker without flipping its frontmatter. extraEnv mirrors Start.
func Ensure(tasksDir, slug string, extraEnv []string) (string, error) {
	path := filepath.Join(tasksDir, slug+".md")
	raw, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	m, _, err := frontmatter.Parse(raw)
	if err != nil {
		return "", fmt.Errorf("parse %s: %w", path, err)
	}
	if IsDone(m.Status) {
		return "", fmt.Errorf("task %s: already done", slug)
	}
	return ensureSession(tasksDir, slug, extraEnv)
}

// Reap kills every tmux session matching slug for the project (the
// frontmatter-recorded one plus any duplicates that drifted during
// spawn or got minted with a different tier tag). Status, worktree,
// and branch are left untouched. Used by the fleet reconciler when a
// task leaves active.
func Reap(tasksDir, projectRoot, slug string) error {
	killAllSlugSessions(tasksDir, projectRoot, slug)
	return nil
}

// SpawnedSlugs lists slugs of every tmux session that matches the
// current wt-style pattern plus older spore-prefixed names. Returns an
// empty slice (and a nil error) when no tmux server is running.
func SpawnedSlugs(projectRoot string) ([]string, error) {
	out, err := exec.Command("tmux", "list-sessions", "-F", "#{session_name}").Output()
	if err != nil {
		// tmux exits non-zero with "no server running" or no
		// sessions; treat both as empty.
		return nil, nil
	}
	project := projectNameOrBase(projectRoot)
	var slugs []string
	seen := map[string]bool{}
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		line = strings.TrimSpace(line)
		p, ok := ParseSession(line, project)
		if !ok || p.Kind != SessionKindWorker || seen[p.Slug] {
			continue
		}
		seen[p.Slug] = true
		slugs = append(slugs, p.Slug)
	}
	sort.Strings(slugs)
	return slugs, nil
}

// Pause is retired. drop-parked-status-gate collapsed paused into
// blocked; callers must name a blocker reason instead. The stub
// returns a hard error pointing at the new verb.
func Pause(tasksDir, slug string) error {
	return fmt.Errorf("task %s: pause is retired; use `spore task block %s --blocker \"<reason>\"`", slug, slug)
}

// Park is retired. drop-parked-status-gate collapsed parked into
// blocked; callers must name a blocker reason instead. The stub
// returns a hard error pointing at the new verb.
func Park(tasksDir, slug string) error {
	return fmt.Errorf("task %s: park is retired; use `spore task block %s --blocker \"<reason>\"`", slug, slug)
}

// Block flips an active task to blocked, persisting the blocker
// reason. Same idle-gated reap as before: the worktree stays, the
// session is killed only if idle past IdleReapThreshold. Refuses when
// the inbox has unread messages. Refuses when called from a
// coordinator session (drop-parked-status-gate gate: only operator
// and a worker session on its own slug may block).
func Block(tasksDir, slug, blocker string) error {
	if err := blockCoordinatorGate(); err != nil {
		return err
	}
	if err := inboxGate(slug); err != nil {
		return err
	}
	if err := flipStatusWithBlocker(tasksDir, slug, StatusActive, StatusBlocked, blocker); err != nil {
		return err
	}
	reapIdleSlugSessions(tasksDir, slug)
	return nil
}

// BlockAuto is the same status flip as Block, minus the inbox gate.
// Used by the auto-eviction path: when a worker posts a question to
// the coordinator via `spore task tell coordinator ...`, the same
// call atomically flips the worker's own slug to blocked so the slot
// is freed without the worker calling `spore task block` itself. The
// coordinator-session gate still applies; the operator-bound "drain
// your inbox before flipping" gate does not, because the worker has
// just posted a question and may legitimately still hold unread
// inbox items it wanted the coordinator to address.
func BlockAuto(tasksDir, slug, blocker string) error {
	if err := blockCoordinatorGate(); err != nil {
		return err
	}
	if err := flipStatusWithBlocker(tasksDir, slug, StatusActive, StatusBlocked, blocker); err != nil {
		return err
	}
	reapIdleSlugSessions(tasksDir, slug)
	return nil
}

// Unblock flips a blocked task back to active and clears the blocker
// reason. Used by scheduler scripts when their trigger condition is
// met and by the operator. No coordinator gate: a coordinator may
// unblock; it just may not block.
func Unblock(tasksDir, slug string) error {
	return flipStatusWithBlocker(tasksDir, slug, StatusBlocked, StatusActive, "")
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
	if IsDone(m.Status) {
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

	if err := consumerClaimsGate(slug, m, force, os.Stderr); err != nil {
		return err
	}

	if err := evidenceGate(slug, m, body, os.Stderr); err != nil {
		return err
	}

	m.Status = StatusDone
	if err := os.WriteFile(path, frontmatter.Write(m, body), 0o644); err != nil {
		return err
	}

	notifyMatterDone(projectRoot, slug, m, os.Stderr)

	worktree := filepath.Join(projectRoot, ".worktrees", slug)

	killAllSlugSessions(tasksDir, projectRoot, slug)
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
// consumerClaimsGate enforces I11
// (tasks/spore-worker-finish-contract.md section 3): a task with
// `consumer-claims:` frontmatter cannot flip `done` until every claim
// resolves clean (consumer no longer references the obsoleted thing)
// or the operator passes --force. Skipped claims (consumer checkout
// absent locally) count as unresolved; an operator cannot prove the
// consumer caught up if they cannot scan.
func consumerClaimsGate(slug string, m frontmatter.Meta, force bool, warnOut io.Writer) error {
	if len(m.ConsumerClaims) == 0 {
		return nil
	}
	claims := make([]consumerclaim.Claim, 0, len(m.ConsumerClaims))
	for _, raw := range m.ConsumerClaims {
		c, err := consumerclaim.ParseClaim(raw)
		if err != nil {
			if force {
				fmt.Fprintf(warnOut, "spore task done %s: --force: ignoring malformed claim %q: %v\n", slug, raw, err)
				continue
			}
			return fmt.Errorf("done refused for %s: %w", slug, err)
		}
		claims = append(claims, c)
	}
	results := consumerclaim.Scan(claims, consumerclaim.Deps{})
	if !consumerclaim.AnyUnresolved(results) {
		return nil
	}
	if force {
		fmt.Fprintf(warnOut, "spore task done %s: --force: %d consumer-claim(s) still unresolved\n", slug, countUnresolved(results))
		for _, r := range results {
			if r.Status == consumerclaim.StatusResolved {
				continue
			}
			fmt.Fprintf(warnOut, "  - %s:%s:%s [%s] %s\n", r.Claim.Repo, r.Claim.Kind, r.Claim.Value, r.Status, r.Detail)
		}
		return nil
	}
	var b strings.Builder
	fmt.Fprintf(&b, "done refused for %s: %d consumer-claim(s) still unresolved (use --force to override):\n", slug, countUnresolved(results))
	for _, r := range results {
		if r.Status == consumerclaim.StatusResolved {
			continue
		}
		fmt.Fprintf(&b, "  - %s:%s:%s [%s] %s\n", r.Claim.Repo, r.Claim.Kind, r.Claim.Value, r.Status, r.Detail)
	}
	return fmt.Errorf("%s", b.String())
}

func countUnresolved(results []consumerclaim.Result) int {
	n := 0
	for _, r := range results {
		if r.Status != consumerclaim.StatusResolved {
			n++
		}
	}
	return n
}

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
// Creates / reuses the worktree per classifyWorktree and (re)spawns
// the tmux session when not already alive. The session name comes
// from frontmatter `session:` when set (so external spawners like
// wt-go keep their name across respawns); otherwise the kernel's
// wt-style form. extraEnv lands on tmux new-session as `-e KEY=VAL`
// repeats.
func ensureSession(tasksDir, slug string, extraEnv []string) (string, error) {
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

	state, err := classifyWorktree(projectRoot, worktree, branch)
	if err != nil {
		return "", err
	}
	switch state {
	case worktreeOK:
	case worktreeAbsent, worktreeStaleReg:
		// prune is repo-wide; harmless because it only drops entries
		// whose dir is already gone.
		if state == worktreeStaleReg {
			if out, err := gitCmd(projectRoot, "worktree", "prune").CombinedOutput(); err != nil {
				return "", fmt.Errorf("git worktree prune: %v: %s", err, strings.TrimSpace(string(out)))
			}
		}
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
		// Source HEAD often has no committed brief; soft-fails so the
		// worker falls back to interactive mode there.
		if err := copyBriefToWorktree(tasksDir, worktree, slug); err != nil {
			return "", fmt.Errorf("copy brief: %w", err)
		}
	default:
		return "", worktreeConflictError(state, worktree, branch, projectRoot)
	}

	session, err := tmuxSessionName(projectRoot, slug, meta)
	if err != nil {
		return "", err
	}
	if meta.Session != "" {
		session = meta.Session
	}
	if tmuxsess.Has(session) {
		return session, nil
	}
	agent, err := workerAgentCommand(meta)
	if err != nil {
		return "", err
	}
	agentName := workerAgentName(meta)
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
	// Refresh the initial-prompt stage file on every respawn so a
	// re-mint into an existing worktree (the wedge-recovery path)
	// still gets a fresh brief, not just first-time worktree creation.
	if err := stageInitialPrompt(tasksDir, worktree, slug); err != nil {
		return "", fmt.Errorf("stage initial-prompt: %w", err)
	}
	if _, _, err := inject.Inject(projectRoot, worktree, SessionKindWorker); err != nil {
		return "", fmt.Errorf("inject settings: %w", err)
	}
	if _, _, err := inject.InjectCodex(projectRoot, worktree, SessionKindWorker); err != nil {
		return "", fmt.Errorf("inject codex hooks: %w", err)
	}
	// Wrap the agent command through sh -c so we can append the
	// initial-prompt brief on launch (mirrors the old wt-task
	// `agent_cmd -- "$(cat .wt/initial-prompt)"` pattern). Without
	// this the claude TUI opens empty and the worker idles waiting
	// for the operator to type. The conditional cat keeps the path
	// no-op when the file is absent.
	if strings.HasPrefix(strings.TrimSpace(agent), "claude") && !strings.Contains(agent, "--dangerously-skip-permissions") {
		agent = "claude --dangerously-skip-permissions" + strings.TrimPrefix(strings.TrimSpace(agent), "claude")
	}
	shellCmd := agent
	if os.Getenv(AgentBinaryEnv) == "" {
		shellCmd += ` ${SPORE_BRIEF_FILE:+-- "$(cat "$SPORE_BRIEF_FILE")"}`
	}
	args := []string{
		"new-session", "-d",
		"-s", session,
		"-n", agentName,
		"-c", worktree,
		"-e", "SPORE_TASK_SLUG=" + slug,
		"-e", "SPORE_PROJECT_ROOT=" + projectRoot,
		"-e", "WT_PROJECT=" + project,
		"-e", "SPORE_TASK_INBOX=" + inbox,
		"-e", "SPORE_COORDINATOR_STATE_DIR=" + coordinatorState,
		"-e", SessionKindEnv + "=" + SessionKindWorker,
	}
	briefPath := filepath.Join(worktree, ".wt", "initial-prompt")
	if _, err := os.Stat(briefPath); err == nil {
		args = append(args, "-e", "SPORE_BRIEF_FILE="+briefPath)
	}
	for _, kv := range extraEnv {
		if kv == "" {
			continue
		}
		args = append(args, "-e", kv)
	}
	args = append(args, "sh", "-c", shellCmd)
	out, err := exec.Command("tmux", args...).CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("tmux new-session: %v: %s", err, strings.TrimSpace(string(out)))
	}
	// remain-on-exit keeps the pane alive after the agent exits, so the
	// single-window session does not vanish on a clean claude exit. Without
	// this every clean exit destroys the session and active frontmatter
	// becomes a lie (tmux session missing in fleet status).
	if out, err := exec.Command("tmux", "set-option", "-t", session, "remain-on-exit", "on").CombinedOutput(); err != nil {
		return "", fmt.Errorf("tmux set-option remain-on-exit: %v: %s", err, strings.TrimSpace(string(out)))
	}
	return session, nil
}

// workerAgentName returns the window name to use for the spawned
// tmux agent window. Mirrors workerAgentCommand's agent-resolution
// but yields just the agent label ("claude" / "codex") so the fleet
// liveness check (which expects window name == agent) sees the
// window as healthy regardless of the binary's wrapper basename.
func workerAgentName(m frontmatter.Meta) string {
	switch m.Agent {
	case "codex":
		return "codex"
	case "":
		return "claude"
	case "claude", "claude-code":
		return "claude"
	default:
		return m.Agent
	}
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

// flipStatusWithBlocker flips a task's status with optional blocker
// field handling: on transitions into blocked, blocker is the named
// reason (machine-readable convention: `scheduler:<key>`); on
// transitions out of blocked, blocker is cleared. Empty blocker on
// entry to blocked is allowed; the lint catches it as a separate
// check.
func flipStatusWithBlocker(tasksDir, slug, from, to, blocker string) error {
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
	if to == StatusBlocked {
		if blocker != "" {
			if m.Extra == nil {
				m.Extra = map[string]string{}
			}
			m.Extra["blocker"] = blocker
		}
	} else {
		delete(m.Extra, "blocker")
	}
	return os.WriteFile(path, frontmatter.Write(m, body), 0o644)
}

// blockCoordinatorGate refuses `spore task block` when the caller is
// a coordinator session. Workers (own-slug block) and operator-
// interactive sessions (env unset) pass. drop-parked-status-gate: the
// coordinator must surface attention via notification, not by parking
// work out of the runnable pool.
func blockCoordinatorGate() error {
	if os.Getenv(SessionKindEnv) == SessionKindCoordinator {
		return fmt.Errorf("coordinator session is not authorized to block tickets; flag for operator attention via notification instead")
	}
	return nil
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

// stageInitialPrompt writes the task brief to <worktree>/.wt/initial-prompt
// so ensureSession's sh-wrapped agent launch can `cat` it into the first
// user message. Called on every ensureSession (not just first-time worktree
// creation) so that a re-mint into an existing worktree (the wedge-recovery
// path) still gets a fresh prompt. Safe to overwrite: .wt/initial-prompt
// is a transient stage file, never the operator's source-of-truth brief.
// Soft-fails on a missing source brief.
func stageInitialPrompt(tasksDir, worktree, slug string) error {
	src := filepath.Join(tasksDir, slug+".md")
	body, err := os.ReadFile(src)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	promptDir := filepath.Join(worktree, ".wt")
	if err := os.MkdirAll(promptDir, 0o755); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(promptDir, "initial-prompt"), body, 0o644)
}
