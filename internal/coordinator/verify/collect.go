package verify

import (
	"bufio"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
)

func resolveRepoRoot() string {
	out, err := exec.Command("git", "rev-parse", "--show-toplevel").Output()
	if err != nil {
		wd, _ := os.Getwd()
		return wd
	}
	root := strings.TrimSpace(string(out))
	if i := strings.Index(root, "/.worktrees/"); i >= 0 {
		root = root[:i]
	}
	return root
}

func resolveMainBranch(root string) string {
	out, err := gitCmd(root, "rev-parse", "--abbrev-ref", "--symbolic-full-name", "HEAD").Output()
	if err != nil {
		return "main"
	}
	branch := strings.TrimSpace(string(out))
	if branch == "main" || branch == "master" {
		return branch
	}
	return "main"
}

func findImplCommit(root, slug, mainBranch string) string {
	if c := findByMergeCommit(root, slug, mainBranch); c != "" {
		return c
	}
	if c := findByReflog(root, slug, mainBranch); c != "" {
		return c
	}
	if c := findByGrepSlug(root, slug, mainBranch); c != "" {
		return c
	}
	return findByFilesTouch(root, slug, mainBranch)
}

func findByMergeCommit(root, slug, mainBranch string) string {
	out, err := gitCmd(root, "log", "--merges", "--format=%h",
		"--grep=into wt/"+slug+"$", mainBranch).Output()
	if err != nil {
		return ""
	}
	mergeSHA := firstLine(string(out))
	if mergeSHA == "" {
		return ""
	}
	out, err = gitCmd(root, "log", "--no-merges", "--format=%h %s",
		mergeSHA+"^2.."+mergeSHA+"^1",
		"--", ":(exclude)tasks/"+slug+".md").Output()
	if err != nil {
		return ""
	}
	return firstLine(string(out))
}

func findByReflog(root, slug, mainBranch string) string {
	out, err := gitCmd(root, "reflog", "show", mainBranch).Output()
	if err != nil {
		return ""
	}
	target := "merge wt/" + slug + ":"
	for _, line := range strings.Split(string(out), "\n") {
		if !strings.Contains(line, target) {
			continue
		}
		parts := strings.Fields(line)
		if len(parts) < 1 {
			continue
		}
		ffSHA := parts[0]
		prevOut, err := gitCmd(root, "reflog", "show", mainBranch).Output()
		if err != nil {
			continue
		}
		refMatch := ""
		if len(parts) > 1 {
			refMatch = parts[1]
		}
		prevSHA := findPrevReflogSHA(string(prevOut), refMatch)
		if prevSHA == "" {
			continue
		}
		out2, err := gitCmd(root, "log", "--no-merges", "--format=%h %s",
			prevSHA+".."+ffSHA, "--", ":(exclude)tasks/"+slug+".md").Output()
		if err != nil {
			continue
		}
		if l := firstLine(string(out2)); l != "" {
			return l
		}
	}
	return ""
}

func findPrevReflogSHA(reflogOutput, refMatch string) string {
	lines := strings.Split(reflogOutput, "\n")
	found := false
	for _, line := range lines {
		if found {
			parts := strings.Fields(line)
			if len(parts) > 0 {
				return parts[0]
			}
		}
		if strings.Contains(line, refMatch) {
			found = true
		}
	}
	return ""
}

func findByGrepSlug(root, slug, mainBranch string) string {
	out, err := gitCmd(root, "log", "--oneline", "--no-merges",
		"--grep="+slug, "--fixed-strings", "-100", mainBranch).Output()
	if err != nil {
		return ""
	}
	for _, line := range strings.Split(string(out), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		parts := strings.SplitN(line, " ", 2)
		if len(parts) < 2 {
			continue
		}
		subj := parts[1]
		if isTaskOrMerge(subj, slug) {
			continue
		}
		return line
	}
	return ""
}

