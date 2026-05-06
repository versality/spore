package fleet

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// CoordinatorConfig captures the [coordinator] section from a project's
// spore.toml. Empty fields fall through to env-var overrides and the
// kernel defaults; nothing in this struct overrides an explicit env.
type CoordinatorConfig struct {
	// Driver is the agent provider ("claude", "codex", or a binary
	// path). When set without an explicit SPORE_COORDINATOR_AGENT it
	// supplies the binary the coordinator session execs and seeds
	// SPORE_COORDINATOR_PROVIDER for launcher scripts that dispatch
	// on provider name.
	Driver string

	// Model is the model identifier passed through to the agent
	// (claude --model <m>, codex -m <m>). Surfaced as
	// SPORE_COORDINATOR_MODEL in the session env.
	Model string

	// Brief is the role-file path. Mirrors SPORE_COORDINATOR_ROLE_FILE;
	// relative paths resolve against projectRoot.
	Brief string

	// ExternalSessionPattern is an RE2 regex matched against tmux
	// session names. When set and a live session matches, EnsureCoordinator
	// treats the coordinator role as externally provided and skips the
	// kernel spawn. Use this when an operator-side process owns the
	// coordinator under a non-spore session name (for example a helm-*
	// session running outside the kernel's spore/<project>/coordinator
	// slot). Empty disables the check and the kernel spawns its own.
	ExternalSessionPattern string
}

// LoadCoordinatorConfig reads `[coordinator]` from <projectRoot>/spore.toml.
// A missing file returns a zero CoordinatorConfig with no error so callers
// can treat absent config as "use defaults".
func LoadCoordinatorConfig(projectRoot string) (CoordinatorConfig, error) {
	tomlPath := filepath.Join(projectRoot, "spore.toml")
	b, err := os.ReadFile(tomlPath)
	if err != nil {
		if os.IsNotExist(err) {
			return CoordinatorConfig{}, nil
		}
		return CoordinatorConfig{}, fmt.Errorf("coordinator: read %s: %w", tomlPath, err)
	}
	cfg, err := parseCoordinatorTOML(string(b))
	if err != nil {
		return CoordinatorConfig{}, fmt.Errorf("coordinator: parse %s: %w", tomlPath, err)
	}
	return cfg, nil
}

// parseCoordinatorTOML reads only the [coordinator] section of a tiny
// TOML subset: bare or quoted scalar values, `# comment` lines, and
// blank lines. Anything outside [coordinator] is ignored. Malformed
// entries inside the section are an error so misconfigured spore.toml
// surfaces loudly.
func parseCoordinatorTOML(content string) (CoordinatorConfig, error) {
	var cfg CoordinatorConfig
	inSection := false
	scanner := bufio.NewScanner(strings.NewReader(content))
	for lineNum := 1; scanner.Scan(); lineNum++ {
		line := strings.TrimSpace(stripTOMLComment(scanner.Text()))
		if line == "" {
			continue
		}
		if strings.HasPrefix(line, "[") && strings.HasSuffix(line, "]") {
			inSection = strings.TrimSpace(line[1:len(line)-1]) == "coordinator"
			continue
		}
		if !inSection {
			continue
		}
		eq := strings.IndexByte(line, '=')
		if eq <= 0 {
			return CoordinatorConfig{}, fmt.Errorf("line %d: malformed entry %q", lineNum, line)
		}
		key := strings.TrimSpace(line[:eq])
		val := stripTOMLQuotes(strings.TrimSpace(line[eq+1:]))
		switch key {
		case "driver":
			cfg.Driver = val
		case "model":
			cfg.Model = val
		case "brief":
			cfg.Brief = val
		case "external_session_pattern":
			cfg.ExternalSessionPattern = val
		default:
			return CoordinatorConfig{}, fmt.Errorf("line %d: unknown key %q in [coordinator]", lineNum, key)
		}
	}
	if err := scanner.Err(); err != nil {
		return CoordinatorConfig{}, err
	}
	return cfg, nil
}

func stripTOMLComment(line string) string {
	inQuote := byte(0)
	for i := 0; i < len(line); i++ {
		ch := line[i]
		switch {
		case inQuote != 0:
			if ch == inQuote {
				inQuote = 0
			}
		case ch == '"' || ch == '\'':
			inQuote = ch
		case ch == '#':
			return line[:i]
		}
	}
	return line
}

func stripTOMLQuotes(v string) string {
	if len(v) >= 2 {
		first, last := v[0], v[len(v)-1]
		if (first == '"' && last == '"') || (first == '\'' && last == '\'') {
			return v[1 : len(v)-1]
		}
	}
	return v
}
