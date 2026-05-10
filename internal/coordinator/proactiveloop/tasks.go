package proactiveloop

import (
	"bufio"
	"os"
	"path/filepath"
	"strings"
)

// taskIndex is the result of scanning all tasks/*.md across the
// configured projects. statusBySlug holds every slug we saw (any host);
// remoteHosts holds slugs whose host frontmatter does not equal
// localHost; local holds the per-task-file paths to scan further.
type taskIndex struct {
	statusBySlug map[string]string
	remoteHosts  map[string]bool
	local        []string
}

func loadTasks(projects []string, localHost string) taskIndex {
	idx := taskIndex{
		statusBySlug: map[string]string{},
		remoteHosts:  map[string]bool{},
	}
	for _, project := range projects {
		taskDir := filepath.Join(project, "tasks")
		entries, err := os.ReadDir(taskDir)
		if err != nil {
			continue
		}
		for _, e := range entries {
			name := e.Name()
			if !strings.HasSuffix(name, ".md") {
				continue
			}
			slug := strings.TrimSuffix(name, ".md")
			if slug == "README" {
				continue
			}
			path := filepath.Join(taskDir, name)
			fm := parseFrontmatter(path)
			idx.statusBySlug[slug] = fm["status"]
			host := fm["host"]
			if host != "" && host != localHost {
				idx.remoteHosts[slug] = true
				continue
			}
			idx.local = append(idx.local, path)
		}
	}
	return idx
}

// parseFrontmatter reads the first YAML frontmatter block (between
// `---` lines) and returns a flat key->value map. List values
// (`needs:` followed by `- foo`) are not flattened here; see
// taskNeeds for that.
func parseFrontmatter(path string) map[string]string {
	out := map[string]string{}
	f, err := os.Open(path)
	if err != nil {
		return out
	}
	defer f.Close()
	scan := bufio.NewScanner(f)
	state := 0
	for scan.Scan() {
		line := scan.Text()
		if strings.TrimSpace(line) == "---" {
			state++
			if state == 2 {
				break
			}
			continue
		}
		if state != 1 {
			continue
		}
		if strings.HasPrefix(line, " ") || strings.HasPrefix(line, "\t") {
			continue
		}
		colon := strings.IndexByte(line, ':')
		if colon < 0 {
			continue
		}
		key := strings.TrimSpace(line[:colon])
		val := strings.TrimSpace(line[colon+1:])
		out[key] = val
	}
	return out
}

// taskNeeds returns the list items under a `needs:` key in the task
// frontmatter. Trailing comments are stripped; whitespace trimmed.
func taskNeeds(path string) []string {
	f, err := os.Open(path)
	if err != nil {
		return nil
	}
	defer f.Close()
	scan := bufio.NewScanner(f)
	var out []string
	state := 0 // 0 pre-fm, 1 in-fm, 2 done
	inList := false
	for scan.Scan() {
		line := scan.Text()
		if strings.TrimSpace(line) == "---" {
			state++
			if state == 2 {
				break
			}
			continue
		}
		if state != 1 {
			continue
		}
		if inList {
			trimmed := strings.TrimLeft(line, " \t")
			if strings.HasPrefix(trimmed, "- ") {
				v := strings.TrimSpace(trimmed[2:])
				if i := strings.IndexByte(v, '#'); i >= 0 {
					v = strings.TrimSpace(v[:i])
				}
				if v != "" {
					out = append(out, v)
				}
				continue
			}
			inList = false
		}
		if strings.TrimSpace(line) == "needs:" {
			inList = true
		}
	}
	return out
}

// fmField returns the value for key from path's frontmatter, or "".
func fmField(path, key string) string {
	return parseFrontmatter(path)[key]
}
