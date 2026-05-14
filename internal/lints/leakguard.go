package lints

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/versality/spore/internal/leakdict"
)

// LeakGuard scans tracked files for terms that are private to a
// specific consumer (the home nix-config layout) and therefore must
// not appear in spore source, comments, or test fixtures. It is the
// kernel-side half of the bidirectional leak guard; the reverse-
// direction guard (spore-internal terms leaked into nix-config) lives
// downstream.
//
// Scope: file content of every git ls-files entry whose extension is
// in leakGuardTextExts. A separate hook integration (the commit-msg
// path in internal/hooks/install.go) scans commit messages via
// leakdict.ScanMessage. LeakGuard is in Default() so go test ./...
// and spore lint both catch a leak the moment it lands in the index.
type LeakGuard struct {
	// Extra terms a consumer can add via spore.toml. Empty by default.
	Extra []string
	// Extra per-path skip globs (filepath.Match-compatible) in
	// addition to the built-in allowlist.
	SkipPath []string
}

func (LeakGuard) Name() string { return "leak-guard" }

// leakGuardPathAllowlist lists repo-relative paths that may legally
// contain leak terms: the dictionary file itself (in internal/leakdict)
// and its tests, plus the leakguard lint sources (which exist to
// detect the terms).
var leakGuardPathAllowlist = []string{
	"internal/leakdict/leakdict.go",
	"internal/leakdict/leakdict_test.go",
	"internal/lints/leakguard*.go",
}

// leakGuardTextExts is the set of file extensions whose content is
// scanned. Anything else (binaries, lockfiles, generated artifacts)
// is skipped by listFiles.
var leakGuardTextExts = map[string]bool{
	".go":   true,
	".md":   true,
	".sh":   true,
	".bash": true,
	".nix":  true,
	".toml": true,
	".yaml": true,
	".yml":  true,
	".json": true,
	".txt":  true,
	".py":   true,
	".rs":   true,
}

// Run scans every tracked file with an allowed extension and emits
// one Issue per (path, line, term) hit. Lines are 1-indexed.
func (l LeakGuard) Run(root string) ([]Issue, error) {
	files, err := listFiles(root, leakGuardTextExts)
	if err != nil {
		return nil, err
	}
	skips := append(append([]string{}, leakGuardPathAllowlist...), l.SkipPath...)

	var issues []Issue
	for _, rel := range files {
		if leakGuardSkip(rel, skips) {
			continue
		}
		b, err := os.ReadFile(filepath.Join(root, rel))
		if err != nil {
			return nil, fmt.Errorf("leak-guard: read %s: %w", rel, err)
		}
		for i, line := range strings.Split(string(b), "\n") {
			for _, hit := range leakdict.ScanLine(line, l.Extra) {
				issues = append(issues, Issue{
					Path:    rel,
					Line:    i + 1,
					Message: fmt.Sprintf("leak-guard: %q is consumer-private", hit),
				})
			}
		}
	}
	return issues, nil
}

func leakGuardSkip(rel string, patterns []string) bool {
	rel = filepath.ToSlash(rel)
	for _, p := range patterns {
		ok, err := filepath.Match(p, rel)
		if err == nil && ok {
			return true
		}
	}
	return false
}
