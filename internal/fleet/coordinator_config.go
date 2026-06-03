package fleet

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/versality/spore/internal/sporetoml"
	"github.com/versality/spore/internal/task"
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
	// coordinator under a non-spore session name (for example a pilot-*
	// session running outside the kernel's spore/<project>/coordinator
	// slot). Empty disables the check and the kernel spawns its own.
	ExternalSessionPattern string
}

// LoadCoordinatorConfig reads `[coordinator]` from <projectRoot>/spore.toml.
// A missing file returns a zero CoordinatorConfig with no error so callers
// can treat absent config as "use defaults".
func LoadCoordinatorConfig(projectRoot string) (CoordinatorConfig, error) {
	tomlPath := filepath.Join(task.MainCheckoutRoot(projectRoot), "spore.toml")
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
	err := sporetoml.ScanSections(content, func(l sporetoml.Line) error {
		if l.Section != "coordinator" {
			return nil
		}
		key, raw, ok := sporetoml.SplitKeyValue(l.Text)
		if !ok {
			return fmt.Errorf("line %d: malformed entry %q", l.LineNum, l.Text)
		}
		val := sporetoml.StripQuotes(raw)
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
			return fmt.Errorf("line %d: unknown key %q in [coordinator]", l.LineNum, key)
		}
		return nil
	})
	if err != nil {
		return CoordinatorConfig{}, err
	}
	return cfg, nil
}
