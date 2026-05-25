package task

import (
	"os"
	"path/filepath"
	"strings"

	"github.com/versality/spore/internal/agentpreflight"
	"github.com/versality/spore/internal/codextrust"
	"github.com/versality/spore/internal/hooks/settings"
)

func codexReadinessIssues(projectRoot string) []agentpreflight.Issue {
	var issues []agentpreflight.Issue
	trust, err := codextrust.Inspect(projectRoot)
	if err != nil {
		issues = append(issues, agentpreflight.Issue{Severity: agentpreflight.SeverityError, Code: "codex-trust-read-failed", Tool: "codex", Message: err.Error()})
	} else if !trust.Trusted {
		issues = append(issues, agentpreflight.Issue{Severity: agentpreflight.SeverityError, Code: "codex-project-untrusted", Tool: "codex", Message: "trust Codex root project " + trust.Root + " before starting Codex tasks"})
	}
	source := filepath.Join(projectRoot, "configs", "codex", "hooks-config.json")
	runtime := filepath.Join(projectRoot, ".codex", "hooks.json")
	rendered, ok, err := settings.RenderCodex(source, SessionKindCoordinator)
	if err != nil {
		issues = append(issues, agentpreflight.Issue{Severity: agentpreflight.SeverityError, Code: "codex-hooks-render-failed", Tool: "codex", Message: err.Error()})
	} else if !ok {
		issues = append(issues, agentpreflight.Issue{Severity: agentpreflight.SeverityError, Code: "missing-codex-hooks-config", Tool: "codex", Message: source + " is missing"})
	} else {
		current, err := os.ReadFile(runtime)
		if err != nil {
			if os.IsNotExist(err) {
				issues = append(issues, agentpreflight.Issue{Severity: agentpreflight.SeverityError, Code: "missing-codex-root-hooks", Tool: "codex", Message: runtime + " is missing; run spore setup or spawn coordinator first"})
			} else {
				issues = append(issues, agentpreflight.Issue{Severity: agentpreflight.SeverityError, Code: "codex-root-hooks-unreadable", Tool: "codex", Message: err.Error()})
			}
		} else if strings.TrimSpace(string(current)) != strings.TrimSpace(string(rendered)) {
			issues = append(issues, agentpreflight.Issue{Severity: agentpreflight.SeverityError, Code: "stale-codex-root-hooks", Tool: "codex", Message: runtime + " differs from rendered adapter"})
		}
	}
	return issues
}
