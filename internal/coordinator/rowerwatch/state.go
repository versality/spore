package rowerwatch

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// Entry is one row of the NDJSON state file.
type Entry struct {
	Slug     string `json:"slug"`
	Status   string `json:"status"`
	Branch   string `json:"branch"`
	HeadSHA  string `json:"head_sha"`
	Agent    string `json:"agent"`
	IdleSecs int    `json:"idle_secs"`
	Stuck    bool   `json:"stuck"`
	Flap     int    `json:"flap"`
	LastSeen string `json:"last_seen"`
}

// readState parses one NDJSON entry per line. Malformed lines are
// skipped. Missing files yield an empty map.
func readState(path string) (map[string]Entry, error) {
	f, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return map[string]Entry{}, nil
		}
		return nil, err
	}
	defer f.Close()

	out := make(map[string]Entry)
	scan := bufio.NewScanner(f)
	scan.Buffer(make([]byte, 64*1024), 1024*1024)
	for scan.Scan() {
		line := scan.Bytes()
		if len(line) == 0 {
			continue
		}
		var e Entry
		if err := json.Unmarshal(line, &e); err != nil {
			continue
		}
		if e.Slug == "" {
			continue
		}
		out[e.Slug] = e
	}
	return out, scan.Err()
}

// timeFormat matches `date -Iseconds` (RFC3339, seconds precision,
// numeric offset).
const timeFormat = "2006-01-02T15:04:05-07:00"

// writeState atomically replaces path with one NDJSON line per
// rower. Bash uses tmp + rename; we mirror that.
func writeState(path, dir string, current []*rower, now time.Time) error {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), filepath.Base(path)+".*.tmp")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()

	w := bufio.NewWriter(tmp)
	iso := now.Format(timeFormat)
	for _, r := range current {
		idle := r.idleSecs
		if idle < -1 {
			idle = -1
		}
		row := map[string]interface{}{
			"slug":      r.slug,
			"status":    r.status,
			"branch":    "wt/" + r.baseSlug,
			"head_sha":  r.headSHA,
			"agent":     r.agent,
			"idle_secs": idle,
			"stuck":     r.newStuck,
			"flap":      r.newFlap,
			"last_seen": iso,
		}
		// Emit fields in the same order the bash printf does, so a
		// state file produced here is byte-identical to bash output
		// for diffability.
		w.WriteString(`{"slug":`)
		w.WriteString(jsonString(row["slug"].(string)))
		w.WriteString(`,"status":`)
		w.WriteString(jsonString(row["status"].(string)))
		w.WriteString(`,"branch":`)
		w.WriteString(jsonString(row["branch"].(string)))
		w.WriteString(`,"head_sha":`)
		w.WriteString(jsonString(row["head_sha"].(string)))
		w.WriteString(`,"agent":`)
		w.WriteString(jsonString(row["agent"].(string)))
		w.WriteString(`,"idle_secs":`)
		w.WriteString(strconv.Itoa(idle))
		w.WriteString(`,"stuck":`)
		if r.newStuck {
			w.WriteString("true")
		} else {
			w.WriteString("false")
		}
		w.WriteString(`,"flap":`)
		w.WriteString(strconv.Itoa(r.newFlap))
		w.WriteString(`,"last_seen":`)
		w.WriteString(jsonString(iso))
		w.WriteString("}\n")
	}
	if err := w.Flush(); err != nil {
		tmp.Close()
		os.Remove(tmpPath)
		return err
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmpPath)
		return err
	}
	return os.Rename(tmpPath, path)
}

// jsonString returns a JSON-encoded scalar string. Falls back to
// json.Marshal for the rare control-char case.
func jsonString(s string) string {
	if !strings.ContainsAny(s, "\\\"\n\r\t\x00") {
		return `"` + s + `"`
	}
	b, _ := json.Marshal(s)
	return string(b)
}
