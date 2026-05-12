package claudepolicy

import "fmt"

const DefaultAgentEffort = "default"

func NormalizeEffort(effort, defaultEffort string) (string, error) {
	switch effort {
	case "":
		return defaultEffort, nil
	case "default", "low", "medium", "high", "xhigh", "max":
		return effort, nil
	case "very-high", "very_high":
		return "xhigh", nil
	default:
		return "", fmt.Errorf("claude effort must be default, low, medium, high, xhigh, max, or very-high; got: %s", effort)
	}
}

func EffortForTask(effort, complexity string) (string, error) {
	if effort != "" {
		return NormalizeEffort(effort, DefaultAgentEffort)
	}
	switch complexity {
	case "light", "medium":
		return "medium", nil
	case "heavy":
		return "high", nil
	case "":
		return DefaultAgentEffort, nil
	default:
		return "high", nil
	}
}

func InteractiveArgs(model, effort string) []string {
	args := []string{"claude", "--dangerously-skip-permissions"}
	if effort != "" && effort != DefaultAgentEffort {
		args = append(args, "--effort", effort)
	}
	return args
}
