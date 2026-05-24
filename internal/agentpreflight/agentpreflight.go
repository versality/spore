package agentpreflight

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/versality/spore/internal/task/frontmatter"
)

type Severity string

const (
	SeverityError Severity = "error"
	SeverityWarn  Severity = "warn"
	SeverityInfo  Severity = "info"
)

type Issue struct {
	Severity Severity `json:"severity"`
	Code     string   `json:"code"`
	Tool     string   `json:"tool,omitempty"`
	Message  string   `json:"message"`
}

func WarningLines(issues []Issue) []string {
	var lines []string
	for _, issue := range issues {
		if issue.Severity == SeverityInfo {
			continue
		}
		prefix := "warning"
		if issue.Severity == SeverityError {
			prefix = "error"
		}
		if issue.Tool != "" {
			lines = append(lines, fmt.Sprintf("%s[%s:%s]: %s", prefix, issue.Code, issue.Tool, issue.Message))
		} else {
			lines = append(lines, fmt.Sprintf("%s[%s]: %s", prefix, issue.Code, issue.Message))
		}
	}
	return lines
}

type Checker struct {
	LookPath func(string) (string, error)
	Env      func(string) string
}

func (c Checker) CheckRequiredTools(projectRoot string) []Issue {
	var issues []Issue
	for _, tool := range []string{"git", "tmux"} {
		if !c.hasTool(tool) {
			issues = append(issues, Issue{
				Severity: SeverityError,
				Code:     "missing-required-tool",
				Tool:     tool,
				Message:  tool + " is required before Spore can launch or manage agent sessions",
			})
		}
	}
	if !c.hasTool("spore") {
		issues = append(issues, Issue{
			Severity: SeverityWarn,
			Code:     "missing-spore",
			Tool:     "spore",
			Message:  "spore is not on PATH; spawned lifecycle hooks and inbox handling may not work",
		})
	}
	if projectRoot != "" {
		if _, err := os.Stat(filepath.Join(projectRoot, "justfile")); err == nil && !c.hasTool("just") {
			issues = append(issues, Issue{
				Severity: SeverityWarn,
				Code:     "missing-just",
				Tool:     "just",
				Message:  "justfile exists but just is not on PATH; merge and validation recipes may fail",
			})
		}
	}
	return issues
}

func (c Checker) CheckSetupToolHints() []Issue {
	var issues []Issue
	if missing := c.missingTools([]string{"gh", "age", "go", "gofmt", "just", "nix", "pgrep", "sh", "bash", "wt"}); len(missing) > 0 {
		issues = append(issues, Issue{
			Severity: SeverityWarn,
			Code:     "missing-recommended-tools",
			Message:  "recommended tools are not on PATH: " + strings.Join(missing, ", "),
		})
	}
	if missing := c.missingTools([]string{"bwrap", "ssh", "scp", "rsync"}); len(missing) > 0 {
		issues = append(issues, Issue{
			Severity: SeverityWarn,
			Code:     "missing-feature-tools",
			Message:  "feature-specific tools are not on PATH: " + strings.Join(missing, ", "),
		})
	}
	return issues
}

func (c Checker) CheckWorkerAgent(meta frontmatter.Meta, projectRoot string) []Issue {
	tool := workerTool(meta, c.env("SPORE_AGENT_BINARY"))
	issues := c.checkExecutable(tool, "missing-worker-agent", "selected worker agent "+tool+" is not on PATH")
	if !c.hasTool("spore") {
		issues = append(issues, Issue{
			Severity: SeverityWarn,
			Code:     "missing-spore",
			Tool:     "spore",
			Message:  "spore is not on PATH; worker hooks and inbox handling may not work",
		})
	}
	return issues
}

func (c Checker) CheckCoordinatorAgent(projectRoot string) []Issue {
	tool := coordinatorTool(projectRoot, c.env("SPORE_COORDINATOR_AGENT"), c.env("SPORE_AGENT_BINARY"))
	issues := c.checkExecutable(tool, "missing-coordinator-agent", "selected coordinator agent "+tool+" is not on PATH")
	if !c.hasTool("spore") {
		issues = append(issues, Issue{
			Severity: SeverityWarn,
			Code:     "missing-spore",
			Tool:     "spore",
			Message:  "spore is not on PATH; coordinator hooks may not work in spawned shells",
		})
	}
	return issues
}

func (c Checker) checkExecutable(command, code, message string) []Issue {
	tool := firstExecutableToken(command)
	if tool == "" {
		return []Issue{{
			Severity: SeverityError,
			Code:     code,
			Message:  "selected command is empty",
		}}
	}
	if c.hasTool(tool) {
		return nil
	}
	return []Issue{{
		Severity: SeverityError,
		Code:     code,
		Tool:     tool,
		Message:  message,
	}}
}

func (c Checker) hasTool(tool string) bool {
	if tool == "" {
		return false
	}
	if filepath.IsAbs(tool) || strings.ContainsRune(tool, filepath.Separator) {
		_, err := os.Stat(tool)
		return err == nil
	}
	look := c.LookPath
	if look == nil {
		look = defaultLookPath
	}
	_, err := look(tool)
	return err == nil
}

func (c Checker) missingTools(tools []string) []string {
	var missing []string
	for _, tool := range tools {
		if !c.hasTool(tool) {
			missing = append(missing, tool)
		}
	}
	return missing
}

func (c Checker) env(key string) string {
	if c.Env != nil {
		return c.Env(key)
	}
	return os.Getenv(key)
}

func workerTool(meta frontmatter.Meta, override string) string {
	if override != "" {
		return override
	}
	switch meta.Agent {
	case "", "claude", "claude-code":
		return "claude"
	case "codex":
		return "codex"
	default:
		return meta.Agent
	}
}

func coordinatorTool(projectRoot, coordinatorEnv, workerEnv string) string {
	if coordinatorEnv != "" {
		return coordinatorEnv
	}
	if workerEnv != "" {
		return workerEnv
	}
	driver := readCoordinatorDriver(projectRoot)
	if driver == "" || driver == "claude" {
		return "claude"
	}
	return driver
}

func readCoordinatorDriver(projectRoot string) string {
	if projectRoot == "" {
		return ""
	}
	body, err := os.ReadFile(filepath.Join(projectRoot, "spore.toml"))
	if err != nil {
		return ""
	}
	inCoordinator := false
	for _, line := range strings.Split(string(body), "\n") {
		line = strings.TrimSpace(stripComment(line))
		if line == "" {
			continue
		}
		if strings.HasPrefix(line, "[") && strings.HasSuffix(line, "]") {
			inCoordinator = strings.TrimSpace(line[1:len(line)-1]) == "coordinator"
			continue
		}
		if !inCoordinator || !strings.HasPrefix(line, "driver") {
			continue
		}
		parts := strings.SplitN(line, "=", 2)
		if len(parts) != 2 {
			return ""
		}
		return strings.Trim(strings.TrimSpace(parts[1]), `"'`)
	}
	return ""
}

func firstExecutableToken(command string) string {
	fields := strings.Fields(strings.TrimSpace(command))
	if len(fields) == 0 {
		return ""
	}
	return fields[0]
}

func stripComment(s string) string {
	if idx := strings.IndexByte(s, '#'); idx >= 0 {
		return s[:idx]
	}
	return s
}

func defaultLookPath(file string) (string, error) {
	if file == "" {
		return "", errors.New("empty file")
	}
	return exec.LookPath(file)
}
