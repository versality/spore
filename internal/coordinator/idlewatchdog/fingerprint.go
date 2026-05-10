package idlewatchdog

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// fingerprintList returns the per-finding sha256 hex digests in
// finding order (matches bash `for f; sha256sum`).
func fingerprintList(findings []string) []string {
	out := make([]string, 0, len(findings))
	for _, f := range findings {
		sum := sha256.Sum256([]byte(f))
		out = append(out, hex.EncodeToString(sum[:]))
	}
	return out
}

// aggregateHash sorts the unique fingerprints and returns the sha256
// of the joined-with-newlines string. Mirrors bash
// `printf '%s\n' fp | sort -u | sha256sum`.
func aggregateHash(fps []string) string {
	uniq := uniqueSorted(fps)
	sum := sha256.Sum256([]byte(strings.Join(uniq, "\n") + "\n"))
	return hex.EncodeToString(sum[:])
}

// aggregateOfFile returns the sorted-unique aggregate hash of the
// fingerprint set persisted at path. Missing / empty file -> "".
func aggregateOfFile(path string) string {
	body, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	var fps []string
	for _, line := range strings.Split(string(body), "\n") {
		if line != "" {
			fps = append(fps, line)
		}
	}
	if len(fps) == 0 {
		return ""
	}
	return aggregateHash(fps)
}

// newCountVs returns how many fingerprints in fps are NOT already
// present in the file at path. Missing file -> len(fps).
func newCountVs(path string, fps []string) int {
	body, _ := os.ReadFile(path)
	prior := map[string]bool{}
	for _, line := range strings.Split(string(body), "\n") {
		if line != "" {
			prior[line] = true
		}
	}
	n := 0
	for _, fp := range fps {
		if !prior[fp] {
			n++
		}
	}
	return n
}

// writeFingerprintSet overwrites the file at path with one fingerprint
// per line. Best-effort: failures are swallowed (bash `2>/dev/null`).
func writeFingerprintSet(path string, fps []string) {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil && !os.IsExist(err) {
		return
	}
	_ = os.WriteFile(path, []byte(strings.Join(fps, "\n")+"\n"), 0o600)
}

func uniqueSorted(in []string) []string {
	seen := map[string]bool{}
	out := make([]string, 0, len(in))
	for _, s := range in {
		if !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	sort.Strings(out)
	return out
}
