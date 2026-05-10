package claudepolicy

import "fmt"

// NormalizeEffort accepts Claude's reasoning effort vocabulary plus
// the operator-friendly very-high aliases. Empty input returns
// defaultEffort verbatim (which may itself be empty: Claude's own
// default applies and no --effort flag is passed).
func NormalizeEffort(effort, defaultEffort string) (string, error) {
	switch effort {
	case "":
		return defaultEffort, nil
	case "low", "medium", "high", "xhigh", "max":
		return effort, nil
	case "very-high", "very_high":
		return "xhigh", nil
	default:
		return "", fmt.Errorf("claude effort must be low, medium, high, xhigh, max, or very-high; got: %s", effort)
	}
}
