package lints

import (
	"fmt"
	"path/filepath"
	"sort"
	"strings"

	"github.com/versality/spore/internal/task/frontmatter"
)

// TaskNeeds validates `needs:` frontmatter on tasks/<slug>.md files.
// Ports nix-config harness/check-task-needs.sh. Rejects: scalar or
// inline-list `needs:` shape, references to missing slugs, self-
// references, and dependency cycles. Absent or canonically-empty
// `needs:` is fine (backwards-compatible with older tasks).
//
// Bash also emits a non-failing warning when a dep is currently
// status=blocked; spore has no warn channel, and the bash exit was
// still 0, so we drop that signal here.
type TaskNeeds struct {
	TasksDir string
}

func (TaskNeeds) Name() string { return "task-needs" }

func (l TaskNeeds) Run(root string) ([]Issue, error) {
	dir := l.TasksDir
	if dir == "" {
		dir = "tasks"
	}

	type entry struct {
		slug string
		rel  string
		deps []string
	}
	var slugs []string
	known := map[string]bool{}
	files := map[string]*entry{}

	var issues []Issue

	err := forEachTask(root, l.TasksDir, func(rel string, raw []byte) error {
		m, _, err := frontmatter.Parse(raw)
		if err != nil {
			return nil
		}
		slug := m.Slug
		if slug == "" {
			slug = strings.TrimSuffix(filepath.Base(rel), ".md")
		}
		slugs = append(slugs, slug)
		known[slug] = true
		files[slug] = &entry{slug: slug, rel: rel}

		bad := needsShapeError(raw)
		if bad != "" {
			issues = append(issues, Issue{
				Path:    rel,
				Message: fmt.Sprintf("needs: must be a YAML block list (got: %s)", bad),
			})
			return nil
		}
		files[slug].deps = append([]string(nil), m.Needs...)
		return nil
	})
	if err != nil {
		return nil, err
	}

	for _, slug := range slugs {
		e := files[slug]
		for _, dep := range e.deps {
			switch {
			case dep == slug:
				issues = append(issues, Issue{
					Path:    e.rel,
					Message: fmt.Sprintf("needs: contains self-reference '%s'", slug),
				})
			case !known[dep]:
				issues = append(issues, Issue{
					Path:    e.rel,
					Message: fmt.Sprintf("needs: '%s' has no task file (%s/%s.md)", dep, dir, dep),
				})
			}
		}
	}

	// Cycle detection. Edge set is restricted to known deps that
	// are not self-references; missing-target and self-ref are
	// already reported above and would otherwise double-fire here.
	graph := map[string][]string{}
	for _, slug := range slugs {
		e := files[slug]
		for _, dep := range e.deps {
			if dep == slug || !known[dep] {
				continue
			}
			graph[slug] = append(graph[slug], dep)
		}
	}

	color := map[string]int{} // 0 white, 1 gray, 2 black
	reported := map[string]bool{}
	var stack []string
	var visit func(node string)
	visit = func(node string) {
		color[node] = 1
		stack = append(stack, node)
		for _, dep := range graph[node] {
			switch color[dep] {
			case 1:
				first := 0
				for i, n := range stack {
					if n == dep {
						first = i
						break
					}
				}
				cycle := append([]string(nil), stack[first:]...)
				sorted := append([]string(nil), cycle...)
				sort.Strings(sorted)
				key := strings.Join(sorted, ",")
				if reported[key] {
					continue
				}
				reported[key] = true
				path := strings.Join(cycle, " -> ") + " -> " + dep
				issues = append(issues, Issue{
					Message: fmt.Sprintf("cycle detected: %s", path),
				})
			case 0:
				visit(dep)
			}
		}
		color[node] = 2
		stack = stack[:len(stack)-1]
	}
	for _, slug := range slugs {
		if color[slug] == 0 {
			visit(slug)
		}
	}

	return issues, nil
}

// needsShapeError returns the offending content after `needs:` when the
// field is set to anything other than empty (block-list header) or
// `[]`. Returns "" when the field is absent or canonically empty.
// Only inspects the frontmatter region (between the first pair of
// `---` fence lines).
func needsShapeError(raw []byte) string {
	src := string(raw)
	// Find frontmatter bounds.
	lines := strings.Split(src, "\n")
	if len(lines) == 0 || strings.TrimRight(lines[0], " \t\r") != "---" {
		return ""
	}
	for i := 1; i < len(lines); i++ {
		s := strings.TrimRight(lines[i], " \t\r")
		if s == "---" {
			return ""
		}
		if !strings.HasPrefix(s, "needs:") {
			continue
		}
		rest := strings.TrimSpace(s[len("needs:"):])
		if rest == "" || rest == "[]" {
			return ""
		}
		return rest
	}
	return ""
}
