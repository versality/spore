package workerwatch

import (
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// ClaudeIdleSecs returns the seconds since the most recent
// claude-code transcript jsonl in $HOME/.claude/projects/<encoded>
// was modified. Encoding maps both `/` and `.` in wtDir to `-`,
// matching claude-code's projects-dir scheme. ok=false on any
// missing-dir / missing-jsonl / stat failure; idle stays unobserved.
func ClaudeIdleSecs(homeDir, wtDir string, now time.Time) (int, bool) {
	if homeDir == "" || wtDir == "" {
		return 0, false
	}
	encoded := strings.ReplaceAll(wtDir, "/", "-")
	encoded = strings.ReplaceAll(encoded, ".", "-")
	projDir := filepath.Join(homeDir, ".claude", "projects", encoded)
	entries, err := os.ReadDir(projDir)
	if err != nil {
		return 0, false
	}
	var latest time.Time
	found := false
	for _, ent := range entries {
		if ent.IsDir() || !strings.HasSuffix(ent.Name(), ".jsonl") {
			continue
		}
		info, err := ent.Info()
		if err != nil || !info.Mode().IsRegular() {
			continue
		}
		if !found || info.ModTime().After(latest) {
			latest = info.ModTime()
			found = true
		}
	}
	if !found {
		return 0, false
	}
	d := now.Sub(latest)
	if d < 0 {
		d = 0
	}
	return int(d / time.Second), true
}

// OpencodeIdleSecs shells out to `opencode db --format tsv` and
// returns the seconds since the most recent assistant message in any
// session whose `directory` matches wtDir. ok=false when the binary
// is missing, the query fails, or the row is unparseable. A zero
// asst_ms (no assistant messages ever) maps to "idle = forever"
// rather than ok=false so the worker can still be flagged STUCK; this
// matches the bash watcher's `((asst_ms == 0)) -> printf now_s`
// branch which yields idle=now_s.
func OpencodeIdleSecs(bin, wtDir string, now time.Time) (int, bool) {
	if bin == "" {
		bin = "opencode"
	}
	if _, err := exec.LookPath(bin); err != nil {
		return 0, false
	}
	dirLit := "'" + strings.ReplaceAll(wtDir, "'", "''") + "'"
	q := "SELECT COALESCE(MAX(m.time_updated), 0) FROM message m JOIN session s ON s.id = m.session_id WHERE s.directory = " + dirLit + " AND json_extract(m.data, '$.role') = 'assistant'"
	out, err := exec.Command(bin, "db", "--format", "tsv", q).Output()
	if err != nil {
		return 0, false
	}
	lines := strings.Split(strings.TrimRight(string(out), "\n"), "\n")
	if len(lines) < 2 {
		return 0, false
	}
	row := strings.TrimSpace(lines[1])
	asstMs, err := strconv.ParseInt(row, 10, 64)
	if err != nil {
		return 0, false
	}
	nowS := now.Unix()
	if asstMs == 0 {
		return int(nowS), true
	}
	idle := nowS - asstMs/1000
	if idle < 0 {
		idle = 0
	}
	return int(idle), true
}

// HeadShortSHA returns the short-7 hash of `wt/<base_slug>` in the
// project repo, or "" on any failure (worktree absent, branch
// missing, git error). Used to populate the snapshot.head_sha field.
func HeadShortSHA(projectRoot, baseSlug string) string {
	if projectRoot == "" || baseSlug == "" {
		return ""
	}
	if _, err := os.Stat(filepath.Join(projectRoot, ".worktrees", baseSlug)); err != nil {
		return ""
	}
	out, err := exec.Command("git", "-C", projectRoot, "rev-parse", "--short=7", "wt/"+baseSlug).Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}
