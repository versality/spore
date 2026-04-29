// Package composer renders a CLAUDE.md by concatenating named rule
// fragments listed in a consumer file. Pure stdlib.
//
// The rules pool has ONE source dir, no override layering. Compose
// reads only the rulesDir argument it is given: no env-var redirects,
// no ~/.config/ overlays, no per-user shadow paths. A render is a
// pure function of (rulesDir contents, consumer file, predicate map).
// If a future change wants per-user overrides, it must change this
// signature, not slip in via an environment side channel.
package composer

import (
	"bufio"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Options configures Compose. Predicates gates lines that begin
// with "?<name> ": the rule is included only when Predicates[name]
// is true. Lines without a "?" prefix are unconditional.
type Options struct {
	Predicates map[string]bool
}

// Compose reads a consumer config (line-per-rule plain text, "#"
// and blank lines ignored) and returns the rendered CLAUDE.md
// string built by concatenating the named rule fragments from
// rulesDir, separated by a single blank line. Returns error on
// missing rule, missing consumer, or read failure.
//
// Lines may be prefixed with "?<predicate> <id>". The rule is
// included only when opts.Predicates[predicate] is true. An unknown
// predicate (not present in the map) is treated as false, so
// predicate-gated lines are dropped by default.
func Compose(rulesDir, consumerPath string, opts Options) (string, error) {
	ids, err := readConsumer(consumerPath, opts)
	if err != nil {
		return "", err
	}

	pieces := make([]string, 0, len(ids))
	for _, id := range ids {
		frag, err := readRule(rulesDir, id)
		if err != nil {
			return "", err
		}
		pieces = append(pieces, frag)
	}

	return strings.Join(pieces, "\n\n") + "\n", nil
}

func readConsumer(path string, opts Options) ([]string, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("composer: open consumer %q: %w", path, err)
	}
	defer f.Close()

	var ids []string
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if strings.HasPrefix(line, "?") {
			pred, rule, ok := splitPredicate(line)
			if !ok {
				return nil, fmt.Errorf("composer: %s: malformed predicate line %q", path, line)
			}
			if !opts.Predicates[pred] {
				continue
			}
			ids = append(ids, rule)
			continue
		}
		ids = append(ids, line)
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("composer: read consumer %q: %w", path, err)
	}
	if len(ids) == 0 {
		return nil, fmt.Errorf("composer: consumer %q lists no rules", path)
	}
	return ids, nil
}

func splitPredicate(line string) (pred, rule string, ok bool) {
	body := strings.TrimPrefix(line, "?")
	sp := strings.IndexAny(body, " \t")
	if sp <= 0 {
		return "", "", false
	}
	pred = strings.TrimSpace(body[:sp])
	rule = strings.TrimSpace(body[sp+1:])
	if pred == "" || rule == "" {
		return "", "", false
	}
	return pred, rule, true
}

func readRule(rulesDir, id string) (string, error) {
	if id == "" {
		return "", errors.New("composer: empty rule id")
	}
	rel := filepath.FromSlash(id) + ".md"
	full := filepath.Join(rulesDir, rel)
	b, err := os.ReadFile(full)
	if err != nil {
		return "", fmt.Errorf("composer: read rule %q (%s): %w", id, full, err)
	}
	return strings.TrimSpace(string(b)), nil
}
