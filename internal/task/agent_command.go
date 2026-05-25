package task

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/versality/spore/claudepolicy"
	"github.com/versality/spore/codexpolicy"
	"github.com/versality/spore/internal/task/frontmatter"
)

func resolveWorkerAgent(m frontmatter.Meta, projectRoot string) (string, error) {
	if m.Agent != "" {
		return m.Agent, nil
	}
	cfg, err := readWorkerAgentConfig(projectRoot)
	if err != nil {
		return "", err
	}
	if cfg.rules != nil {
		if c := m.Extra["complexity"]; c != "" {
			if agent := cfg.rules[c]; agent != "" {
				return agent, nil
			}
		}
	}
	if cfg.defaultAgent != "" {
		return cfg.defaultAgent, nil
	}
	return "", nil
}

type workerAgentConfig struct {
	defaultAgent string
	rules        map[string]string
}

func readWorkerAgentConfig(projectRoot string) (workerAgentConfig, error) {
	if projectRoot == "" {
		return workerAgentConfig{}, nil
	}
	body, err := os.ReadFile(filepath.Join(projectRoot, "spore.toml"))
	if err != nil {
		if os.IsNotExist(err) {
			return workerAgentConfig{}, nil
		}
		return workerAgentConfig{}, err
	}
	var cfg workerAgentConfig
	section := ""
	for lineNum, raw := range strings.Split(string(body), "\n") {
		line := strings.TrimSpace(stripTOMLComment(raw))
		if line == "" {
			continue
		}
		if strings.HasPrefix(line, "[") && strings.HasSuffix(line, "]") {
			section = strings.TrimSpace(line[1 : len(line)-1])
			continue
		}
		eq := strings.IndexByte(line, '=')
		if eq <= 0 {
			if section == "fleet.workers" || section == "fleet.workers.rules" {
				return workerAgentConfig{}, fmt.Errorf("workers: line %d: malformed entry %q", lineNum+1, line)
			}
			continue
		}
		key := strings.TrimSpace(line[:eq])
		val := strings.Trim(strings.TrimSpace(line[eq+1:]), `"'`)
		switch section {
		case "fleet.workers":
			if key != "default" {
				return workerAgentConfig{}, fmt.Errorf("workers: line %d: unknown key %q in [fleet.workers]", lineNum+1, key)
			}
			cfg.defaultAgent = val
		case "fleet.workers.rules":
			if cfg.rules == nil {
				cfg.rules = map[string]string{}
			}
			cfg.rules[key] = val
		}
	}
	return cfg, nil
}

func stripTOMLComment(line string) string {
	inSingle := false
	inDouble := false
	for i, r := range line {
		switch r {
		case '\'':
			if !inDouble {
				inSingle = !inSingle
			}
		case '"':
			if !inSingle {
				inDouble = !inDouble
			}
		case '#':
			if !inSingle && !inDouble {
				return line[:i]
			}
		}
	}
	return line
}

func workerAgentCommand(m frontmatter.Meta, projectRoot string) (string, error) {
	if override := os.Getenv(AgentBinaryEnv); override != "" {
		return override, nil
	}
	agent, err := resolveWorkerAgent(m, projectRoot)
	if err != nil {
		return "", err
	}
	if agent == "" || agent == "claude" || agent == "claude-code" {
		effort, err := claudepolicy.EffortForTask(m.Extra["effort"], m.Extra["complexity"])
		if err != nil {
			return "", err
		}
		return shellJoin(claudepolicy.InteractiveArgs(m.Extra["model"], effort)), nil
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

// workerAgentName returns the window name to use for the spawned
// tmux agent window. Mirrors workerAgentCommand's agent-resolution
// but yields just the agent label ("claude" / "codex") so the fleet
// liveness check (which expects window name == agent) sees the
// window as healthy regardless of the binary's wrapper basename.
func workerAgentName(m frontmatter.Meta, projectRoot string) (string, error) {
	agent, err := resolveWorkerAgent(m, projectRoot)
	if err != nil {
		return "", err
	}
	switch agent {
	case "codex":
		return "codex", nil
	case "":
		return "claude", nil
	case "claude", "claude-code":
		return "claude", nil
	default:
		return agent, nil
	}
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
