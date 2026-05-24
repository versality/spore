package bootstrap

import (
	"os"
	"path/filepath"
	"strings"
)

func hookSourceConfigReadiness(root, selectedAgent string) string {
	var notes []string
	switch selectedAgent {
	case "codex":
		if _, err := os.Stat(filepath.Join(root, "configs", "codex", "hooks-config.json")); err != nil {
			if os.IsNotExist(err) {
				notes = append(notes, "warning: Codex hook source config missing at configs/codex/hooks-config.json")
			}
		}
	case "claude", "claude-code", "":
		if _, err := os.Stat(filepath.Join(root, "configs", "claude", "hooks-config.json")); err != nil {
			if os.IsNotExist(err) {
				notes = append(notes, "warning: Claude hook source config missing at configs/claude/hooks-config.json")
			}
		}
	}
	for _, runtimePath := range []string{
		filepath.Join(root, ".codex", "hooks.json"),
		filepath.Join(root, ".claude", "settings.local.json"),
	} {
		if _, err := os.Stat(runtimePath); os.IsNotExist(err) {
			rel, _ := filepath.Rel(root, runtimePath)
			notes = append(notes, "info: runtime hook file "+filepath.ToSlash(rel)+" will be rendered when a session spawns")
		}
	}
	if len(notes) == 0 {
		return "hook source configs present"
	}
	return strings.Join(notes, "; ")
}
