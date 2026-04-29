// Package task is the data layer for spore tasks: slug shaping, file
// allocation, and a directory-wide listing built on top of the
// frontmatter package. Pure stdlib.
package task

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/versality/spore/internal/task/frontmatter"
)

// MaxSlugLen is the upper bound on a Slugify result. Long titles are
// truncated; the trailing dash (if any) is then trimmed.
const MaxSlugLen = 50

// Slugify reduces a free-form title to an ASCII, lowercase, dash-
// separated identifier suitable for a tasks/<slug>.md filename. Non-
// alphanumeric runes (including any non-ASCII letter) collapse into
// a single `-`; leading and trailing dashes are trimmed; the result
// is capped at MaxSlugLen.
func Slugify(title string) string {
	var b strings.Builder
	b.Grow(len(title))
	prevDash := false
	for _, r := range title {
		switch {
		case r >= 'A' && r <= 'Z':
			b.WriteByte(byte(r) + 32)
			prevDash = false
		case (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9'):
			b.WriteByte(byte(r))
			prevDash = false
		default:
			if !prevDash && b.Len() > 0 {
				b.WriteByte('-')
				prevDash = true
			}
		}
	}
	s := strings.TrimRight(b.String(), "-")
	if len(s) > MaxSlugLen {
		s = strings.TrimRight(s[:MaxSlugLen], "-")
	}
	return s
}

// Allocate finds the first free slug-with-suffix variant under
// tasksDir. It returns slug if `tasksDir/slug.md` is free, else
// `slug-2`, `slug-3`, ... up to `slug-99`. Errors if every variant
// is taken or stat fails for an unrelated reason.
func Allocate(tasksDir, slug string) (string, error) {
	if slug == "" {
		return "", fmt.Errorf("task: empty slug")
	}
	for n := 1; n <= 99; n++ {
		candidate := slug
		if n > 1 {
			candidate = fmt.Sprintf("%s-%d", slug, n)
		}
		path := filepath.Join(tasksDir, candidate+".md")
		_, err := os.Stat(path)
		if os.IsNotExist(err) {
			return candidate, nil
		}
		if err != nil {
			return "", err
		}
	}
	return "", fmt.Errorf("task: no slug variant free for %q (up to slug-99)", slug)
}

// List reads tasksDir, parses frontmatter from each `*.md` entry,
// and returns the resulting Meta values sorted by Slug. Files
// without parseable frontmatter (e.g. tasks/README.md) are skipped
// silently so unrelated markdown can coexist.
func List(tasksDir string) ([]frontmatter.Meta, error) {
	entries, err := os.ReadDir(tasksDir)
	if err != nil {
		return nil, err
	}
	var metas []frontmatter.Meta
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".md") {
			continue
		}
		path := filepath.Join(tasksDir, e.Name())
		b, err := os.ReadFile(path)
		if err != nil {
			return nil, err
		}
		m, _, err := frontmatter.Parse(b)
		if err != nil {
			continue
		}
		metas = append(metas, m)
	}
	sort.Slice(metas, func(i, j int) bool { return metas[i].Slug < metas[j].Slug })
	return metas, nil
}
