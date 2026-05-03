package bootstrap

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// credIndicators are filenames or directory basenames that suggest the
// project carries secrets / env-bound config. When one is present, the
// agent instructions must mention how the agent obtains the value
// (creds-broker reference, .envrc usage, agenix path, etc).
var credIndicators = []string{
	".env",
	".envrc",
	"secrets",
	"credentials",
	".env.example",
	".env.template",
}

// credKeywords are the substrings the detector looks for in the agent
// instructions to confirm the operator has documented the secret
// surface. Lowercase match.
var credKeywords = []string{
	"creds-broker",
	"creds broker",
	"credential",
	"secret",
	"agenix",
	".env",
	"vault",
	"envrc",
	"environment variable",
}

func detectCredsWired(root string) (string, error) {
	if root == "" {
		return "", errors.New("creds-wired: empty root")
	}
	var found []string
	for _, marker := range credIndicators {
		_, err := os.Stat(filepath.Join(root, marker))
		if err == nil {
			found = append(found, marker)
		} else if !os.IsNotExist(err) {
			return "", err
		}
		// also detect *.age files in any dir at depth 1
		if marker == "secrets" {
			matches, _ := filepath.Glob(filepath.Join(root, "secrets", "*.age"))
			if len(matches) > 0 {
				found = append(found, fmt.Sprintf("secrets/*.age (%d)", len(matches)))
			}
		}
	}
	if len(found) == 0 {
		return "no secret surface detected; nothing to document", nil
	}
	b, paths, err := readAgentInstructions(root)
	if err != nil {
		return "", err
	}
	lower := strings.ToLower(string(b))
	for _, kw := range credKeywords {
		if strings.Contains(lower, kw) {
			return fmt.Sprintf("documented in %s (matched %q); detected %v", paths, kw, found), nil
		}
	}
	return "", fmt.Errorf("found secret surface %v but agent instructions mention none of %v; document how the agent obtains values without storing them", found, credKeywords)
}

func readAgentInstructions(root string) ([]byte, string, error) {
	var parts []string
	var paths []string
	for _, name := range []string{"CLAUDE.md", "AGENTS.md"} {
		b, err := os.ReadFile(filepath.Join(root, name))
		if err == nil {
			parts = append(parts, string(b))
			paths = append(paths, name)
			continue
		}
		if !os.IsNotExist(err) {
			return nil, "", err
		}
	}
	if len(parts) > 0 {
		return []byte(strings.Join(parts, "\n")), strings.Join(paths, " / "), nil
	}
	return nil, "", fmt.Errorf("agent instructions are absent; complete repo-mapped first")
}
