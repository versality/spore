package verify

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

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
