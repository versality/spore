package proactiveloop

import (
	"bufio"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// readProjects returns the project roots to scan: the contents of
// $WT_PROJECTS_FILE if it exists, otherwise the current git worktree
// when its root has a tasks/ dir.
func readProjects(cfg Config) []string {
	if f, err := os.Open(cfg.ProjectsFile); err == nil {
		defer f.Close()
		var out []string
		scan := bufio.NewScanner(f)
		for scan.Scan() {
			line := trimComment(scan.Text())
			if line != "" {
				out = append(out, line)
			}
		}
		return out
	}
	if root, ok := gitWorktreeRoot(); ok {
		if st, err := os.Stat(filepath.Join(root, "tasks")); err == nil && st.IsDir() {
			return []string{root}
		}
	}
	return nil
}

func gitWorktreeRoot() (string, bool) {
	out, err := exec.Command("git", "rev-parse", "--git-common-dir").Output()
	if err != nil {
		return "", false
	}
	dir := strings.TrimSpace(string(out))
	if dir == "" {
		return "", false
	}
	if !filepath.IsAbs(dir) {
		cwd, err := os.Getwd()
		if err != nil {
			return "", false
		}
		dir = filepath.Join(cwd, dir)
	}
	root := filepath.Clean(filepath.Join(dir, ".."))
	return root, true
}

// firstProject returns the first project root, or "" if none.
func firstProject(projects []string) string {
	for _, p := range projects {
		if p != "" {
			return p
		}
	}
	return ""
}

// projectName returns the basename of the resolved path, mirroring
// `readlink -f $p; ${p##*/}`.
func projectName(p string) string {
	resolved, err := filepath.EvalSymlinks(p)
	if err != nil {
		resolved = p
	}
	return filepath.Base(resolved)
}

func trimComment(line string) string {
	if i := strings.IndexByte(line, '#'); i >= 0 {
		line = line[:i]
	}
	return strings.TrimSpace(line)
}
