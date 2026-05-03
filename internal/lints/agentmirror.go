package lints

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
)

// AgentMirror keeps Codex-facing AGENTS.md files in lockstep with
// the CLAUDE.md files that already define agent instructions.
type AgentMirror struct{}

func (AgentMirror) Name() string { return "agents-mirror" }

func (AgentMirror) Run(root string) ([]Issue, error) {
	files, err := listFiles(root, map[string]bool{"CLAUDE.md": true})
	if err != nil {
		return nil, err
	}
	var issues []Issue
	for _, claude := range files {
		if filepath.Base(claude) != "CLAUDE.md" {
			continue
		}
		agents := filepath.ToSlash(filepath.Join(filepath.Dir(claude), "AGENTS.md"))
		if filepath.Dir(claude) == "." {
			agents = "AGENTS.md"
		}
		claudeBytes, err := os.ReadFile(filepath.Join(root, claude))
		if err != nil {
			return nil, err
		}
		agentsBytes, err := os.ReadFile(filepath.Join(root, agents))
		if err != nil {
			if os.IsNotExist(err) {
				issues = append(issues, Issue{
					Path:    agents,
					Message: fmt.Sprintf("missing AGENTS.md mirror for %s", claude),
				})
				continue
			}
			return nil, err
		}
		if !bytes.Equal(agentsBytes, claudeBytes) {
			issues = append(issues, Issue{
				Path:    agents,
				Message: fmt.Sprintf("drift vs %s; copy the instruction mirror exactly", claude),
			})
		}
	}
	return issues, nil
}
