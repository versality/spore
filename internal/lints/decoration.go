package lints

import (
	"bufio"
	"os"
	"path/filepath"
	"regexp"
)

// Decoration flags ASCII banner/divider comments: lines whose comment
// body opens with 3+ divider characters (- = * _ ~). Unicode
// box-drawing (──, ══) is allowed. Covers `# ---`, `// ===`,
// `;; ---`, `/* ===`, and variants.
type Decoration struct{}

func (Decoration) Name() string { return "decoration" }

// decorationExts is the file-extension set Decoration scans: shell
// flavours (nix, sh, bash, bb, edn) plus mainstream source languages
// (Go, Python, Ruby, Rust, JS/TS, Lua, Clojure) that can carry
// banner-divider comments. Justfile is included by base name.
var decorationExts = map[string]bool{
	".nix":     true,
	".sh":      true,
	".bash":    true,
	".bb":      true,
	".edn":     true,
	".go":      true,
	".py":      true,
	".rs":      true,
	".js":      true,
	".ts":      true,
	".rb":      true,
	".lua":     true,
	".clj":     true,
	".cljs":    true,
	".cljc":    true,
	"justfile": true,
}

var reDecoration = regexp.MustCompile(`^[[:space:]]*(?:#+|;+|//|/\*)[[:space:]]*[-=*_~]{3,}`)

func (Decoration) Run(root string) ([]Issue, error) {
	files, err := listFiles(root, decorationExts)
	if err != nil {
		return nil, err
	}
	var issues []Issue
	for _, rel := range files {
		if isGenerated(rel) {
			continue
		}
		path := filepath.Join(root, rel)
		found, err := scanDecoration(path, rel)
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return nil, err
		}
		issues = append(issues, found...)
	}
	return issues, nil
}

func scanDecoration(path, rel string) ([]Issue, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 64*1024), 4*1024*1024)
	var issues []Issue
	lineNo := 0
	for scanner.Scan() {
		lineNo++
		line := scanner.Text()
		if reDecoration.MatchString(line) {
			issues = append(issues, Issue{
				Path:    rel,
				Line:    lineNo,
				Message: "decorative/banner comment (keep only WHY comments)",
			})
		}
	}
	return issues, scanner.Err()
}
