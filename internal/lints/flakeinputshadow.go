package lints

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// FlakeInputShadow flags `pkgs.<flake-input>` references in nix
// modules where <flake-input> is one of the inputs declared in the
// project's flake.nix. The correct path is the specialargs surface
// (`<input>` passed via _module.args / specialargs); reaching for
// it through `pkgs.<input>` lands as a confusing eval error because
// nixpkgs has no attribute by that name.
//
// nixpkgs itself is always exempt (it IS pkgs). Additional inputs
// can be exempted via AllowInputs (the spore.toml `allowlist` key).
type FlakeInputShadow struct {
	FlakePath   string
	ScanDirs    []string
	SkipPath    []string
	AllowInputs []string
}

func (FlakeInputShadow) Name() string { return "flake-input-shadow" }

func (l FlakeInputShadow) Run(root string) ([]Issue, error) {
	flakePath := l.FlakePath
	if flakePath == "" {
		flakePath = "flake.nix"
	}
	allow := map[string]bool{"nixpkgs": true}
	for _, a := range l.AllowInputs {
		a = strings.TrimSpace(a)
		if a != "" {
			allow[a] = true
		}
	}

	inputs, err := readFlakeInputs(filepath.Join(root, flakePath))
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("flake-input-shadow: read %s: %w", flakePath, err)
	}
	var targets []string
	for _, name := range inputs {
		if !allow[name] {
			targets = append(targets, name)
		}
	}
	if len(targets) == 0 {
		return nil, nil
	}

	files, err := listFiles(root, map[string]bool{".nix": true})
	if err != nil {
		return nil, err
	}

	res := make([]*regexp.Regexp, 0, len(targets))
	for _, name := range targets {
		res = append(res, regexp.MustCompile(`\bpkgs\.`+regexp.QuoteMeta(name)+`\b`))
	}

	var issues []Issue
	for _, rel := range files {
		if rel == flakePath {
			continue
		}
		if skipPath(rel, l.SkipPath) {
			continue
		}
		if scanDirsConfigured(l.ScanDirs) && !inScanDirs(rel, l.ScanDirs) {
			continue
		}
		b, err := os.ReadFile(filepath.Join(root, rel))
		if err != nil {
			return nil, err
		}
		for lineNo, line := range strings.Split(string(b), "\n") {
			for i, re := range res {
				if re.MatchString(line) {
					issues = append(issues, Issue{
						Path:    rel,
						Line:    lineNo + 1,
						Message: fmt.Sprintf("pkgs.%s shadows flake input - use the specialargs binding instead", targets[i]),
					})
				}
			}
		}
	}
	return issues, nil
}

// readFlakeInputs returns the top-level input ids declared in the
// `inputs = { ... }` attrset of a flake.nix. Brace-depth-tolerant
// without a full nix parser: locates `inputs =\s*{`, then scans
// identifier assignments at depth 1 until the matching close brace.
func readFlakeInputs(path string) ([]string, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	src := string(b)
	loc := flakeInputsHeader.FindStringIndex(src)
	if loc == nil {
		return nil, nil
	}
	rest := src[loc[1]:]
	depth := 1
	i := 0
	var names []string
	atLineStart := true
	for i < len(rest) && depth > 0 {
		ch := rest[i]
		switch {
		case ch == '#':
			for i < len(rest) && rest[i] != '\n' {
				i++
			}
		case ch == '{':
			depth++
			atLineStart = false
			i++
		case ch == '}':
			depth--
			atLineStart = false
			i++
		case ch == '\n':
			atLineStart = true
			i++
		case ch == ' ' || ch == '\t':
			i++
		case depth == 1 && atLineStart && isIdentStart(ch):
			j := i
			for j < len(rest) && isIdentByte(rest[j]) {
				j++
			}
			id := rest[i:j]
			k := j
			for k < len(rest) && (rest[k] == ' ' || rest[k] == '\t') {
				k++
			}
			if k < len(rest) && (rest[k] == '=' || rest[k] == '.') {
				names = append(names, id)
			}
			i = j
			atLineStart = false
		default:
			atLineStart = false
			i++
		}
	}
	return names, nil
}

var flakeInputsHeader = regexp.MustCompile(`(?m)^\s*inputs\s*=\s*\{`)

func isIdentStart(c byte) bool {
	return (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || c == '_'
}

func isIdentByte(c byte) bool {
	return isIdentStart(c) || (c >= '0' && c <= '9') || c == '-'
}
