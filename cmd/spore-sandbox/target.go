package main

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// target names an LLM coding agent the sandbox can launch. Bin is the
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
		// to the sandboxed agent is intentional under the current
		// threat model; per-sandbox credential isolation is a later
		// phase.
		StatePaths: func() []string { return homePaths(".claude", ".claude.json") },
	},
	"codex": {
		Name: "codex",
		Bin:  "codex",
		// codex puts everything under ~/.codex: auth.json (OpenAI
		// API token), sessions/, history.jsonl, hooks.json,
		// state_*.sqlite, logs_*.sqlite, models_cache.json, plus
		// tmp/ scratch. One rw bind covers the lot.
		StatePaths: func() []string { return homePaths(".codex") },
	},
	"opencode": {
		Name: "opencode",
		Bin:  "opencode",
		// opencode splits its state across three XDG dirs:
		//   ~/.config/opencode   -> opencode.json config + npm deps
		//                           the launcher pulls in
		//   ~/.local/share/opencode -> auth.json (anthropic/openai
		//                              tokens) + opencode.db SQLite
		//                              + storage/ snapshots
		//   ~/.cache/opencode    -> bin/ with downloaded native
		//                           runtimes, models.json
		// All three need rw: opencode mutates the SQLite during a
		// session and may unpack new runtime binaries on demand.
		StatePaths: func() []string {
			return homePaths(
				filepath.Join(".config", "opencode"),
				filepath.Join(".local", "share", "opencode"),
				filepath.Join(".cache", "opencode"),
			)
		},
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
