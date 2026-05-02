// Package verify ports the coordinator-verify-done.sh verdict logic to Go.
// Given a slug and a project root, it inspects git history, wt-task
// event logs, and claude-code session transcripts to determine whether
// a rower's done flip is backed by real evidence.
package verify

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

type Verdict string

const (
	RealImpl              Verdict = "real-impl"
	RationalClose         Verdict = "rational-close"
	CrossRepo             Verdict = "cross-repo"
	SuspectHallucination  Verdict = "suspect-hallucination"
	BogusEvidence         Verdict = "bogus-evidence"
	LostToReflog          Verdict = "lost-to-reflog"
	Unknown               Verdict = "unknown"
)

type Result struct {
	Slug              string  `json:"slug"`
	Verdict           Verdict `json:"verdict"`
	GitCommit         string  `json:"git_commit"`
	MergeEntry        string  `json:"merge_entry"`
	FinalTool         string  `json:"final_tool"`
	FinalText         string  `json:"final_text"`
	LastTimestamp      string  `json:"last_timestamp"`
	FrontmatterStatus string  `json:"frontmatter_status"`
	CrossRepoPath     string  `json:"cross_repo_path,omitempty"`
	ReflogSHA         string  `json:"reflog_sha,omitempty"`
	EvidenceStatus    string  `json:"evidence_status,omitempty"`
	EvidenceFailures  string  `json:"evidence_failures,omitempty"`
}

type Config struct {
	ProjectRoot string
	EventsFile  string
	ProjectsDir string
}

var (
	rationalRE   = regexp.MustCompile(`(?i)(abandon|already (done|shipped|landed)|shipped in [a-f0-9]{7,40}|superseded|replaced by|rejected|rejection|out of bot reach|work is done|nothing (left|to do)|administrative close)`)
	completionRE = regexp.MustCompile(`(?i)(\bmerged\b|\bdone\.|\bcompleted\b|fast-forward|wt merge succeeded|all green)`)
)

func Verify(slug string, cfg Config) Result {
	r := Result{
		Slug:         slug,
		Verdict:      Unknown,
		MergeEntry:   "none",
		FinalTool:    "none",
		LastTimestamp: "?",
		FrontmatterStatus: "?",
	}

	root := cfg.ProjectRoot
	if root == "" {
		root = resolveRepoRoot()
	}

	mainBranch := resolveMainBranch(root)

	implCommit := findImplCommit(root, slug, mainBranch)
	r.GitCommit = implCommit

	reflogSHA := ""
	if implCommit == "" {
		reflogSHA = findReflogCommit(root, slug, mainBranch)
	}
	r.ReflogSHA = reflogSHA

	mergeEntry := findMergeEvent(slug, cfg.EventsFile)
	r.MergeEntry = mergeEntry

	session := findSessionFile(slug, cfg.ProjectsDir)
	finalTool, finalText, lastTS, gitCommitSeen, wtMergeSeen, crossRepoPath := analyzeSession(session)
	r.FinalTool = finalTool
	r.FinalText = truncate(finalText, 200)
	r.LastTimestamp = lastTS
	r.CrossRepoPath = crossRepoPath

	r.FrontmatterStatus = readFrontmatterStatus(root, slug)

	evidenceFailures := checkEvidence(root, slug)
	if evidenceFailures != "" {
		r.EvidenceStatus = "failed"
		r.EvidenceFailures = evidenceFailures
	} else if hasEvidenceSection(root, slug) {
		r.EvidenceStatus = "ok"
	}

	r.Verdict = decide(implCommit, reflogSHA, mergeEntry, finalTool,
		finalText, crossRepoPath, evidenceFailures,
		gitCommitSeen, wtMergeSeen)

	return r
}

func decide(implCommit, reflogSHA, mergeEntry, finalTool, finalText,
	crossRepoPath, evidenceFailures string,
	gitCommitSeen, wtMergeSeen bool) Verdict {

	if evidenceFailures != "" {
		return BogusEvidence
	}
	if implCommit != "" {
		return RealImpl
	}
	if reflogSHA != "" {
		return LostToReflog
	}
	if crossRepoPath != "" {
		return CrossRepo
	}

	ftLower := strings.ToLower(finalText)
	if finalTool == "wt-abandon" || finalTool == "tell-abandoned" ||
		rationalRE.MatchString(ftLower) {
		return RationalClose
	}

	if mergeEntry == "none" && !gitCommitSeen && !wtMergeSeen &&
		completionRE.MatchString(ftLower) {
		return SuspectHallucination
	}

	return Unknown
}

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
	// simplified: find the line after the matching reflog entry
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

func findSessionFile(slug, projectsDir string) string {
	if projectsDir == "" {
		home, _ := os.UserHomeDir()
		projectsDir = filepath.Join(home, ".claude", "projects")
	}
	active := filepath.Join(projectsDir, "-home-sky-consumer-config--worktrees-"+slug)
	if f := newestJSONL(active); f != "" {
		return f
	}
	entries, err := filepath.Glob(active + ".archived-*")
	if err != nil || len(entries) == 0 {
		return ""
	}
	var newest string
	var newestTime time.Time
	for _, d := range entries {
		fi, err := os.Stat(d)
		if err != nil || !fi.IsDir() {
			continue
		}
		if newest == "" || fi.ModTime().After(newestTime) {
			newest = d
			newestTime = fi.ModTime()
		}
	}
	if newest != "" {
		return newestJSONL(newest)
	}
	return ""
}

func newestJSONL(dir string) string {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return ""
	}
	var newest string
	var newestTime time.Time
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".jsonl") {
			continue
		}
		fi, err := e.Info()
		if err != nil {
			continue
		}
		if newest == "" || fi.ModTime().After(newestTime) {
			newest = filepath.Join(dir, e.Name())
			newestTime = fi.ModTime()
		}
	}
	return newest
}

