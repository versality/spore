// Package matter is the ticket-source plug point for the fleet
// reconciler. A "matter" is an external place where work intents live
// (Linear, Jira, GitHub Issues, ...). Each adapter implements Source
// and brings ready intents on-disk as tasks/<slug>.md so the existing
// reconcile loop can pick them up.
//
// The package itself is small on purpose: it owns the interface, the
// per-pass Stats shape, and the spore.toml loader that materialises a
// list of configured Sources. Concrete adapters live in subpackages
// (e.g. internal/matter/linear).
package matter

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Source pulls ready intents from an external system into tasks/ and
// pushes terminal status (done) back. Sync is one pass; the fleet
// reconciler calls it before each reconcile tick.
type Source interface {
	// Name is a stable short identifier for logs ("linear",
	// "github-issues"). One per source kind.
	Name() string
	// Sync runs one full pass: pull ready intents into projectRoot's
	// tasks/, push done status back. tasksDir is relative to
	// projectRoot. Implementations must be idempotent.
	Sync(projectRoot, tasksDir string) (Stats, error)
}

// Stats is the per-pass outcome surfaced to the operator. Slug lists
// are sorted; counts cover transitions that touched the external
// system on this pass (re-runs collapse to zero).
type Stats struct {
	// Source is the Name() of the adapter that produced these stats.
	Source string
	// Created lists slugs created on disk this pass from new ready
	// intents.
	Created []string
	// AdoptedReady lists external IDs whose ready->in-progress
	// transition was pushed this pass.
	AdoptedReady []string
	// PushedDone lists slugs whose linked intent was advanced to the
	// done state this pass.
	PushedDone []string
}

// LinearConfig is the parsed shape of a `[matter.linear]` section in
// spore.toml. APIKeyEnv names an env var; APIKeyFile names a file
// (typically systemd LoadCredential under $CREDENTIALS_DIRECTORY).
// Either is enough; APIKeyFile wins when both are set.
type LinearConfig struct {
	Team            string
	ReadyState      string
	InProgressState string
	DoneState       string
	APIKeyEnv       string
	APIKeyFile      string
	Endpoint        string
}

// LinearTOMLEnv overrides the path to the TOML file the loader reads
// `[matter.linear]` from. The NixOS module wires it to a generated
// path in /nix/store carrying the non-secret fields (team, states,
// endpoint, api_key_file). The API key itself still flows through
// systemd LoadCredential and never lands in the store.
const LinearTOMLEnv = "SPORE_MATTER_TOML"

