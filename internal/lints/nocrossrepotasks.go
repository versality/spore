package lints

import (
	"fmt"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/versality/spore/internal/task/frontmatter"
)

// NoCrossRepoTasks rejects tasks/<slug>.md that cross-link into a
// sibling repo. Two checks:
//
//  1. Slug matches a foreign-repo prefix (ForbiddenSlugs).
//  2. The body's `# Files` section names a foreign-repo path as a
//     write target (ForbiddenPaths), unqualified by `READ-ONLY:`.
//
// Status filter:
//   - active|draft always in scope
//   - other statuses only in scope when the current branch is
//     wt/<slug> and the file's slug matches.
//
// Allowlist exempts slug prefixes that legitimately reference the
// foreign repo (e.g. cross-repo bridge tasks).
type NoCrossRepoTasks struct {
	TasksDir       string
	ForbiddenSlugs map[string]string
	ForbiddenPaths map[string]string
	SlugAllowlist  map[string]bool
}

func (NoCrossRepoTasks) Name() string { return "no-cross-repo-tasks" }

var filesHeadingRE = regexp.MustCompile(`(?m)^#+[ \t]+Files([ \t]|$)`)

func (l NoCrossRepoTasks) Run(root string) ([]Issue, error) {
	own := ownSlug(root)

	inScope := func(slug, status string) bool {
		if status == "active" || status == "draft" {
			return true
		}
		return own != "" && slug == own
	}

	type taskFile struct {
		rel  string
		slug string
		meta frontmatter.Meta
		body []byte
	}
	var tasks []taskFile
	err := forEachTask(root, l.TasksDir, func(rel string, raw []byte) error {
		m, body, err := frontmatter.Parse(raw)
		if err != nil {
			return nil
		}
		slug := strings.TrimSuffix(filepath.Base(rel), ".md")
		tasks = append(tasks, taskFile{
			rel:  rel,
			slug: slug,
			meta: m,
			body: body,
		})
		return nil
	})
	if err != nil {
		return nil, err
	}

	var issues []Issue

	prefixes := sortedKeys(l.ForbiddenSlugs)
	for _, prefix := range prefixes {
		target := l.ForbiddenSlugs[prefix]
		for _, tf := range tasks {
			if !strings.HasPrefix(tf.slug, prefix) {
				continue
			}
			if l.SlugAllowlist[tf.slug] {
				continue
			}
			if !inScope(tf.slug, tf.meta.Status) {
				continue
			}
			issues = append(issues, Issue{
				Path:    tf.rel,
				Message: fmt.Sprintf("foreign-repo slug prefix %q -> belongs in %s", prefix, target),
			})
		}
	}

	patterns := sortedKeys(l.ForbiddenPaths)
	for _, tf := range tasks {
		if !inScope(tf.slug, tf.meta.Status) {
			continue
		}
		section := extractFilesSection(tf.body)
		for _, line := range strings.Split(section, "\n") {
			t := strings.TrimLeft(line, " \t")
			if !strings.HasPrefix(t, "-") {
				continue
			}
			if strings.Contains(line, "READ-ONLY") {
				continue
			}
			for _, pat := range patterns {
				if strings.Contains(line, pat) {
					target := l.ForbiddenPaths[pat]
					issues = append(issues, Issue{
						Path:    tf.rel,
						Message: fmt.Sprintf("write target '%s' in # Files (belongs in %s)", pat, target),
					})
				}
			}
		}
	}

	return issues, nil
}

func sortedKeys(m map[string]string) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

func extractFilesSection(body []byte) string {
	lines := strings.Split(string(body), "\n")
	var out []string
	in := false
	for _, ln := range lines {
		if filesHeadingRE.MatchString(ln) {
			in = true
			continue
		}
		if strings.HasPrefix(strings.TrimLeft(ln, " \t"), "#") {
			if in {
				in = false
			}
			continue
		}
		if in {
			out = append(out, ln)
		}
	}
	return strings.Join(out, "\n")
}
