package lints

import (
	"bufio"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// CommentNoise flags three high-confidence redundant-comment patterns:
//
//  1. Section-label comments: a 1-3 word Title-Case comment alone on
//     its line, directly above a non-comment, non-closer line. These
//     mimic banner dividers without the divider and burn tokens for
//     zero context the next line lacks.
//  2. Bare TODO/FIXME/XXX/HACK without an issue ref or URL anywhere in
//     the same comment block. Untracked TODOs rot.
//  3. Dated event refs (an event verb like Tested or Fixed followed
//     by an ISO date) inside comments. The fact belongs in the
//     invariant, the date in the commit log.
//
// Safe-port note: this lint ships with no per-project allowlists. If a
// downstream needs to opt a file out, they suppress at the source.
type CommentNoise struct{}

func (CommentNoise) Name() string { return "comment-noise" }

func (CommentNoise) Run(root string) ([]Issue, error) {
	files, err := listFiles(root, sourceExts)
	if err != nil {
		return nil, err
	}
	var issues []Issue
	for _, rel := range files {
		if isGenerated(rel) {
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
		issues = append(issues, scanCommentNoise(f, rel)...)
		_ = f.Close()
	}
	return issues, nil
}

var (
	reSectionLabel = regexp.MustCompile(`^[[:space:]]*(?:#|//|;)[[:space:]]*([A-Z][A-Za-z]*(?:[[:space:]]+[A-Za-z]+){0,2})[[:space:]]*$`)
	reCommentLine  = regexp.MustCompile(`^[[:space:]]*(?:#|//|;)`)
	reCloser       = regexp.MustCompile(`^[[:space:]]*[\})\];,]`)
	reBlank        = regexp.MustCompile(`^[[:space:]]*$`)
	reDatedEvent   = regexp.MustCompile(`(Tested|Live-discovered|Found|Fixed|Shipped|Broken|Observed|Reproduced|Seen|Hit|Reported|Discovered|Bumped|Landed|Deployed|[Aa]s of)[[:space:]]+(?:[a-z]+[[:space:]]+)?[0-9]{4}-[0-9]{2}(?:-[0-9]{2})?`)
	reTodoTag      = regexp.MustCompile(`(^|[[:space:]])(TODO|FIXME|XXX|HACK)([[:space:]:(]|$)`)
	reTrackingLink = regexp.MustCompile(`#[0-9]+|https?://|see [./a-zA-Z0-9_-]+\.(md|nix|sh|bash|bb|edn|go|rs|py|js|ts|rb|lua|clj|cljs)`)
)

type cmtLine struct {
	text string
	num  int
}

func scanCommentNoise(r io.Reader, rel string) []Issue {
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 64*1024), 4*1024*1024)

	var issues []Issue
	var (
		labelText string
		labelLine int
		block     []cmtLine
	)

	flushBlock := func() {
		if len(block) == 0 {
			return
		}
		hasLink := false
		for _, l := range block {
			if reTrackingLink.MatchString(l.text) {
				hasLink = true
				break
			}
		}
		if !hasLink {
			for _, l := range block {
				if reTodoTag.MatchString(l.text) {
					issues = append(issues, Issue{
						Path:    rel,
						Line:    l.num,
						Message: "bare TODO/FIXME (no #issue or URL in this comment block): " + strings.TrimSpace(l.text),
					})
				}
			}
		}
		block = block[:0]
	}

	lineNo := 0
	for scanner.Scan() {
		lineNo++
		line := scanner.Text()

		if reCommentLine.MatchString(line) {
			block = append(block, cmtLine{text: line, num: lineNo})
			if reDatedEvent.MatchString(line) {
				issues = append(issues, Issue{
					Path:    rel,
					Line:    lineNo,
					Message: "dated event ref (rewrite as present-tense invariant; date belongs in the commit log): " + strings.TrimSpace(line),
				})
			}
			if labelText != "" {
				labelText = ""
				labelLine = 0
			}
			if m := reSectionLabel.FindStringSubmatch(line); m != nil {
				labelText = m[1]
				labelLine = lineNo
			}
			continue
		}

		flushBlock()

		if labelText != "" {
			if reBlank.MatchString(line) {
				continue
			}
			if reCloser.MatchString(line) {
				labelText = ""
				labelLine = 0
				continue
			}
			issues = append(issues, Issue{
				Path:    rel,
				Line:    labelLine,
				Message: "section-label comment `" + labelText + "` above `" + strings.TrimSpace(line) + "`",
			})
			labelText = ""
			labelLine = 0
		}
	}
	flushBlock()
	return issues
}
