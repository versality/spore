package lints

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// Agenix flags two store-leak patterns in .nix modules using agenix.
// Both vectors land plaintext secret bytes into /nix/store (world-
// readable), which is the agenix integration footgun.
//
//  1. readFile of a decrypted age secret - either a literal .age path
//     or `age.secrets.<name>.path` (which is a /run/agenix path at
//     runtime but evaluates to a string the file-system import would
//     read at eval time).
//  2. `age.secrets.*.path` interpolated into a store-bound string
//     body: writeText / writeShellScript / writeScriptBin /
//     writeScript builders, or `text = ''...''` / `script = ''...''`
//     attribute heredocs.
//
// Sanctioned runtime contexts (`passwordCommand = "cat ${...path}"`,
// `preStart = "...${...path}..."`, plain attribute references like
// `environmentFile = config.age.secrets.x.path`) are not matched
// because they are neither readFile nor inside one of the
// enumerated store-bound builders.
//
// Default skips: `templates/` and `secrets/` (matches the bash prior
// art at nix-config/harness/check-agenix.sh). Configurable via the
// shared scan_dirs / skip_path TOML keys.
type Agenix struct {
	ScanDirs []string
	SkipPath []string
}

func (Agenix) Name() string { return "agenix" }

var (
	agenixReadFile = regexp.MustCompile(`(builtins\.|lib\.|[\s(])readFile\s+[^;]*(\.age\b|age\.secrets\.)`)
	agenixWriteBuilder = regexp.MustCompile(`(?s)\b(writeText|writeShellScript|writeScriptBin|writeScript)\b[^;]{0,400}?\$\{[^}]*age\.secrets\.`)
	agenixHeredocAttr  = regexp.MustCompile(`(?ms)^[\t ]*(text|script)[\t ]*=[\t ]*''[^']{0,400}?\$\{[^}]*age\.secrets\.`)
)

const (
	agenixReadFileMsg = "readFile of an age secret - decrypted bytes would be inlined into /nix/store"
	agenixInterpMsg   = "age.secrets.*.path interpolated into store-bound string - lands in /nix/store"
)

var agenixDefaultSkips = []string{"templates/", "secrets/"}

func (l Agenix) Run(root string) ([]Issue, error) {
	files, err := listFiles(root, map[string]bool{".nix": true})
	if err != nil {
		return nil, err
	}
	skips := append(append([]string{}, agenixDefaultSkips...), l.SkipPath...)

	var issues []Issue
	for _, rel := range files {
		if skipPath(rel, skips) {
			continue
		}
		if l.scanDirsConfigured() && !l.inScanDirs(rel) {
			continue
		}
		b, err := os.ReadFile(filepath.Join(root, rel))
		if err != nil {
			return nil, fmt.Errorf("agenix: read %s: %w", rel, err)
		}
		src := string(b)
		lines := strings.Split(src, "\n")
		for i, line := range lines {
			if agenixReadFile.MatchString(line) {
				issues = append(issues, Issue{
					Path:    rel,
					Line:    i + 1,
					Message: agenixReadFileMsg,
				})
			}
		}
		for _, loc := range agenixWriteBuilder.FindAllStringIndex(src, -1) {
			issues = append(issues, Issue{
				Path:    rel,
				Line:    lineOf(src, loc[0]),
				Message: agenixInterpMsg,
			})
		}
		for _, loc := range agenixHeredocAttr.FindAllStringIndex(src, -1) {
			issues = append(issues, Issue{
				Path:    rel,
				Line:    lineOf(src, loc[0]),
				Message: agenixInterpMsg,
			})
		}
	}
	return issues, nil
}

func (l Agenix) scanDirsConfigured() bool {
	for _, d := range l.ScanDirs {
		if s := strings.TrimSpace(d); s != "" && s != "." {
			return true
		}
	}
	return false
}

func (l Agenix) inScanDirs(rel string) bool {
	rel = filepath.ToSlash(rel)
	for _, d := range l.ScanDirs {
		d = strings.TrimSpace(filepath.ToSlash(d))
		if d == "" || d == "." {
			return true
		}
		d = strings.TrimSuffix(d, "/")
		if rel == d || strings.HasPrefix(rel, d+"/") {
			return true
		}
	}
	return false
}

func lineOf(src string, byteOffset int) int {
	if byteOffset > len(src) {
		byteOffset = len(src)
	}
	return strings.Count(src[:byteOffset], "\n") + 1
}
