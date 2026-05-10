package idlewatchdog

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// scanTasks walks tasks/*.md and returns the slug->status map plus the
// set of slugs whose `host:` differs from localHost (those are skipped
// when comparing state.md Active rows against frontmatter).
func scanTasks(tasksDir, localHost string) (map[string]string, map[string]bool) {
	statusBySlug := map[string]string{}
	remote := map[string]bool{}
	entries, err := os.ReadDir(tasksDir)
	if err != nil {
		return statusBySlug, remote
	}
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		if !strings.HasSuffix(name, ".md") {
			continue
		}
		slug := strings.TrimSuffix(name, ".md")
		if slug == "README" {
			continue
		}
		fm := readFrontmatter(filepath.Join(tasksDir, name))
		statusBySlug[slug] = fm["status"]
		if h := fm["host"]; h != "" && h != localHost {
			remote[slug] = true
		}
	}
	return statusBySlug, remote
}

// readFrontmatter returns the flat key->value map between the leading
// `---` markers. Indented (list) values are skipped.
func readFrontmatter(path string) map[string]string {
	body, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	out := map[string]string{}
	inFM := false
	closed := false
	for _, line := range strings.Split(string(body), "\n") {
		t := strings.TrimRight(line, " \t")
		if t == "---" {
			if inFM {
				closed = true
				break
			}
			inFM = true
			continue
		}
		if !inFM {
			continue
		}
		if strings.HasPrefix(line, " ") || strings.HasPrefix(line, "\t") {
			continue
		}
		i := strings.IndexByte(line, ':')
		if i < 0 {
			continue
		}
		out[strings.TrimSpace(line[:i])] = strings.TrimSpace(line[i+1:])
	}
	if !closed && len(out) == 0 {
		return nil
	}
	return out
}

// activeStateSlugs returns the slug column from the state.md
// "## Active tasks" pipe table. Header / divider rows are skipped.
func activeStateSlugs(state string) []string {
	if state == "" {
		return nil
	}
	var out []string
	for _, line := range sectionLines(state, "Active tasks") {
		if !strings.HasPrefix(line, "|") || dividerRowRE.MatchString(line) {
			continue
		}
		parts := strings.Split(line, "|")
		if len(parts) < 3 {
			continue
		}
		slug := strings.TrimSpace(parts[1])
		if slug == "" || slug == "slug" {
			continue
		}
		out = append(out, slug)
	}
	return out
}

var dividerRowRE = regexp.MustCompile(`^\|[-\s|]+\|?$`)

// openRoadmapItems returns the text after the unchecked "- [ ] "
// marker for each open Roadmap line in state.md.
func openRoadmapItems(state string) []string {
	if state == "" {
		return nil
	}
	var out []string
	for _, line := range sectionLines(state, "Roadmap") {
		t := strings.TrimLeft(line, " \t")
		switch {
		case strings.HasPrefix(t, "- [ ] "):
			out = append(out, strings.TrimSpace(t[len("- [ ] "):]))
		case strings.HasPrefix(t, "- [ ]\t"):
			out = append(out, strings.TrimSpace(t[len("- [ ]\t"):]))
		}
	}
	return out
}

// sectionLines returns the lines under the `## <name>` header up to
// the next `## ` heading (exclusive). Header itself is excluded.
func sectionLines(state, name string) []string {
	var lines []string
	in := false
	for _, line := range strings.Split(state, "\n") {
		if strings.HasPrefix(line, "## ") {
			if in {
				return lines
			}
			if strings.TrimSpace(strings.TrimPrefix(line, "## ")) == name {
				in = true
			}
			continue
		}
		if in {
			lines = append(lines, line)
		}
	}
	return lines
}

func stateOpenQuestionFor(state, slug string) bool {
	if state == "" {
		return false
	}
	prefix := "- " + slug + ":"
	for _, line := range sectionLines(state, "Open operator questions") {
		if strings.HasPrefix(line, prefix) {
			return true
		}
	}
	return false
}

func stateRecentOperatorNoticeFor(state, slug string) bool {
	if state == "" || slug == "" {
		return false
	}
	for _, line := range sectionLines(state, "Recent events") {
		if !strings.Contains(line, slug) {
			continue
		}
		for _, kw := range operatorTouchKeywords {
			if strings.Contains(line, kw) {
				return true
			}
		}
	}
	return false
}

var operatorTouchKeywords = []string{"operator", "notify", "notification", "attention", "asked"}

func notifyThreadFor(path, slug string) bool {
	if path == "" || slug == "" {
		return false
	}
	body, err := os.ReadFile(path)
	if err != nil {
		return false
	}
	return strings.Contains(string(body), `"slug":"`+slug+`"`)
}

// coastCandidateAllowed mirrors the bash gate: skip slugs with an open
// question, recent operator notice, or notify thread; skip under
// "ration"; require a Roadmap mention under "tighten".
func coastCandidateAllowed(cfg Config, state, budget, slug string) bool {
	if stateOpenQuestionFor(state, slug) {
		return false
	}
	if stateRecentOperatorNoticeFor(state, slug) {
		return false
	}
	if notifyThreadFor(cfg.NotifyThreads, slug) {
		return false
	}
	if budget == "ration" {
		return false
	}
	if budget != "tighten" {
		return true
	}
	if state == "" {
		return false
	}
	for _, line := range sectionLines(state, "Roadmap") {
		t := strings.TrimLeft(line, " \t")
		if strings.HasPrefix(t, "- [ ]") && strings.Contains(line, slug) {
			return true
		}
	}
	return false
}

// resolveBudgetAdvice fills BudgetAdvice from env or runs
// skyhelm-budget query and parses out "advice":"...".
func resolveBudgetAdvice(cfg Config) string {
	if v := os.Getenv("SKYHELM_QUEUE_BUDGET_ADVICE"); v != "" {
		if v == "tighten" || v == "ration" {
			return v
		}
		return "ok"
	}
	if _, err := cfg.LookPath("skyhelm-budget"); err != nil {
		return "ok"
	}
	out, _ := runCapture(cfg.Run, "skyhelm-budget", "query")
	m := budgetAdviceRE.FindStringSubmatch(out)
	if len(m) == 2 && (m[1] == "tighten" || m[1] == "ration") {
		return m[1]
	}
	return "ok"
}

var budgetAdviceRE = regexp.MustCompile(`"advice"\s*:\s*"([^"]*)"`)
