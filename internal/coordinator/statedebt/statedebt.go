// Package statedebt scans a coordinator state.md file for prose
// lessons (CRITICAL LESSON / <prefix> SELF-LESSON / RULE blocks under
// H2/H3 headings) that should have been lifted to the harness. The
// SELF-LESSON prefix is consumer-supplied (e.g. SKYHELM SELF-LESSON);
// any single-word prefix matches.
package statedebt

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

const DefaultAgeDays = 14

type Config struct {
	StateDir  string
	StateFile string
	AgeDays   int
}

type Classification string

const (
	Lifted             Classification = "lifted"
	StaleLiftCandidate Classification = "stale-lift-candidate"
	Pending            Classification = "pending"
)

type Block struct {
	Heading        string
	Classification Classification
	LatestDate     string
	HasHarness     bool
}

type ScanResult struct {
	Blocks        []Block
	StaleCount    int
	StaleHeadings []string
}

func (c Config) defaults() Config {
	if c.StateDir == "" {
		c.StateDir = defaultStateDir()
	}
	if c.StateFile == "" {
		c.StateFile = filepath.Join(c.StateDir, "state.md")
	}
	if c.AgeDays <= 0 {
		c.AgeDays = DefaultAgeDays
	}
	return c
}

var (
	lessonRE  = regexp.MustCompile(`(?i)(CRITICAL LESSON|\w+ SELF-LESSON|RULE)`)
	dateRE    = regexp.MustCompile(`\d{4}-\d{2}-\d{2}`)
	harnessRE = regexp.MustCompile(`harness:\s*\S+`)
)

// defaultStateDir resolves the coordinator state dir from the
// SPORE_COORDINATOR_STATE_DIR env var, falling back to
// $HOME/.local/state/spore/coordinator. Consumers (e.g. an external
// orchestrator that already has its own state tree) export the env
// var to point spore at their existing layout.
func defaultStateDir() string {
	if d := os.Getenv("SPORE_COORDINATOR_STATE_DIR"); d != "" {
		return d
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".local", "state", "spore", "coordinator")
}

// Scan reads the state file and classifies every H2/H3 block whose
// heading matches the lesson pattern.
func Scan(cfg Config) (ScanResult, error) {
	cfg = cfg.defaults()

	content, err := os.ReadFile(cfg.StateFile)
	if err != nil {
		if os.IsNotExist(err) {
			return ScanResult{}, nil
		}
		return ScanResult{}, err
	}

	threshold := time.Now().UTC().AddDate(0, 0, -cfg.AgeDays).Format("2006-01-02")

	return scanContent(string(content), threshold), nil
}

func scanContent(content, threshold string) ScanResult {
	lines := strings.Split(content, "\n")
	var result ScanResult

	var heading string
	var bodyLines []string

	flush := func() {
		if heading == "" {
			return
		}
		body := strings.Join(bodyLines, "\n")
		block := classify(heading, body, threshold)
		result.Blocks = append(result.Blocks, block)
		if block.Classification == StaleLiftCandidate {
			result.StaleCount++
			clean := strings.TrimLeft(heading, "# \t")
			result.StaleHeadings = append(result.StaleHeadings, clean)
		}
		heading = ""
		bodyLines = nil
	}

	for _, line := range lines {
		if isH2orH3(line) {
			flush()
			if lessonRE.MatchString(line) {
				heading = line
				bodyLines = nil
			}
			continue
		}
		if heading != "" {
			bodyLines = append(bodyLines, line)
		}
	}
	flush()

	return result
}

func isH2orH3(line string) bool {
	trimmed := strings.TrimRight(line, " \t")
	return strings.HasPrefix(trimmed, "## ") || strings.HasPrefix(trimmed, "### ")
}

func classify(heading, body, threshold string) Block {
	combined := heading + "\n" + body
	hasHarness := harnessRE.MatchString(combined)
	latestDate := findLatestDate(combined)

	b := Block{
		Heading:    heading,
		HasHarness: hasHarness,
		LatestDate: latestDate,
	}

	if hasHarness {
		b.Classification = Lifted
	} else if latestDate == "" || latestDate < threshold {
		b.Classification = StaleLiftCandidate
	} else {
		b.Classification = Pending
	}

	return b
}

func findLatestDate(text string) string {
	matches := dateRE.FindAllString(text, -1)
	var latest string
	for _, d := range matches {
		if d > latest {
			latest = d
		}
	}
	return latest
}

// FormatVerbose returns a human-readable table of all classified blocks.
func FormatVerbose(result ScanResult) string {
	if len(result.Blocks) == 0 {
		return "no CRITICAL LESSON / SELF-LESSON / RULE blocks"
	}
	var buf strings.Builder
	for _, b := range result.Blocks {
		heading := strings.TrimLeft(b.Heading, "# \t")
		fmt.Fprintf(&buf, "%-22s %s\n", b.Classification, heading)
	}
	return buf.String()
}

// FormatSummary returns the stale-lift-candidate summary line (empty
// if no stale blocks).
func FormatSummary(result ScanResult) string {
	if result.StaleCount == 0 {
		return ""
	}
	return "stale-lift-candidate: " + strings.Join(result.StaleHeadings, "; ")
}