func findByFilesTouch(root, slug, mainBranch string) string {
	taskFile := filepath.Join(root, "tasks", slug+".md")
	content, err := os.ReadFile(taskFile)
	if err != nil {
		return ""
	}

	created := extractFrontmatterField(content, "created")

	filesBlock := extractFilesSection(content)
	paths := extractBacktickPaths(filesBlock)
	if len(paths) == 0 {
		return ""
	}

	args := []string{"log", "--oneline", "--no-merges", "-50", mainBranch, "--"}
	if created != "" {
		args = append(args[:3], append([]string{"--since=" + created}, args[3:]...)...)
	}
	args = append(args, paths...)
	args = append(args, ":(exclude)tasks/"+slug+".md")

	out, err := gitCmd(root, args...).Output()
	if err != nil {
		return ""
	}
	for _, line := range strings.Split(string(out), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		parts := strings.SplitN(line, " ", 2)
		if len(parts) < 2 {
			continue
		}
		if isTaskOrMerge(parts[1], slug) {
			continue
		}
		return line
	}
	return ""
}

func findReflogCommit(root, slug, mainBranch string) string {
	out, err := gitCmd(root, "reflog", "show", "HEAD", "--format=%H\t%gs").Output()
	if err != nil {
		return ""
	}
	lines := strings.Split(string(out), "\n")
	reversed := make([]string, len(lines))
	for i, l := range lines {
		reversed[len(lines)-1-i] = l
	}

	inWindow := false
	for _, line := range reversed {
		parts := strings.SplitN(line, "\t", 2)
		if len(parts) < 2 {
			continue
		}
		sha, subj := parts[0], parts[1]

		if strings.HasSuffix(subj, "to wt/"+slug) && strings.Contains(subj, "checkout: moving from") {
			inWindow = true
			continue
		}
		if strings.HasPrefix(subj, "checkout: moving from wt/"+slug+" to ") {
			inWindow = false
			continue
		}
		if !inWindow {
			continue
		}
		if !strings.HasPrefix(subj, "commit") {
			continue
		}

		if isAncestor(root, sha, mainBranch) {
			continue
		}

		implPaths := nonTaskPaths(root, sha, slug)
		if implPaths == "" {
			continue
		}

		shortSHA := sha
		if len(shortSHA) > 7 {
			shortSHA = shortSHA[:7]
		}
		return shortSHA
	}
	return ""
}

func isAncestor(root, sha, branch string) bool {
	return gitCmd(root, "merge-base", "--is-ancestor", sha, branch).Run() == nil
}

func nonTaskPaths(root, sha, slug string) string {
	out, err := gitCmd(root, "diff-tree", "--no-commit-id", "--name-only", "-r", sha).Output()
	if err != nil {
		return ""
	}
	for _, p := range strings.Split(string(out), "\n") {
		p = strings.TrimSpace(p)
		if p == "" || p == "tasks/"+slug+".md" {
			continue
		}
		return p
	}
	return ""
}

func findMergeEvent(slug, eventsFile string) string {
	if eventsFile == "" {
		eventsFile = defaultEventsFile()
	}
	f, err := os.Open(eventsFile)
	if err != nil {
		return "none"
	}
	defer f.Close()

	var last string
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for scanner.Scan() {
		var ev struct {
			TS      string `json:"ts"`
			Event   string `json:"event"`
			Payload struct {
				Slug   string `json:"slug"`
				To     string `json:"to"`
				Caller string `json:"caller"`
			} `json:"payload"`
		}
		if json.Unmarshal(scanner.Bytes(), &ev) != nil {
			continue
		}
		if ev.Event == "status-flip" && ev.Payload.Slug == slug &&
			ev.Payload.To == "done" && ev.Payload.Caller == slug {
			last = ev.TS + " " + ev.Payload.Slug + "->done"
		}
	}
	if last != "" {
		return last
	}
	return "none"
}

func defaultEventsFile() string {
	if f := os.Getenv("WT_EVENTS_FILE"); f != "" {
		return f
	}
	home, _ := os.UserHomeDir()
	wtState := os.Getenv("WT_STATE")
	if wtState == "" {
		wtState = filepath.Join(home, ".local", "state", "wt")
	}
	return filepath.Join(wtState, "events.jsonl")
}

func readFrontmatterStatus(root, slug string) string {
	content, err := os.ReadFile(filepath.Join(root, "tasks", slug+".md"))
	if err != nil {
		return "?"
	}
	return extractFrontmatterField(content, "status")
}

