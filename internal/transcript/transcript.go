// Package transcript reads claude-code session JSONL transcripts to
// extract token-usage signals. Token monitors (coordinator and worker)
// share these helpers.
package transcript

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

// SumContextTokens reads the JSONL transcript at path and returns the
// sum of input_tokens + cache_creation_input_tokens +
// cache_read_input_tokens from the last assistant message's usage
// block. Returns 0 on any error or when no usage is found.
func SumContextTokens(path string) int {
	f, err := os.Open(path)
	if err != nil {
		return 0
	}
	defer f.Close()

	var lastLine string
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 0, 64*1024), 16*1024*1024)
	for scanner.Scan() {
		line := scanner.Text()
		if strings.Contains(line, `"role":"assistant"`) {
			lastLine = line
		}
	}
	if lastLine == "" {
		return 0
	}
	return ExtractContextFromLine(lastLine)
}

// ExtractContextFromLine pulls the last "usage" block from a single
// JSONL line and sums the three context-token counters.
func ExtractContextFromLine(line string) int {
	usageRE := regexp.MustCompile(`"usage"\s*:\s*\{`)
	locs := usageRE.FindAllStringIndex(line, -1)
	if len(locs) == 0 {
		return 0
	}
	lastLoc := locs[len(locs)-1]
	start := lastLoc[1] - 1

	depth := 0
	end := start
	for i := start; i < len(line); i++ {
		switch line[i] {
		case '{':
			depth++
		case '}':
			depth--
			if depth == 0 {
				end = i + 1
				goto done
			}
		}
	}
done:
	if end <= start {
		return 0
	}

	block := line[start:end]

	var usage map[string]json.RawMessage
	if json.Unmarshal([]byte(block), &usage) != nil {
		return 0
	}

	sum := 0
	for _, key := range []string{"input_tokens", "cache_creation_input_tokens", "cache_read_input_tokens"} {
		if raw, ok := usage[key]; ok {
			var n int
			if json.Unmarshal(raw, &n) == nil {
				sum += n
			}
		}
	}
	return sum
}

// FindFallbackTranscript locates the newest *.jsonl under
// ~/.claude/projects/<encoded-cwd>/ when the hook payload omits
// transcript_path. Returns "" when nothing is found.
func FindFallbackTranscript() string {
	cwd := os.Getenv("CLAUDE_PROJECT_DIR")
	if cwd == "" {
		cwd, _ = os.Getwd()
	}
	encoded := strings.ReplaceAll(cwd, "/", "-")
	home, _ := os.UserHomeDir()
	projDir := filepath.Join(home, ".claude", "projects", encoded)

	entries, err := os.ReadDir(projDir)
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
			newest = filepath.Join(projDir, e.Name())
			newestTime = fi.ModTime()
		}
	}
	return newest
}
