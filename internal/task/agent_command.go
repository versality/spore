package task

import (
	"os"
	"strings"

	"github.com/versality/spore/internal/agentpolicy/claude"
	"github.com/versality/spore/internal/agentpolicy/codex"
	"github.com/versality/spore/internal/task/frontmatter"
)

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
