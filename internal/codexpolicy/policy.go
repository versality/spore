package codexpolicy

import "fmt"

const (
	DefaultAgentEffort       = "medium"
	DefaultCoordinatorEffort = "high"
)

// NormalizeEffort accepts Codex's reasoning effort vocabulary plus the
// operator-friendly very-high aliases used in task frontmatter.
func NormalizeEffort(effort, defaultEffort string) (string, error) {
	switch effort {
	case "":
		return defaultEffort, nil
	case "low", "medium", "high", "xhigh":
		return effort, nil
	case "very-high", "very_high":
		return "xhigh", nil
	default:
		return "", fmt.Errorf("codex effort must be low, medium, high, xhigh, or very-high; got: %s", effort)
	}
}

// EffortForTask maps task frontmatter to a Codex reasoning effort. An
// explicit effort wins; otherwise complexity keeps light/medium work on
// medium reasoning and heavy/unknown work on high.
func EffortForTask(effort, complexity string) (string, error) {
	if effort != "" {
		return NormalizeEffort(effort, DefaultAgentEffort)
	}
	switch complexity {
	case "light", "medium":
		return "medium", nil
	case "heavy":
		return "high", nil
	default:
		return "high", nil
	}
}

func ReasoningConfig(effort string) string {
	return fmt.Sprintf("model_reasoning_effort=\"%s\"", effort)
}

// InteractiveArgs returns the Codex CLI argv shape Spore uses for tmux
// worker and coordinator launches.
func InteractiveArgs(model, effort string) []string {
	args := []string{
		"codex",
		"--dangerously-bypass-approvals-and-sandbox",
		"--no-alt-screen",
		"--disable", "apps",
	}
	if model != "" {
		args = append(args, "-m", model)
	}
	args = append(args, "-c", ReasoningConfig(effort))
	return args
}
