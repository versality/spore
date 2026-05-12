package rowerwatch

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
)

// LoadStateFile reads an NDJSON snapshot. Missing file is empty, not
// an error. Per-line parse errors drop the offending row silently:
// the recovery shape is "every active rower flashes APPEARED once on
// the next turn", same as the bash watcher when the file is
// truncated.
func LoadStateFile(path string) ([]Snapshot, error) {
	f, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 0, 64*1024), 1*1024*1024)
	var out []Snapshot
	for scanner.Scan() {
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}
		var s Snapshot
		if err := json.Unmarshal(line, &s); err != nil || s.Slug == "" {
			continue
		}
		out = append(out, s)
	}
	if err := scanner.Err(); err != nil {
		return out, err
	}
	return out, nil
}

// SaveStateFile writes the snapshot atomically via tmp + rename. The
// parent dir is created with 0o700 when missing. Each row is one JSON
// object on its own line. NDJSON despite the .ndjson suffix.
func SaveStateFile(path string, snaps []Snapshot) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, filepath.Base(path)+".*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	w := bufio.NewWriter(tmp)
	enc := json.NewEncoder(w)
	enc.SetEscapeHTML(false)
	for _, s := range snaps {
		if err := enc.Encode(s); err != nil {
			tmp.Close()
			os.Remove(tmpName)
			return err
		}
	}
	if err := w.Flush(); err != nil {
		tmp.Close()
		os.Remove(tmpName)
		return err
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmpName)
		return err
	}
	return os.Rename(tmpName, path)
}
