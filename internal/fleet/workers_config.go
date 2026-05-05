package fleet

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/versality/spore/internal/task/frontmatter"
)

// WorkersConfig captures the [fleet.workers] section from a project's
// spore.toml. It controls the agent each spawned worker runs when the
// task's frontmatter does not already pin one.
//
// Default is the fallback agent name written when neither rules nor
// ratio yield a pick. Empty Default plus empty Ratio plus empty Rules
// is the kernel default: "claude".
//
// Ratio maps an agent name to a percentage (any positive integer; the
// values are normalised against their sum). When set, Reconcile picks
// the agent whose currently spawned share is furthest below its target
// share, so the active fleet converges on the configured split.
//
// Rules maps a task `complexity:` value to an agent name. When the task
// frontmatter carries a matching complexity, the rule wins over Ratio.
type WorkersConfig struct {
	Default string
	Ratio   map[string]int
	Rules   map[string]string
}

// LoadWorkersConfig reads `[fleet.workers]` from <projectRoot>/spore.toml.
// A missing file returns a zero WorkersConfig with no error so callers
// can treat absent config as "use kernel defaults".
func LoadWorkersConfig(projectRoot string) (WorkersConfig, error) {
	tomlPath := filepath.Join(projectRoot, "spore.toml")
	b, err := os.ReadFile(tomlPath)
	if err != nil {
		if os.IsNotExist(err) {
			return WorkersConfig{}, nil
		}
		return WorkersConfig{}, fmt.Errorf("workers: read %s: %w", tomlPath, err)
	}
	cfg, err := parseWorkersTOML(string(b))
	if err != nil {
		return WorkersConfig{}, fmt.Errorf("workers: parse %s: %w", tomlPath, err)
	}
	return cfg, nil
}

// parseWorkersTOML reads the [fleet.workers], [fleet.workers.ratio],
// and [fleet.workers.rules] sections of the same tiny TOML subset the
// rest of the kernel parses: bare or quoted scalars, `# comment` lines,
// blank lines. Anything outside those sections is ignored. Malformed
// entries inside the sections are an error.
func parseWorkersTOML(content string) (WorkersConfig, error) {
	var cfg WorkersConfig
	section := ""
	scanner := bufio.NewScanner(strings.NewReader(content))
	for lineNum := 1; scanner.Scan(); lineNum++ {
		line := strings.TrimSpace(stripTOMLComment(scanner.Text()))
		if line == "" {
			continue
		}
		if strings.HasPrefix(line, "[") && strings.HasSuffix(line, "]") {
			section = strings.TrimSpace(line[1 : len(line)-1])
			continue
		}
		switch section {
		case "fleet.workers":
			eq := strings.IndexByte(line, '=')
			if eq <= 0 {
				return WorkersConfig{}, fmt.Errorf("line %d: malformed entry %q", lineNum, line)
			}
			key := strings.TrimSpace(line[:eq])
			val := stripTOMLQuotes(strings.TrimSpace(line[eq+1:]))
			if key != "default" {
				return WorkersConfig{}, fmt.Errorf("line %d: unknown key %q in [fleet.workers]", lineNum, key)
			}
			cfg.Default = val
		case "fleet.workers.ratio":
			eq := strings.IndexByte(line, '=')
			if eq <= 0 {
				return WorkersConfig{}, fmt.Errorf("line %d: malformed entry %q", lineNum, line)
			}
			key := strings.TrimSpace(line[:eq])
			val := strings.TrimSpace(line[eq+1:])
			n, err := strconv.Atoi(val)
			if err != nil {
				return WorkersConfig{}, fmt.Errorf("line %d: ratio %q: want integer, got %q", lineNum, key, val)
			}
			if n < 0 {
				return WorkersConfig{}, fmt.Errorf("line %d: ratio %q must be >= 0, got %d", lineNum, key, n)
			}
			if cfg.Ratio == nil {
				cfg.Ratio = map[string]int{}
			}
			cfg.Ratio[key] = n
		case "fleet.workers.rules":
			eq := strings.IndexByte(line, '=')
			if eq <= 0 {
				return WorkersConfig{}, fmt.Errorf("line %d: malformed entry %q", lineNum, line)
			}
			key := strings.TrimSpace(line[:eq])
			val := stripTOMLQuotes(strings.TrimSpace(line[eq+1:]))
			if cfg.Rules == nil {
				cfg.Rules = map[string]string{}
			}
			cfg.Rules[key] = val
		}
	}
	if err := scanner.Err(); err != nil {
		return WorkersConfig{}, err
	}
	return cfg, nil
}

// DefaultWorkerAgent is the fallback agent name when no spore.toml
// pins one. Matches workerAgentCommand's "claude" interpretation.
const DefaultWorkerAgent = "claude"

// SelectAgent returns the agent name to assign to a worker for the
// given task, honouring this precedence:
//
//  1. Explicit `agent:` already set in the task frontmatter.
//  2. A rule keyed on the task's `complexity:` extra.
//  3. Ratio balancing against the running counts: pick the agent whose
//     current share is furthest below its target share.
//  4. cfg.Default, or DefaultWorkerAgent when Default is empty.
//
// counts is the agent-to-spawned-count map for the current pass; it
// must include workers about to spawn so a single Reconcile pass picks
// agents that actually approach the configured ratio.
func SelectAgent(meta frontmatter.Meta, cfg WorkersConfig, counts map[string]int) string {
	if meta.Agent != "" {
		return meta.Agent
	}
	if cfg.Rules != nil {
		if c := meta.Extra["complexity"]; c != "" {
			if a, ok := cfg.Rules[c]; ok && a != "" {
				return a
			}
		}
	}
	if a := selectByRatio(cfg.Ratio, counts); a != "" {
		return a
	}
	if cfg.Default != "" {
		return cfg.Default
	}
	return DefaultWorkerAgent
}

// selectByRatio picks the agent with the largest target-vs-actual
// share deficit. Empty ratio (or all-zero entries) returns "" so the
// caller falls through to the default. Ties resolve in alphabetic
// agent-name order so successive picks are reproducible.
func selectByRatio(ratio map[string]int, counts map[string]int) string {
	if len(ratio) == 0 {
		return ""
	}
	targetTotal := 0
	for _, p := range ratio {
		targetTotal += p
	}
	if targetTotal == 0 {
		return ""
	}
	currTotal := 0
	for k := range ratio {
		currTotal += counts[k]
	}

	keys := make([]string, 0, len(ratio))
	for k := range ratio {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	best := ""
	bestDeficit := -1.0
	for _, k := range keys {
		targetShare := float64(ratio[k]) / float64(targetTotal)
		currShare := 0.0
		if currTotal > 0 {
			currShare = float64(counts[k]) / float64(currTotal)
		}
		deficit := targetShare - currShare
		if deficit > bestDeficit {
			best = k
			bestDeficit = deficit
		}
	}
	return best
}
