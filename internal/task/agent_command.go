package task

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/versality/spore/internal/agentpolicy/claude"
	"github.com/versality/spore/internal/agentpolicy/codex"
	"github.com/versality/spore/internal/sandboxcfg"
	"github.com/versality/spore/internal/task/frontmatter"
)

// sandboxTargets mirrors the registry in cmd/spore-sandbox/target.go.
// spore-sandbox --exec rejects an unknown -target, so only agents with
// a registered target can be wrapped; custom binaries run unwrapped.
// Keep in sync with that file.
var sandboxTargets = map[string]bool{"claude": true, "codex": true, "opencode": true}

// maybeSandboxWrap wraps the agent argv string in `spore-sandbox --exec`
// when the project enables the sandbox and the task has not opted out
// (`sandbox: false`). allow_hosts / rw / ro come from the worktree's
// spore.toml, which --exec loads itself; the only per-spawn bind passed
// on the CLI is the main repo's .git/ (a linked worktree commits into
// the shared object store + worktrees/<slug>/ there). Returns the agent
// command unchanged when the sandbox is off, the task opted out, or the
// agent has no registered target. Errors when enabled but bwrap is
// absent rather than silently running unsandboxed.
func maybeSandboxWrap(projectRoot, worktree string, m frontmatter.Meta, agent string) (string, error) {
	cfg, err := sandboxcfg.LoadForProject(projectRoot)
	if err != nil {
		return "", fmt.Errorf("load sandbox config: %w", err)
	}
	if !cfg.Enabled || m.Extra["sandbox"] == "false" {
		return agent, nil
	}
	name := workerAgentName(m)
	if !sandboxTargets[name] {
		return agent, nil
	}
	if _, err := exec.LookPath("bwrap"); err != nil {
		return "", fmt.Errorf("sandbox enabled for %s but bwrap not found on PATH: %w", name, err)
	}
	prefix := shellJoin([]string{
		sandboxBinary(), "--exec",
		"-worktree", worktree,
		"-home", os.Getenv("HOME"),
		"-target", name,
		"-rw", filepath.Join(projectRoot, ".git"),
		"--",
	})
	return prefix + " " + agent, nil
}

// sandboxBinary resolves spore-sandbox next to the running executable
// (a deployed /usr/local/bin/spore finds its sibling), falling back to
// a bare name for PATH lookup.
func sandboxBinary() string {
	if exe, err := os.Executable(); err == nil {
		cand := filepath.Join(filepath.Dir(exe), "spore-sandbox")
		if st, err := os.Stat(cand); err == nil && !st.IsDir() {
			return cand
		}
	}
	return "spore-sandbox"
}

func workerAgentCommand(m frontmatter.Meta) (string, error) {
	if override := os.Getenv(AgentBinaryEnv); override != "" {
		return override, nil
	}
	agent := m.Agent
	if agent == "" || agent == "claude" || agent == "claude-code" {
		effort, err := claude.EffortForTask(m.Extra["effort"], m.Extra["complexity"])
		if err != nil {
			return "", err
		}
		return shellJoin(claude.InteractiveArgs(m.Extra["model"], effort)), nil
	}
	if agent != "codex" {
		return agent, nil
	}

	effort, err := codex.EffortForTask(m.Extra["effort"], m.Extra["complexity"])
	if err != nil {
		return "", err
	}
	model := m.Extra["model"]
	if model == "" {
		model = os.Getenv(CodexModelEnv)
	}
	return shellJoin(codex.InteractiveArgs(model, effort)), nil
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
