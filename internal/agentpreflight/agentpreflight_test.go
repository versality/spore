package agentpreflight

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/versality/spore/internal/task/frontmatter"
)

func TestCheckWorkerAgentMissingCodex(t *testing.T) {
	c := fakeChecker("spore")
	issues := c.CheckWorkerAgent(frontmatter.Meta{Agent: "codex"}, "")
	assertIssue(t, issues, SeverityError, "missing-worker-agent", "codex")
}

func TestCheckWorkerAgentMissingClaude(t *testing.T) {
	c := fakeChecker("spore")
	issues := c.CheckWorkerAgent(frontmatter.Meta{Agent: "claude"}, "")
	assertIssue(t, issues, SeverityError, "missing-worker-agent", "claude")
}

func TestCheckWorkerAgentMissingCustom(t *testing.T) {
	c := fakeChecker("spore")
	issues := c.CheckWorkerAgent(frontmatter.Meta{Agent: "missing-tool"}, "")
	assertIssue(t, issues, SeverityError, "missing-worker-agent", "missing-tool")
}

func TestCheckWorkerAgentReportsMissingSpore(t *testing.T) {
	c := fakeChecker("codex")
	issues := c.CheckWorkerAgent(frontmatter.Meta{Agent: "codex"}, "")
	assertIssue(t, issues, SeverityWarn, "missing-spore", "spore")
}

func TestCheckWorkerAgentReady(t *testing.T) {
	c := fakeChecker("codex", "spore")
	issues := c.CheckWorkerAgent(frontmatter.Meta{Agent: "codex"}, "")
	if hasSeverity(issues, SeverityError) {
		t.Fatalf("issues = %+v, want no errors", issues)
	}
}

func TestCheckWorkerAgentOverrideUsesFirstToken(t *testing.T) {
	c := fakeChecker("sleep", "spore")
	c.Env = func(key string) string {
		if key == "SPORE_AGENT_BINARY" {
			return "sleep 30"
		}
		return ""
	}
	issues := c.CheckWorkerAgent(frontmatter.Meta{Agent: "codex"}, "")
	if hasSeverity(issues, SeverityError) {
		t.Fatalf("issues = %+v, want no errors", issues)
	}
}

func TestCheckCoordinatorAgentFromConfig(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "spore.toml"), []byte("[coordinator]\ndriver = \"codex\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	c := fakeChecker("spore")
	issues := c.CheckCoordinatorAgent(dir)
	assertIssue(t, issues, SeverityError, "missing-coordinator-agent", "codex")
}

func TestCheckRequiredTools(t *testing.T) {
	c := fakeChecker("git", "tmux")
	issues := c.CheckRequiredTools("")
	assertIssue(t, issues, SeverityWarn, "missing-spore", "spore")
	if hasIssue(issues, "missing-required-tool", "git") || hasIssue(issues, "missing-required-tool", "tmux") {
		t.Fatalf("issues = %+v, want git and tmux ready", issues)
	}
}

func TestCheckSetupToolHintsGroupsMissingTools(t *testing.T) {
	c := fakeChecker("git", "tmux", "spore", "claude")
	issues := c.CheckSetupToolHints()
	assertIssue(t, issues, SeverityWarn, "missing-recommended-tools", "")
	assertIssue(t, issues, SeverityWarn, "missing-feature-tools", "")
	if !issueMessageContains(issues, "missing-recommended-tools", "bash") || !issueMessageContains(issues, "missing-recommended-tools", "go") {
		t.Fatalf("issues = %+v, want recommended bash and go hints", issues)
	}
	if !issueMessageContains(issues, "missing-feature-tools", "bwrap") || !issueMessageContains(issues, "missing-feature-tools", "rsync") {
		t.Fatalf("issues = %+v, want feature-specific bwrap and rsync hints", issues)
	}
}

func TestCheckSetupToolHintsReady(t *testing.T) {
	c := fakeChecker("gh", "age", "go", "gofmt", "just", "nix", "pgrep", "sh", "bash", "wt", "bwrap", "ssh", "scp", "rsync")
	if issues := c.CheckSetupToolHints(); len(issues) != 0 {
		t.Fatalf("issues = %+v, want none", issues)
	}
}

func fakeChecker(tools ...string) Checker {
	set := map[string]bool{}
	for _, tool := range tools {
		set[tool] = true
	}
	return Checker{
		LookPath: func(file string) (string, error) {
			if set[file] {
				return "/bin/" + file, nil
			}
			return "", errors.New("missing")
		},
		Env: func(string) string { return "" },
	}
}

func assertIssue(t *testing.T, issues []Issue, severity Severity, code, tool string) {
	t.Helper()
	if !hasIssueWithSeverity(issues, severity, code, tool) {
		t.Fatalf("issues = %+v, want %s %s %s", issues, severity, code, tool)
	}
}

func hasIssueWithSeverity(issues []Issue, severity Severity, code, tool string) bool {
	for _, issue := range issues {
		if issue.Severity == severity && issue.Code == code && issue.Tool == tool {
			return true
		}
	}
	return false
}

func hasIssue(issues []Issue, code, tool string) bool {
	for _, issue := range issues {
		if issue.Code == code && issue.Tool == tool {
			return true
		}
	}
	return false
}

func issueMessageContains(issues []Issue, code, want string) bool {
	for _, issue := range issues {
		if issue.Code == code && strings.Contains(issue.Message, want) {
			return true
		}
	}
	return false
}

func hasSeverity(issues []Issue, severity Severity) bool {
	for _, issue := range issues {
		if issue.Severity == severity {
			return true
		}
	}
	return false
}
