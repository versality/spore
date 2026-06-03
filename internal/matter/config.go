package matter

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/versality/spore/internal/sporetoml"
)

// EnvPrefix is the leading segment of every env-var override:
// SPORE_MATTER_<NAME>__<KEY>. NAME and KEY are upper-cased and have
// every non [A-Z0-9] rune mapped to '_'. The double underscore
// separates the matter name from the option key; this is the only
// shape that lets multi-word names (e.g. "github-issues") survive
// the round-trip without shell-name escaping. The "ENABLED" key is
// reserved and parsed as a boolean (1/true/yes/on -> true; the
// inverse turns the matter off).
const (
	EnvPrefix = "SPORE_MATTER_"
	envSep    = "__"
)

// LoadFromProject walks the project's spore.toml plus the process
// environment and returns one Config per discovered matter. The two
// sources are merged: spore.toml supplies the baseline; env vars
// (SPORE_MATTER_<NAME>_<KEY>=...) override on a per-key basis. Env-
// only matters (no [matter.<name>] section but at least one env var)
// are returned too, so the NixOS module can drive matters without
// templating spore.toml.
//
// Returned configs are sorted by Name for deterministic ordering.
func LoadFromProject(projectRoot string) ([]Config, error) {
	tomlPath := filepath.Join(projectRoot, "spore.toml")
	b, err := os.ReadFile(tomlPath)
	if err != nil && !os.IsNotExist(err) {
		return nil, fmt.Errorf("matter: read %s: %w", tomlPath, err)
	}
	configs, err := parseMatterTOML(string(b))
	if err != nil {
		return nil, fmt.Errorf("matter: parse %s: %w", tomlPath, err)
	}
	mergeEnv(configs, os.Environ())
	return sortedConfigs(configs), nil
}

// LoadFromString parses spore.toml content directly (for tests and
// for callers that already hold the bytes). Env vars are NOT merged;
// use LoadFromProject for the file + env combination.
func LoadFromString(content string) ([]Config, error) {
	configs, err := parseMatterTOML(content)
	if err != nil {
		return nil, err
	}
	return sortedConfigs(configs), nil
}

func sortedConfigs(byName map[string]*Config) []Config {
	names := make([]string, 0, len(byName))
	for n := range byName {
		names = append(names, n)
	}
	sort.Strings(names)
	out := make([]Config, 0, len(names))
	for _, n := range names {
		out = append(out, *byName[n])
	}
	return out
}

// parseMatterTOML reads a tiny TOML subset: only [matter.<name>]
// sections, only `key = value` scalar lines. Values may be bare,
// double-quoted, or single-quoted; the surrounding quotes are
// stripped. The reserved key "enabled" is parsed as a boolean
// (1/true/yes/on are true; 0/false/no/off are false) and lifted into
// Config.Enabled. Other keys land in Options as their string form.
//
// Lines outside any matter section, blanks, and `#` comments are
// ignored. Anything malformed inside a matter section is an error so
// misconfiguration surfaces loudly.
func parseMatterTOML(content string) (map[string]*Config, error) {
	out := map[string]*Config{}
	sections := map[string]*Config{}
	err := sporetoml.ScanSections(content, func(l sporetoml.Line) error {
		name, ok := matterSectionName(l.Section)
		if !ok {
			return nil
		}
		current := sections[l.Section]
		if current == nil {
			if name == "" {
				return fmt.Errorf("line %d: empty matter name in %q", l.LineNum, l.Section)
			}
			c, exists := out[name]
			if !exists {
				c = &Config{Name: name, Options: map[string]string{}}
				out[name] = c
			}
			current = c
			sections[l.Section] = c
		}
		key, raw, ok := sporetoml.SplitKeyValue(l.Text)
		if !ok {
			return fmt.Errorf("line %d: malformed entry %q", l.LineNum, l.Text)
		}
		val := sporetoml.StripQuotes(raw)
		if key == "" {
			return fmt.Errorf("line %d: empty key in %q", l.LineNum, l.Text)
		}
		if key == "enabled" {
			b, err := parseBool(val)
			if err != nil {
				return fmt.Errorf("line %d: enabled: %w", l.LineNum, err)
			}
			current.Enabled = b
			return nil
		}
		current.Options[key] = val
		return nil
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

// matterSectionName returns the trailing <name> from a TOML section
// header that opened with "matter." (e.g. "matter.linear" -> "linear").
// Sections with any other prefix are ignored (ok=false).
func matterSectionName(section string) (string, bool) {
	const prefix = "matter."
	if !strings.HasPrefix(section, prefix) {
		return "", false
	}
	return strings.TrimSpace(section[len(prefix):]), true
}

// mergeEnv overlays SPORE_MATTER_<NAME>__<KEY>=<value> entries onto
// the parsed configs. Env-only matters get a fresh Config entry so
// the NixOS module path works without spore.toml. Names are matched
// against existing TOML entries with dashes folded to underscores
// (so [matter.github-issues] in spore.toml accepts an env override
// rendered as SPORE_MATTER_GITHUB_ISSUES__REPO).
func mergeEnv(configs map[string]*Config, environ []string) {
	for _, kv := range environ {
		eq := strings.IndexByte(kv, '=')
		if eq <= 0 {
			continue
		}
		k := kv[:eq]
		v := kv[eq+1:]
		if !strings.HasPrefix(k, EnvPrefix) {
			continue
		}
		rest := k[len(EnvPrefix):]
		sep := strings.Index(rest, envSep)
		if sep <= 0 {
			continue
		}
		name := strings.ToLower(rest[:sep])
		key := strings.ToLower(rest[sep+len(envSep):])
		if name == "" || key == "" {
			continue
		}
		c := lookupOrCreateByEnvName(configs, name)
		if key == "enabled" {
			if b, err := parseBool(v); err == nil {
				c.Enabled = b
			}
			continue
		}
		c.Options[key] = v
	}
}

// lookupOrCreateByEnvName returns the Config whose normalized name
// (dashes folded to underscores) matches envName, creating a new
// entry under envName when no match exists. The envName form is the
// canonical storage key for env-only matters.
func lookupOrCreateByEnvName(configs map[string]*Config, envName string) *Config {
	if c, ok := configs[envName]; ok {
		return c
	}
	for k, c := range configs {
		if normalizeName(k) == envName {
			return c
		}
	}
	c := &Config{Name: envName, Options: map[string]string{}}
	configs[envName] = c
	return c
}

// normalizeName folds dashes to underscores so a [matter.github-issues]
// section and a SPORE_MATTER_GITHUB_ISSUES__... env var address the
// same Config. Other characters pass through.
func normalizeName(s string) string {
	return strings.ReplaceAll(s, "-", "_")
}

func parseBool(v string) (bool, error) {
	switch strings.ToLower(v) {
	case "1", "true", "yes", "on":
		return true, nil
	case "0", "false", "no", "off":
		return false, nil
	default:
		return false, fmt.Errorf("want boolean, got %q", v)
	}
}
