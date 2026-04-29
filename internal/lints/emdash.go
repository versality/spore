package lints

import (
	"bufio"
	"io"
	"os"
	"path/filepath"
	"strings"
	"unicode/utf8"
)

// emDashEnDash is the two-rune set the EmDash lint matches against:
// U+2014 (em-dash) and U+2013 (en-dash). Defined via escapes so the
// source itself does not contain the bytes the lint hunts for.
const emDashEnDash = "\u2014\u2013"

// EmDash flags U+2014 (em-dash) and U+2013 (en-dash) anywhere in
// tracked text files. Replace with a regular hyphen, colon,
// parentheses, or a new sentence. Binary files and obvious data
// extensions are skipped.
type EmDash struct{}

func (EmDash) Name() string { return "emdash" }

func (EmDash) Run(root string) ([]Issue, error) {
	files, err := listFiles(root, nil)
	if err != nil {
		return nil, err
	}
	var issues []Issue
	for _, rel := range files {
		ext := strings.ToLower(filepath.Ext(rel))
		if isBinaryExt(ext) {
			continue
		}
		path := filepath.Join(root, rel)
		f, err := os.Open(path)
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return nil, err
		}
		issues = append(issues, scanEmDash(f, rel)...)
		_ = f.Close()
	}
	return issues, nil
}

func scanEmDash(r io.Reader, rel string) []Issue {
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 64*1024), 4*1024*1024)
	var issues []Issue
	lineNo := 0
	for scanner.Scan() {
		lineNo++
		line := scanner.Text()
		if !utf8.ValidString(line) {
			continue
		}
		if strings.ContainsAny(line, emDashEnDash) {
			issues = append(issues, Issue{
				Path:    rel,
				Line:    lineNo,
				Message: "em-dash or en-dash; replace with hyphen, colon, parentheses, or a new sentence",
			})
		}
	}
	return issues
}

func isBinaryExt(ext string) bool {
	switch ext {
	case ".png", ".jpg", ".jpeg", ".gif", ".webp", ".ico",
		".pdf", ".zip", ".tar", ".gz", ".bz2", ".xz",
		".woff", ".woff2", ".ttf", ".eot",
		".so", ".dylib", ".dll", ".a", ".o",
		".bin", ".wasm", ".lock":
		return true
	}
	return false
}