type transcriptMsg struct {
	Type    string `json:"type"`
	Message struct {
		Content []struct {
			Type  string `json:"type"`
			Text  string `json:"text,omitempty"`
			Name  string `json:"name,omitempty"`
			Input struct {
				Command string `json:"command,omitempty"`
			} `json:"input,omitempty"`
		} `json:"content"`
	} `json:"message"`
	Timestamp string `json:"timestamp"`
}

func analyzeSession(path string) (finalTool, finalText, lastTS string, gitCommitSeen, wtMergeSeen bool, crossRepoPath string) {
	finalTool = "none"
	lastTS = "?"
	if path == "" {
		return
	}

	f, err := os.Open(path)
	if err != nil {
		return
	}
	defer f.Close()

	var allBash []string
	var lastBash string
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 0, 64*1024), 16*1024*1024)

	for scanner.Scan() {
		var msg transcriptMsg
		if json.Unmarshal(scanner.Bytes(), &msg) != nil {
			continue
		}
		if msg.Type != "assistant" {
			continue
		}
		if msg.Timestamp != "" {
			lastTS = msg.Timestamp
		}
		for _, c := range msg.Message.Content {
			if c.Type == "text" {
				finalText = c.Text
			}
			if c.Type == "tool_use" && c.Name == "Bash" && c.Input.Command != "" {
				lastBash = c.Input.Command
				allBash = append(allBash, c.Input.Command)
			}
		}
	}

	switch {
	case strings.HasPrefix(lastBash, "wt abandon"):
		finalTool = "wt-abandon"
	case strings.Contains(lastBash, "wt merge"):
		finalTool = "wt-merge"
	case strings.Contains(lastBash, "wt task done"):
		finalTool = "wt-task-done"
	case strings.Contains(lastBash, "wt task tell coordinator") && strings.Contains(lastBash, "abandoned"):
		finalTool = "tell-abandoned"
	case lastBash == "":
		finalTool = "none"
	default:
		finalTool = "other"
	}

	gitCommitRE := regexp.MustCompile(`(^|[\s;|&])git(\s+-C\s+\S+)?\s+commit\b`)
	wtMergeRE := regexp.MustCompile(`(^|[\s;|&])wt merge\b`)
	crossRepoRE := regexp.MustCompile(`git -C (\S+)`)

	for _, cmd := range allBash {
		if gitCommitRE.MatchString(cmd) {
			gitCommitSeen = true
		}
		if wtMergeRE.MatchString(cmd) {
			wtMergeSeen = true
		}
		matches := crossRepoRE.FindAllStringSubmatch(cmd, -1)
		for _, m := range matches {
			p := strings.Trim(m[1], `")'`)
			if p == "" {
				continue
			}
			if strings.HasPrefix(p, "/home/sky/consumer-config") || strings.HasPrefix(p, "~/consumer-config") {
				continue
			}
			if strings.HasPrefix(p, "~/projects/") || strings.HasPrefix(p, "/home/sky/projects/") {
				crossRepoPath = p
			} else if crossRepoPath == "" {
				crossRepoPath = p
			}
		}
	}

	finalText = strings.ReplaceAll(finalText, "\n", " ")
	finalText = collapseSpaces(finalText)
	return
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

func firstLine(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i]
	}
	return s
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n]
}

func collapseSpaces(s string) string {
	prev := false
	var buf strings.Builder
	for _, r := range s {
		if r == ' ' || r == '\t' {
			if !prev {
				buf.WriteByte(' ')
			}
			prev = true
		} else {
			buf.WriteRune(r)
			prev = false
		}
	}
	return buf.String()
}

// Format renders the result in the same block format as
// coordinator-verify-done.sh for drop-in compatibility.
func (r Result) Format() string {
	var buf strings.Builder
	fmt.Fprintf(&buf, "%s: %s\n", r.Slug, r.Verdict)
	fmt.Fprintf(&buf, "  git: %s\n", orNone(r.GitCommit))
	fmt.Fprintf(&buf, "  events: %s\n", r.MergeEntry)
	fmt.Fprintf(&buf, "  session: %s @ %s\n", r.FinalTool, r.LastTimestamp)
	fmt.Fprintf(&buf, "  final-text: %s\n", orDefault(r.FinalText, "(no assistant text)"))
	fmt.Fprintf(&buf, "  frontmatter: %s\n", r.FrontmatterStatus)
	if r.CrossRepoPath != "" {
		fmt.Fprintf(&buf, "  cross-repo: %s\n", r.CrossRepoPath)
	}
	if r.ReflogSHA != "" {
		fmt.Fprintf(&buf, "  reflog: %s\n", r.ReflogSHA)
	}
	if r.EvidenceStatus != "" {
		if r.EvidenceFailures != "" {
			fmt.Fprintf(&buf, "  evidence: failed: %s\n", r.EvidenceFailures)
		} else {
			fmt.Fprintf(&buf, "  evidence: ok\n")
		}
	}
	switch r.Verdict {
	case LostToReflog:
		fmt.Fprintf(&buf, "verdict: %s: %s\n", r.Verdict, r.ReflogSHA)
	case BogusEvidence:
		fmt.Fprintf(&buf, "verdict: %s: %s\n", r.Verdict, r.EvidenceFailures)
	default:
		fmt.Fprintf(&buf, "verdict: %s\n", r.Verdict)
	}
	return buf.String()
}

func orNone(s string) string {
	if s == "" {
		return "none"
	}
	return s
}

func orDefault(s, def string) string {
	if s == "" {
		return def
	}
	return s
}
