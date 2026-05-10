package rowerwatch

import (
	"bufio"
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/versality/spore/internal/coordinator/verify"
)

// Probe is the abstraction layer over the live system: git, opencode
// SQLite, claude-code session logs, fleet status, verify-done.
// Tests inject fakes; production wires DefaultProbe.
type Probe interface {
	GitMainRoot() string
	GitHeadSHA(projectRoot, branch string) string
	OpencodeIdleSecs(wtDir string, now time.Time) (int, bool)
	ClaudeIdleSecs(wtDir string, now time.Time) (int, bool)
	FleetStatus() string
	DoneVerdict(slug string) string
}

// DefaultProbe shells out to git/opencode/wt-task and reads claude
// session jsonl files directly.
type DefaultProbe struct{}

func (DefaultProbe) GitMainRoot() string {
	out, err := exec.Command("git", "rev-parse", "--git-common-dir").Output()
	if err != nil {
		return ""
	}
	common := strings.TrimSpace(string(out))
	if common == "" {
		return ""
	}
	if !filepath.IsAbs(common) {
		cwd, err := os.Getwd()
		if err == nil {
			common = filepath.Join(cwd, common)
		}
	}
	parent := filepath.Dir(common)
	abs, err := filepath.Abs(parent)
	if err != nil {
		return parent
	}
	return abs
}

func (DefaultProbe) GitHeadSHA(projectRoot, branch string) string {
	cmd := exec.Command("git", "-C", projectRoot, "rev-parse", "--short=7", branch)
	out, err := cmd.Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

func (DefaultProbe) OpencodeIdleSecs(wtDir string, now time.Time) (int, bool) {
	if _, err := exec.LookPath("opencode"); err != nil {
		return 0, false
	}
	esc := strings.ReplaceAll(wtDir, "'", "''")
	sql := "SELECT COALESCE(MAX(m.time_updated), 0) FROM message m " +
		"JOIN session s ON s.id = m.session_id " +
		"WHERE s.directory = '" + esc + "' " +
		"AND json_extract(m.data, '$.role') = 'assistant'"
	out, err := exec.Command("opencode", "db", "--format", "tsv", sql).Output()
	if err != nil {
		return 0, false
	}
	// `opencode db --format tsv` prints a header row, then the
	// data row. Take line 2.
	lines := strings.Split(string(out), "\n")
	if len(lines) < 2 {
		return 0, false
	}
	val := strings.TrimSpace(lines[1])
	if val == "" {
		return 0, false
	}
	asstMS, err := strconv.ParseInt(val, 10, 64)
	if err != nil {
		return 0, false
	}
	if asstMS == 0 {
		return int(now.Unix()), true
	}
	return int(now.Unix() - asstMS/1000), true
}

func (DefaultProbe) ClaudeIdleSecs(wtDir string, now time.Time) (int, bool) {
	home, _ := os.UserHomeDir()
	if home == "" {
		return 0, false
	}
	encoded := encodeClaudeProjectDir(wtDir)
	projDir := filepath.Join(home, ".claude", "projects", encoded)
	st, err := os.Stat(projDir)
	if err != nil || !st.IsDir() {
		return 0, false
	}
	entries, err := os.ReadDir(projDir)
	if err != nil {
		return 0, false
	}

	type fileMod struct {
		path  string
		mtime time.Time
	}
	var files []fileMod
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		if !strings.HasSuffix(e.Name(), ".jsonl") {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		files = append(files, fileMod{
			path:  filepath.Join(projDir, e.Name()),
			mtime: info.ModTime(),
		})
	}
	if len(files) == 0 {
		return 0, false
	}
	sort.Slice(files, func(i, j int) bool {
		return files[i].mtime.After(files[j].mtime)
	})
	latest := files[0]
	idle := int(now.Unix() - latest.mtime.Unix())

	if errSecs, ok := lastAPIErrorIdle(latest.path, now); ok {
		if errSecs < idle {
			idle = errSecs
		}
	}
	return idle, true
}

// encodeClaudeProjectDir mirrors claude-code's projects-dir naming:
// "/" and "." both collapse to "-".
func encodeClaudeProjectDir(p string) string {
	out := strings.ReplaceAll(p, "/", "-")
	return strings.ReplaceAll(out, ".", "-")
}

// lastAPIErrorIdle scans the jsonl for `"subtype":"api_error"`
// records and returns now - last err timestamp (clamped to >= 0).
func lastAPIErrorIdle(path string, now time.Time) (int, bool) {
	f, err := os.Open(path)
	if err != nil {
		return 0, false
	}
	defer f.Close()

	var lastTS string
	scan := bufio.NewScanner(f)
	scan.Buffer(make([]byte, 64*1024), 1024*1024)
	needle := []byte(`"subtype":"api_error"`)
	for scan.Scan() {
		line := scan.Bytes()
		if !bytes.Contains(line, needle) {
			continue
		}
		ts := extractTimestamp(line)
		if ts != "" {
			lastTS = ts
		}
	}
	if lastTS == "" {
		return 0, false
	}
	t, err := time.Parse(time.RFC3339Nano, lastTS)
	if err != nil {
		t, err = time.Parse(time.RFC3339, lastTS)
		if err != nil {
			return 0, false
		}
	}
	idle := int(now.Unix() - t.Unix())
	if idle < 0 {
		idle = 0
	}
	return idle, true
}

// extractTimestamp pulls the value of "timestamp":"..." from a jsonl
// line without parsing the full record. Mirrors the bash awk substr.
func extractTimestamp(line []byte) string {
	const prefix = `"timestamp":"`
	i := bytes.Index(line, []byte(prefix))
	if i < 0 {
		return ""
	}
	rest := line[i+len(prefix):]
	j := bytes.IndexByte(rest, '"')
	if j < 0 {
		return ""
	}
	return string(rest[:j])
}

func (DefaultProbe) FleetStatus() string {
	out, err := exec.Command("wt-task", "fleet", "status").Output()
	if err != nil {
		return ""
	}
	return string(out)
}

func (DefaultProbe) DoneVerdict(slug string) string {
	cwd, err := os.Getwd()
	if err != nil {
		return ""
	}
	res := verify.Verify(slug, verify.Config{ProjectRoot: cwd})
	out := res.Format()
	for _, line := range strings.Split(out, "\n") {
		if strings.HasPrefix(line, "verdict: ") {
			rest := strings.TrimPrefix(line, "verdict: ")
			if i := strings.Index(rest, ": "); i >= 0 {
				return rest[:i]
			}
			return rest
		}
	}
	return ""
}
