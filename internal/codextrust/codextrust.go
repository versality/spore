package codextrust

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

type Status struct {
	Root       string
	ConfigPath string
	Trusted    bool
}

func Inspect(root string) (Status, error) {
	if root == "" {
		return Status{}, errors.New("codex trust: project root required")
	}
	clean, err := filepath.Abs(root)
	if err != nil {
		return Status{}, fmt.Errorf("codex trust: abs root: %w", err)
	}
	clean = filepath.Clean(clean)
	configPath := filepath.Join(Home(), "config.toml")
	body, err := os.ReadFile(configPath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return Status{Root: clean, ConfigPath: configPath}, nil
		}
		return Status{}, fmt.Errorf("codex trust: read %s: %w", configPath, err)
	}
	return Status{
		Root:       clean,
		ConfigPath: configPath,
		Trusted:    configTrustsRoot(body, clean),
	}, nil
}

func Home() string {
	if v := os.Getenv("CODEX_HOME"); v != "" {
		return v
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ".codex"
	}
	return filepath.Join(home, ".codex")
}

func configTrustsRoot(body []byte, root string) bool {
	section := ""
	for _, raw := range strings.Split(string(body), "\n") {
		line := strings.TrimSpace(stripComment(raw))
		if line == "" {
			continue
		}
		if strings.HasPrefix(line, "[") && strings.HasSuffix(line, "]") {
			section = parseProjectSection(line)
			continue
		}
		if section != root || !strings.HasPrefix(line, "trust_level") {
			continue
		}
		parts := strings.SplitN(line, "=", 2)
		if len(parts) != 2 {
			continue
		}
		value := strings.TrimSpace(parts[1])
		if unquoted, err := strconv.Unquote(value); err == nil {
			value = unquoted
		}
		return value == "trusted"
	}
	return false
}

func parseProjectSection(line string) string {
	name := strings.TrimSpace(line[1 : len(line)-1])
	const prefix = "projects."
	if !strings.HasPrefix(name, prefix) {
		return ""
	}
	key := strings.TrimSpace(strings.TrimPrefix(name, prefix))
	if unquoted, err := strconv.Unquote(key); err == nil {
		key = unquoted
	}
	return filepath.Clean(key)
}

func stripComment(s string) string {
	inQuote := false
	escaped := false
	for i, r := range s {
		if escaped {
			escaped = false
			continue
		}
		if r == '\\' && inQuote {
			escaped = true
			continue
		}
		if r == '"' {
			inQuote = !inQuote
			continue
		}
		if r == '#' && !inQuote {
			return s[:i]
		}
	}
	return s
}
