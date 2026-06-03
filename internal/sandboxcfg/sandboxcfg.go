// Package sandboxcfg loads the [sandbox] section out of spore.toml
// and an optional per-user override at ~/.config/spore/sandbox.toml.
// The merged Config is what spore-sandbox consults when assembling its
// bwrap policy and HTTPS CONNECT allowlist.
//
// Merge precedence, weakest to strongest:
//
//  1. compiled-in defaults (currently empty)
//  2. ~/.config/spore/sandbox.toml (user override)
//  3. <project>/spore.toml [sandbox] (project)
//  4. CLI flags on the sandbox invocation
//
// The sandbox wires (1)..(3) here; (4) merges on top in main.go.
package sandboxcfg

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/versality/spore/internal/sporetoml"
)

// Config carries the sandbox knobs. All fields are optional; a zero
// Config is a valid empty (disabled) policy. Enabled gates whether the
// worker spawn path wraps the agent at all; the list fields shape the
// bwrap policy and CONNECT allowlist once it does.
type Config struct {
	Enabled    bool
	AllowHosts []string
	RW         []string
	RO         []string
}

// Merge appends b's lists onto a, deduplicating, and ORs Enabled so any
// layer turning the sandbox on wins. The result preserves a's order
// then b's, so weaker layers come first and the CLI's late-add
// overrides land last.
func Merge(a, b Config) Config {
	return Config{
		Enabled:    a.Enabled || b.Enabled,
		AllowHosts: appendUnique(a.AllowHosts, b.AllowHosts),
		RW:         appendUnique(a.RW, b.RW),
		RO:         appendUnique(a.RO, b.RO),
	}
}

func appendUnique(dst, src []string) []string {
	seen := make(map[string]struct{}, len(dst)+len(src))
	out := make([]string, 0, len(dst)+len(src))
	for _, x := range dst {
		if _, ok := seen[x]; ok {
			continue
		}
		seen[x] = struct{}{}
		out = append(out, x)
	}
	for _, x := range src {
		if _, ok := seen[x]; ok {
			continue
		}
		seen[x] = struct{}{}
		out = append(out, x)
	}
	return out
}

// LoadForProject returns the merged user + project config for the
// given project root. A missing file at either layer is not an error;
// a malformed file at either layer is.
func LoadForProject(projectRoot string) (Config, error) {
	var out Config

	if home, err := os.UserHomeDir(); err == nil {
		userPath := filepath.Join(home, ".config", "spore", "sandbox.toml")
		c, err := loadFile(userPath)
		if err != nil {
			return Config{}, err
		}
		out = Merge(out, c)
	}

	projectPath := filepath.Join(projectRoot, "spore.toml")
	c, err := loadFile(projectPath)
	if err != nil {
		return Config{}, err
	}
	out = Merge(out, c)

	return out, nil
}

// LoadFromString parses a TOML blob (test entry point). Returns the
// [sandbox] section as Config; sections other than [sandbox] are
// ignored.
func LoadFromString(content string) (Config, error) {
	return parseSandboxTOML(content)
}

func loadFile(path string) (Config, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return Config{}, nil
		}
		return Config{}, fmt.Errorf("sandboxcfg: read %s: %w", path, err)
	}
	cfg, err := parseSandboxTOML(string(b))
	if err != nil {
		return Config{}, fmt.Errorf("sandboxcfg: parse %s: %w", path, err)
	}
	return cfg, nil
}

// parseSandboxTOML reads a tiny TOML subset: only the [sandbox]
// section, only `key = ["a", "b"]` array-of-string scalars. Lines
// outside [sandbox], blanks, and `#` comments are ignored. Unknown
// keys inside [sandbox] are an error (typos otherwise vanish into
// silent no-ops). Each value MUST be a single-line bracketed list of
// double-quoted strings; multi-line arrays are not supported.
func parseSandboxTOML(content string) (Config, error) {
	var cfg Config
	err := sporetoml.ScanSections(content, func(l sporetoml.Line) error {
		if l.Section != "sandbox" {
			return nil
		}
		key, val, ok := sporetoml.SplitKeyValue(l.Text)
		if !ok {
			return fmt.Errorf("line %d: malformed entry %q", l.LineNum, l.Text)
		}
		if key == "enabled" {
			b, err := parseBool(val)
			if err != nil {
				return fmt.Errorf("line %d: key %q: %w", l.LineNum, key, err)
			}
			cfg.Enabled = b
			return nil
		}
		items, err := parseStringList(val)
		if err != nil {
			return fmt.Errorf("line %d: key %q: %w", l.LineNum, key, err)
		}
		switch key {
		case "allow_hosts":
			cfg.AllowHosts = append(cfg.AllowHosts, items...)
		case "rw":
			cfg.RW = append(cfg.RW, items...)
		case "ro":
			cfg.RO = append(cfg.RO, items...)
		default:
			return fmt.Errorf("line %d: unknown key %q in [sandbox] (known: enabled, allow_hosts, rw, ro)", l.LineNum, key)
		}
		return nil
	})
	if err != nil {
		return Config{}, err
	}
	return cfg, nil
}

// parseBool accepts the bare TOML booleans true/false.
func parseBool(s string) (bool, error) {
	switch strings.TrimSpace(s) {
	case "true":
		return true, nil
	case "false":
		return false, nil
	default:
		return false, fmt.Errorf("want true or false, got %q", s)
	}
}

// parseStringList accepts the single-line shape `["a", "b", "c"]`
// (whitespace and a trailing comma tolerated). Empty list `[]` is
// valid and parses to nil.
func parseStringList(s string) ([]string, error) {
	if !strings.HasPrefix(s, "[") || !strings.HasSuffix(s, "]") {
		return nil, fmt.Errorf("want array, got %q", s)
	}
	body := strings.TrimSpace(s[1 : len(s)-1])
	if body == "" {
		return nil, nil
	}
	var out []string
	for raw := range strings.SplitSeq(body, ",") {
		raw = strings.TrimSpace(raw)
		if raw == "" {
			continue
		}
		if len(raw) < 2 || raw[0] != '"' || raw[len(raw)-1] != '"' {
			return nil, fmt.Errorf("want double-quoted string element, got %q", raw)
		}
		out = append(out, raw[1:len(raw)-1])
	}
	return out, nil
}
