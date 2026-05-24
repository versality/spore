package agentpreflight

import (
	"os/exec"
	"testing"

	"github.com/versality/spore/internal/task/frontmatter"
	"github.com/versality/spore/internal/testpath"
)

func TestCheckWorkerAgentWithControlledPathReportsOnlyCodex(t *testing.T) {
	h := testpath.Install(t, testpath.Options{
		FakeTools: map[string]string{
			"git":   "#!/bin/sh\nexit 0\n",
			"tmux":  "#!/bin/sh\nexit 0\n",
			"spore": "#!/bin/sh\nexit 0\n",
		},
	})
	t.Setenv("PATH", h.BinDir)
	issues := Checker{LookPath: exec.LookPath}.CheckWorkerAgent(frontmatter.Meta{Agent: "codex"}, "")
	assertIssue(t, issues, SeverityError, "missing-worker-agent", "codex")
	if hasIssue(issues, "missing-required-tool", "git") || hasIssue(issues, "missing-required-tool", "tmux") {
		t.Fatalf("issues = %+v, want only codex readiness failure", issues)
	}
}
