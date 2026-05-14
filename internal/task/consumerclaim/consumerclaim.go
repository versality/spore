// Package consumerclaim parses and scans `consumer-claims:` task
// frontmatter entries (tasks/spore-worker-finish-contract.md section
// 4). A claim names a downstream consumer repo plus a mechanical
// signal that proves the consumer has caught up to a spore lift:
// either a `path` that must no longer exist, or a `grep` pattern that
// must no longer match anywhere in the repo's tracked files.
//
// Wire format (one item per `consumer-claims:` list entry):
//
//	<repo>:path:<relative-path>
//	<repo>:grep:<pattern>
//
// Three colon-separated fields; the third may itself contain colons
// and is taken literally to the end of the line.
//
// Repo resolution: by convention the consumer checkout lives at
// `~/projects/<repo>`. Override per-repo via `SPORE_CONSUMER_<UPPER>`
// (e.g. `SPORE_CONSUMER_NIX_CONFIG=/srv/nix-config`). When the
// resolved path does not exist on disk, scanning that claim degrades
// to a `skipped` result (operator does not have a local checkout); the
// I11 gate treats `skipped` as unresolved.
package consumerclaim

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// Kind enumerates the supported claim signals.
type Kind string

const (
	KindPath Kind = "path" // file must NOT exist
	KindGrep Kind = "grep" // pattern must NOT match anywhere
)

// Claim is one parsed entry.
type Claim struct {
	Repo  string
	Kind  Kind
	Value string
}

// ParseClaim splits one `consumer-claims:` list entry into a Claim.
func ParseClaim(s string) (Claim, error) {
	parts := strings.SplitN(strings.TrimSpace(s), ":", 3)
	if len(parts) < 3 {
		return Claim{}, fmt.Errorf("consumer-claims: %q: want <repo>:<kind>:<value>", s)
	}
	repo := strings.TrimSpace(parts[0])
	if repo == "" {
		return Claim{}, fmt.Errorf("consumer-claims: %q: empty repo", s)
	}
	kind := Kind(strings.TrimSpace(parts[1]))
	if kind != KindPath && kind != KindGrep {
		return Claim{}, fmt.Errorf("consumer-claims: %q: kind must be path or grep, got %q", s, parts[1])
	}
	val := strings.TrimSpace(parts[2])
	if val == "" {
		return Claim{}, fmt.Errorf("consumer-claims: %q: empty value", s)
	}
	return Claim{Repo: repo, Kind: kind, Value: val}, nil
}

// Status names the outcome of scanning a single claim.
type Status string

const (
	StatusResolved   Status = "resolved"   // consumer caught up: signal is clean.
	StatusUnresolved Status = "unresolved" // consumer still references the obsoleted thing.
	StatusSkipped    Status = "skipped"    // consumer checkout absent locally; cannot scan.
)

// Result is the scanner's verdict for one claim.
type Result struct {
	Claim  Claim
	Status Status
	Detail string // e.g. matching file, grep hit, or skip reason.
}

// Deps are the test seams. Nil fields fall back to real implementations.
type Deps struct {
	// LookupEnv defaults to os.LookupEnv.
	LookupEnv func(string) (string, bool)
	// HomeDir defaults to os.UserHomeDir.
	HomeDir func() (string, error)
	// Stat defaults to os.Stat.
	Stat func(string) (os.FileInfo, error)
	// Grep defaults to running `git -C <repo> grep --fixed-strings -l -E
	// <pattern>`. Returns the first matching path or "" when no match.
	Grep func(repo, pattern string) (match string, err error)
}

// ResolveRepoPath returns the absolute path of the named consumer repo.
// Order of resolution:
//
//  1. SPORE_CONSUMER_<UPPER_REPO> env var (with `-` -> `_`).
//  2. ~/projects/<repo>.
//
// The path is returned even if it does not exist; callers (Scan) treat
// missing paths as a `skipped` claim.
func ResolveRepoPath(repo string, deps Deps) (string, error) {
	deps = deps.withDefaults()
	envKey := "SPORE_CONSUMER_" + strings.ToUpper(strings.ReplaceAll(repo, "-", "_"))
	if v, ok := deps.LookupEnv(envKey); ok && v != "" {
		return v, nil
	}
	home, err := deps.HomeDir()
	if err != nil {
		return "", fmt.Errorf("consumer-claim: %s: %w", repo, err)
	}
	return filepath.Join(home, "projects", repo), nil
}

// Scan evaluates every claim and returns one Result per claim. Errors
// from individual claims surface in Result.Detail; the function itself
// returns an error only on catastrophic conditions (none today).
func Scan(claims []Claim, deps Deps) []Result {
	deps = deps.withDefaults()
	out := make([]Result, 0, len(claims))
	for _, c := range claims {
		out = append(out, scanOne(c, deps))
	}
	return out
}

func scanOne(c Claim, deps Deps) Result {
	repoPath, err := ResolveRepoPath(c.Repo, deps)
	if err != nil {
		return Result{Claim: c, Status: StatusSkipped, Detail: err.Error()}
	}
	if info, err := deps.Stat(repoPath); err != nil || !info.IsDir() {
		return Result{
			Claim:  c,
			Status: StatusSkipped,
			Detail: fmt.Sprintf("consumer checkout missing at %s", repoPath),
		}
	}
	switch c.Kind {
	case KindPath:
		target := filepath.Join(repoPath, c.Value)
		if _, err := deps.Stat(target); err == nil {
			return Result{Claim: c, Status: StatusUnresolved, Detail: target}
		}
		return Result{Claim: c, Status: StatusResolved}
	case KindGrep:
		match, err := deps.Grep(repoPath, c.Value)
		if err != nil {
			return Result{Claim: c, Status: StatusSkipped, Detail: err.Error()}
		}
		if match != "" {
			return Result{Claim: c, Status: StatusUnresolved, Detail: match}
		}
		return Result{Claim: c, Status: StatusResolved}
	}
	return Result{Claim: c, Status: StatusSkipped, Detail: "unknown kind"}
}

// AnyUnresolved is true when at least one Result is unresolved or
// skipped. Skipped counts as unresolved for the I11 gate: an operator
// cannot prove the consumer caught up if they cannot scan.
func AnyUnresolved(results []Result) bool {
	for _, r := range results {
		if r.Status != StatusResolved {
			return true
		}
	}
	return false
}

func (d Deps) withDefaults() Deps {
	if d.LookupEnv == nil {
		d.LookupEnv = os.LookupEnv
	}
	if d.HomeDir == nil {
		d.HomeDir = os.UserHomeDir
	}
	if d.Stat == nil {
		d.Stat = os.Stat
	}
	if d.Grep == nil {
		d.Grep = realGrep
	}
	return d
}

func realGrep(repo, pattern string) (string, error) {
	// `git grep -l -F <pattern>` lists files containing the literal
	// pattern. -F (fixed-strings) keeps the contract simple: the
	// pattern is matched verbatim, no regex surprises.
	cmd := exec.Command("git", "-C", repo, "grep", "-l", "-F", "--", pattern)
	out, err := cmd.Output()
	if err != nil {
		if ee, ok := err.(*exec.ExitError); ok && ee.ExitCode() == 1 {
			// git grep exits 1 on no matches; treat as clean.
			return "", nil
		}
		return "", err
	}
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		if line != "" {
			return line, nil
		}
	}
	return "", nil
}
