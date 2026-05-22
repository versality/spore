package main

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// target names an LLM coding agent the rover can launch. Bin is the
// PATH-resolvable binary name. StatePaths returns host paths that
// must be bound rw into the sandbox for the binary to start (typically
// credentials and session state). StatePaths may return paths that do
// not exist; the policy stage filters those out.
//
// New targets (codex, opencode, ...) slot in by registering an entry
// in targets below.
type target struct {
	Name       string
	Bin        string
	StatePaths func() []string
}

var targets = map[string]target{
	"claude": {
		Name: "claude",
		Bin:  "claude",
		// claude needs rw on ~/.claude (session state) and
		// ~/.claude.json (credentials). Exposing the credentials file
		// to the sandboxed rover is intentional under the current
		// threat model; per-rover credential isolation is a later
		// phase.
		StatePaths: func() []string { return homePaths(".claude", ".claude.json") },
	},
}

func knownTargets() string {
	names := make([]string, 0, len(targets))
	for n := range targets {
		names = append(names, n)
	}
	sort.Strings(names)
	return strings.Join(names, ", ")
}

func homePaths(rels ...string) []string {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil
	}
	var out []string
	for _, rel := range rels {
		p := filepath.Join(home, rel)
		if _, err := os.Stat(p); err == nil {
			out = append(out, p)
		}
	}
	return out
}
