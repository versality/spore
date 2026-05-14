package lints

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/versality/spore/internal/task/frontmatter"
)

// TechDebtRulings is the pre-merge gate that tasks/tech-debt-*.md
// flipped to status=done MUST have a matching ruling row in
// RulingsPath. The slug encodes the hash; the rulings ledger maps
// the hash to the per-finding decision (`fix`/`ignore`/`defer`).
//
// Defaults match nix-config: TasksDir=tasks,
// RulingsPath=harness/tech-debt-rulings.md.
type TechDebtRulings struct {
	TasksDir    string
	RulingsPath string
}

func (TechDebtRulings) Name() string { return "tech-debt-rulings" }

var techDebtHashRE = regexp.MustCompile(`^[0-9a-f]{8}$`)

func (l TechDebtRulings) Run(root string) ([]Issue, error) {
	dir := l.TasksDir
	if dir == "" {
		dir = "tasks"
	}
	rulingsPath := l.RulingsPath
	if rulingsPath == "" {
		rulingsPath = "harness/tech-debt-rulings.md"
	}

	rulings, err := readRulings(filepath.Join(root, rulingsPath))
	if err != nil {
		return nil, err
	}

	tasks, err := listTechDebtDoneTasks(filepath.Join(root, dir))
	if err != nil {
		return nil, err
	}

	var issues []Issue
	for _, t := range tasks {
		if _, ok := rulings[t.hash]; ok {
			continue
		}
		rel := filepath.ToSlash(filepath.Join(dir, t.name))
		issues = append(issues, Issue{
			Path:    rel,
			Message: fmt.Sprintf("status=done but no ruling row in %s for hash=%s", rulingsPath, t.hash),
		})
	}
	return issues, nil
}

type techDebtTask struct {
	name string
	hash string
	slug string
	path string
}

func listTechDebtDoneTasks(absDir string) ([]techDebtTask, error) {
	entries, err := os.ReadDir(absDir)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var out []techDebtTask
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasPrefix(name, "tech-debt-") || !strings.HasSuffix(name, ".md") {
			continue
		}
		path := filepath.Join(absDir, name)
		raw, err := os.ReadFile(path)
		if err != nil {
			return nil, err
		}
		m, _, err := frontmatter.Parse(raw)
		if err != nil {
			continue
		}
		if m.Status != "done" {
			continue
		}
		slug := strings.TrimSuffix(name, ".md")
		idx := strings.LastIndex(slug, "-")
		if idx < 0 {
			continue
		}
		hash := slug[idx+1:]
		if !techDebtHashRE.MatchString(hash) {
			continue
		}
		out = append(out, techDebtTask{name: name, hash: hash, slug: slug, path: path})
	}
	return out, nil
}

// readRulings parses rows of `| ... | hash | ... | ... | decision | ... |`
// from path. Returns map[hash]decision restricted to fix/ignore/defer.
func readRulings(path string) (map[string]string, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return map[string]string{}, nil
		}
		return nil, err
	}
	out := map[string]string{}
	lines := strings.Split(string(raw), "\n")
	for i, ln := range lines {
		if i < 2 {
			continue
		}
		fields := strings.Split(ln, "|")
		if len(fields) < 6 {
			continue
		}
		hash := strings.TrimSpace(fields[1])
		decision := strings.TrimSpace(fields[4])
		if !techDebtHashRE.MatchString(hash) {
			continue
		}
		switch decision {
		case "fix", "ignore", "defer":
			out[hash] = decision
		}
	}
	return out, nil
}

// TaskDoneZeroCommits is the pre-merge gate that tasks/tech-debt-*.md
// flipped to status=done with a `decision=fix` ruling row MUST have
// at least one worker commit (Task=<slug> trailer) that touches files
// outside the bookkeeping pair (the task file itself and the rulings
// ledger).
type TaskDoneZeroCommits struct {
	TasksDir    string
	RulingsPath string
}

func (TaskDoneZeroCommits) Name() string { return "task-done-zero-commits" }

func (l TaskDoneZeroCommits) Run(root string) ([]Issue, error) {
	dir := l.TasksDir
	if dir == "" {
		dir = "tasks"
	}
	rulingsPath := l.RulingsPath
	if rulingsPath == "" {
		rulingsPath = "harness/tech-debt-rulings.md"
	}

	rulings, err := readRulings(filepath.Join(root, rulingsPath))
	if err != nil {
		return nil, err
	}

	tasks, err := listTechDebtDoneTasks(filepath.Join(root, dir))
	if err != nil {
		return nil, err
	}

	var issues []Issue
	for _, t := range tasks {
		if rulings[t.hash] != "fix" {
			continue
		}
		taskRel := filepath.ToSlash(filepath.Join(dir, t.name))
		bookkeeping := map[string]bool{
			taskRel:     true,
			rulingsPath: true,
		}
		ok, err := substantiveCommitFor(root, t.slug, bookkeeping)
		if err != nil {
			return nil, err
		}
		if !ok {
			issues = append(issues, Issue{
				Path:    taskRel,
				Message: fmt.Sprintf("status=done with decision=fix but no Task=%s commit touches files outside %s / %s", t.slug, rulingsPath, taskRel),
			})
		}
	}
	return issues, nil
}

func substantiveCommitFor(root, slug string, bookkeeping map[string]bool) (bool, error) {
	abs, err := filepath.Abs(root)
	if err != nil {
		return false, err
	}
	cmd := exec.Command("git", "-c", "safe.directory="+abs, "-C", root,
		"log", "--all", "--format=%H", "--grep", "^Task: "+slug+"$")
	var out bytes.Buffer
	cmd.Stdout = &out
	if err := cmd.Run(); err != nil {
		return false, err
	}
	for _, sha := range strings.Split(strings.TrimSpace(out.String()), "\n") {
		if sha == "" {
			continue
		}
		paths, err := commitPaths(root, sha)
		if err != nil {
			return false, err
		}
		for _, p := range paths {
			if !bookkeeping[p] {
				return true, nil
			}
		}
	}
	return false, nil
}

func commitPaths(root, sha string) ([]string, error) {
	abs, err := filepath.Abs(root)
	if err != nil {
		return nil, err
	}
	cmd := exec.Command("git", "-c", "safe.directory="+abs, "-C", root,
		"show", "--name-only", "--format=", sha)
	var out bytes.Buffer
	cmd.Stdout = &out
	if err := cmd.Run(); err != nil {
		return nil, err
	}
	var paths []string
	for _, p := range strings.Split(strings.TrimSpace(out.String()), "\n") {
		if p != "" {
			paths = append(paths, p)
		}
	}
	return paths, nil
}