// LoadLinearConfig reads `[matter.linear]` from $SPORE_MATTER_TOML
// when set, else from `${projectRoot}/spore.toml`. Returns (nil, nil)
// when neither file exists or the section is missing; callers treat
// that as "no Linear matter configured" and skip the sync. Defaults
// applied:
//
//   - ready_state         = "Ready"
//   - in_progress_state   = "In Progress"
//   - done_state          = "Done"
//   - endpoint            = "https://api.linear.app/graphql"
//
// team is required: returns an error when [matter.linear] is present
// but team is empty, since a Linear sync without a team scope would
// dump every workspace ticket on the floor.
func LoadLinearConfig(projectRoot string) (*LinearConfig, error) {
	tomlPath := os.Getenv(LinearTOMLEnv)
	if tomlPath == "" {
		tomlPath = filepath.Join(projectRoot, "spore.toml")
	}
	b, err := os.ReadFile(tomlPath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	kv, present, err := parseSection(string(b), "matter.linear")
	if err != nil {
		return nil, fmt.Errorf("matter: parse %s: %w", tomlPath, err)
	}
	if !present {
		return nil, nil
	}
	cfg := &LinearConfig{
		Team:            kv["team"],
		ReadyState:      defaulted(kv["ready_state"], "Ready"),
		InProgressState: defaulted(kv["in_progress_state"], "In Progress"),
		DoneState:       defaulted(kv["done_state"], "Done"),
		APIKeyEnv:       kv["api_key_env"],
		APIKeyFile:      kv["api_key_file"],
		Endpoint:        defaulted(kv["endpoint"], "https://api.linear.app/graphql"),
	}
	if cfg.Team == "" {
		return nil, fmt.Errorf("matter.linear: team is required (e.g. team = \"MAR\")")
	}
	if cfg.APIKeyEnv == "" && cfg.APIKeyFile == "" {
		return nil, fmt.Errorf("matter.linear: api_key_env or api_key_file is required")
	}
	return cfg, nil
}

// ResolveAPIKey reads the Linear API key from APIKeyFile when set
// (joined under $CREDENTIALS_DIRECTORY when the path is relative),
// else from APIKeyEnv. Returns the trimmed key.
func (c *LinearConfig) ResolveAPIKey() (string, error) {
	if c.APIKeyFile != "" {
		p := c.APIKeyFile
		if !filepath.IsAbs(p) {
			if dir := os.Getenv("CREDENTIALS_DIRECTORY"); dir != "" {
				p = filepath.Join(dir, p)
			}
		}
		b, err := os.ReadFile(p)
		if err != nil {
			return "", fmt.Errorf("matter.linear: read api_key_file %s: %w", p, err)
		}
		key := strings.TrimSpace(string(b))
		if key == "" {
			return "", fmt.Errorf("matter.linear: api_key_file %s is empty", p)
		}
		return key, nil
	}
	key := strings.TrimSpace(os.Getenv(c.APIKeyEnv))
	if key == "" {
		return "", fmt.Errorf("matter.linear: env %s is empty or unset", c.APIKeyEnv)
	}
	return key, nil
}

func defaulted(v, fallback string) string {
	if strings.TrimSpace(v) == "" {
		return fallback
	}
	return v
}

// parseSection reads a tiny TOML subset focused on a single named
// section header (e.g. "matter.linear"). Only `key = "value"` and
// `key = value` scalar lines are recognised. Lines outside the
// section, blanks, comments (`#`), and unknown keys are ignored.
// Anything malformed inside the section is an error so
// misconfiguration surfaces loudly. Returns (kv, present, err) where
// present is true when the section header was seen at least once.
func parseSection(content, section string) (map[string]string, bool, error) {
	out := map[string]string{}
	present := false
	inSection := false
	scanner := bufio.NewScanner(strings.NewReader(content))
	for lineNum := 1; scanner.Scan(); lineNum++ {
		raw := scanner.Text()
		line := strings.TrimSpace(raw)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if strings.HasPrefix(line, "[") && strings.HasSuffix(line, "]") {
			name := strings.TrimSpace(line[1 : len(line)-1])
			if name == section {
				inSection = true
				present = true
			} else {
				inSection = false
			}
			continue
		}
		if !inSection {
			continue
		}
		eq := strings.IndexByte(line, '=')
		if eq <= 0 {
			return nil, present, fmt.Errorf("line %d: malformed entry %q", lineNum, line)
		}
		key := strings.TrimSpace(line[:eq])
		val := strings.TrimSpace(line[eq+1:])
		val = stripInlineComment(val)
		val = strings.TrimSpace(val)
		val = stripQuotes(val)
		out[key] = val
	}
	if err := scanner.Err(); err != nil {
		return nil, present, err
	}
	return out, present, nil
}

// stripInlineComment trims a trailing `# ...` comment unless the `#`
// sits inside double quotes. Mirrors the leniency expected by the
// other spore.toml subset parsers.
func stripInlineComment(s string) string {
	inQuotes := false
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c == '"' {
			inQuotes = !inQuotes
			continue
		}
		if c == '#' && !inQuotes {
			return s[:i]
		}
	}
	return s
}

func stripQuotes(s string) string {
	if len(s) >= 2 && s[0] == '"' && s[len(s)-1] == '"' {
		return s[1 : len(s)-1]
	}
	return s
}