func extractFrontmatterField(content []byte, field string) string {
	lines := strings.Split(string(content), "\n")
	inFM := false
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "---" {
			if inFM {
				return ""
			}
			inFM = true
			continue
		}
		if !inFM {
			continue
		}
		if strings.HasPrefix(trimmed, field+":") {
			return strings.TrimSpace(trimmed[len(field)+1:])
		}
	}
	return ""
}

func extractFilesSection(content []byte) string {
	lines := strings.Split(string(content), "\n")
	inBlock := false
	var buf strings.Builder
	for _, line := range lines {
		if strings.HasPrefix(line, "# Files") {
			inBlock = true
			continue
		}
		if inBlock && strings.HasPrefix(line, "# ") {
			break
		}
		if inBlock {
			buf.WriteString(line)
			buf.WriteByte('\n')
		}
	}
	return buf.String()
}

func extractBacktickPaths(s string) []string {
	re := regexp.MustCompile("`([^`]+)`")
	matches := re.FindAllStringSubmatch(s, -1)
	var paths []string
	for _, m := range matches {
		paths = append(paths, m[1])
	}
	return paths
}

func hasEvidenceSection(root, slug string) bool {
	content, err := os.ReadFile(filepath.Join(root, "tasks", slug+".md"))
	if err != nil {
		return false
	}
	return strings.Contains(extractFrontmatterField(content, "evidence_required"), "[")
}

func checkEvidence(root, slug string) string {
	taskFile := filepath.Join(root, "tasks", slug+".md")
	content, err := os.ReadFile(taskFile)
	if err != nil {
		return ""
	}

	declaredRaw := extractFrontmatterField(content, "evidence_required")
	if !strings.HasPrefix(declaredRaw, "[") {
		return ""
	}

	bullets := extractEvidenceBullets(string(content))
	var failures []string
	shaRE := regexp.MustCompile(`[0-9a-f]{7,40}`)

	for _, b := range bullets {
		parts := strings.SplitN(b, ":", 2)
		if len(parts) < 2 {
			continue
		}
		kind := strings.TrimSpace(parts[0])
		rest := strings.TrimSpace(parts[1])

		switch kind {
		case "commit":
			sha := shaRE.FindString(rest)
			if sha == "" {
				failures = append(failures, "commit:no-sha-in-bullet")
			} else if gitCmd(root, "rev-parse", "--verify", "--quiet", sha+"^{commit}").Run() != nil {
				failures = append(failures, "commit:"+sha+"-unresolved")
			}
		case "file":
			pathRE := regexp.MustCompile(`[^\s` + "`" + `]*/[^\s` + "`" + `]+`)
			path := pathRE.FindString(rest)
			if path != "" {
				full := filepath.Join(root, path)
				if _, err := os.Stat(full); err != nil {
					failures = append(failures, "file:"+path+"-missing")
				}
			}
		}
	}
	return strings.Join(failures, "|")
}

func extractEvidenceBullets(content string) []string {
	lines := strings.Split(content, "\n")
	inSection := false
	pastFM := false
	fmCount := 0
	var bullets []string

	for _, line := range lines {
		if strings.TrimSpace(line) == "---" {
			fmCount++
			if fmCount >= 2 {
				pastFM = true
			}
			continue
		}
		if !pastFM {
			continue
		}
		if strings.HasPrefix(line, "## ") {
			heading := strings.TrimSpace(line[3:])
			if strings.EqualFold(heading, "Evidence") {
				inSection = true
				continue
			}
			if inSection {
				break
			}
			continue
		}
		if !inSection {
			continue
		}
		trimmed := strings.TrimLeft(line, " \t")
		if strings.HasPrefix(trimmed, "- ") {
			bullets = append(bullets, strings.TrimSpace(trimmed[2:]))
		}
	}
	return bullets
}

func isTaskOrMerge(subj, slug string) bool {
	prefixes := []string{
		"tasks/" + slug + ":",
		"tasks/" + slug + ".md",
		"Merge branch",
		"Merge",
	}
	for _, p := range prefixes {
		if strings.HasPrefix(subj, p) {
			return true
		}
	}
	return false
}

func gitCmd(root string, args ...string) *exec.Cmd {
	return exec.Command("git", append([]string{"-C", root}, args...)...)
}
