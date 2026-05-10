package proactiveloop

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// writeInbox drops a JSON envelope into the first project's inbox
// directory, atomically (write to .tmp/, rename in). DryRun prints the
// body to stdout instead.
func writeInbox(cfg Config, projects []string, body string) error {
	project := firstProject(projects)
	if project == "" {
		return fmt.Errorf("no project to address")
	}
	if cfg.DryRun {
		// Bash's dry-run prints the body to stdout via the caller; we
		// preserve that contract by leaving it to the caller, so all
		// we do here is no-op. (See Tick: no path calls writeInbox in
		// DryRun mode where we want a real write.)
		return nil
	}
	name := projectName(project)
	inbox := filepath.Join(cfg.StateDir, name, "inbox")
	if err := os.MkdirAll(filepath.Join(inbox, ".tmp"), 0o700); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Join(inbox, "read"), 0o700); err != nil {
		return err
	}
	stamp := cfg.Random()
	fname := stamp + ".json"
	tmp := filepath.Join(inbox, ".tmp", fname)

	envelope := map[string]string{
		"ts":     cfg.Now().Format(time.RFC3339),
		"source": "skyhelm-proactive-loop",
		"body":   body,
	}
	data, err := json.Marshal(envelope)
	if err != nil {
		return err
	}
	data = append(data, '\n')
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, filepath.Join(inbox, fname))
}

// countUnreadInboxes counts every *.json file across all configured
// project inboxes whose JSON does not carry our own `source` marker.
// Returns the count and the basename of the lexicographically newest
// file (prefixed by the project name when multiple projects exist).
func countUnreadInboxes(cfg Config, projects []string) (int, string) {
	if len(projects) == 0 {
		return 0, ""
	}
	count := 0
	var newestFile, newestProject string
	for _, project := range projects {
		if project == "" {
			continue
		}
		name := projectName(project)
		inbox := filepath.Join(cfg.StateDir, name, "inbox")
		st, err := os.Stat(inbox)
		if err != nil || !st.IsDir() {
			continue
		}
		entries, err := os.ReadDir(inbox)
		if err != nil {
			continue
		}
		var files []string
		for _, e := range entries {
			n := e.Name()
			if !strings.HasSuffix(n, ".json") || e.IsDir() {
				continue
			}
			files = append(files, n)
		}
		sort.Strings(files)
		for _, base := range files {
			full := filepath.Join(inbox, base)
			body, err := os.ReadFile(full)
			if err != nil {
				continue
			}
			if strings.Contains(string(body), `"source":"skyhelm-proactive-loop"`) {
				continue
			}
			count++
			if newestFile == "" || base > newestFile {
				newestFile = base
				newestProject = name
			}
		}
	}
	if newestFile != "" && len(projects) > 1 {
		return count, newestProject + "/" + newestFile
	}
	return count, newestFile
}
