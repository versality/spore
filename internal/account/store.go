package account

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

const (
	storeFileMode = 0o600
	storeDirMode  = 0o700

	activeFile  = ".active"
	ledgerFile  = "switches.jsonl"
	rationedExt = ".rationed-until"
)

// atomicWrite writes data to path via mktemp + rename in the same
// directory. The temp file is created with the target mode so the
// rename never widens permissions. On any error before rename the temp
// file is removed; if rename fails the temp file is also removed so a
// repeated call sees a clean directory.
func atomicWrite(path string, data []byte, mode os.FileMode) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, storeDirMode); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, ".store.*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	cleanup := func() { _ = os.Remove(tmpName) }
	if err := tmp.Chmod(mode); err != nil {
		_ = tmp.Close()
		cleanup()
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		cleanup()
		return err
	}
	if err := tmp.Close(); err != nil {
		cleanup()
		return err
	}
	if err := os.Rename(tmpName, path); err != nil {
		cleanup()
		return err
	}
	return nil
}

// listSnapshotIDs returns the sorted list of <id> values in storeDir
// (basename without `.json`). Missing dir yields an empty slice.
func listSnapshotIDs(storeDir string) ([]string, error) {
	entries, err := os.ReadDir(storeDir)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}
	out := []string{}
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		if !strings.HasSuffix(name, ".json") {
			continue
		}
		if strings.HasPrefix(name, ".") {
			continue
		}
		out = append(out, strings.TrimSuffix(name, ".json"))
	}
	sort.Strings(out)
	return out, nil
}

// readActive reads <storeDir>/.active. Empty string when the marker is
// absent (single-account legacy mode or fresh store).
func readActive(storeDir string) (string, error) {
	b, err := os.ReadFile(filepath.Join(storeDir, activeFile))
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return "", nil
		}
		return "", err
	}
	return strings.TrimSpace(string(b)), nil
}

// writeActive writes id (single line, trailing newline) to
// <storeDir>/.active via mktemp + rename.
func writeActive(storeDir, id string) error {
	return atomicWrite(filepath.Join(storeDir, activeFile), []byte(id+"\n"), storeFileMode)
}

type ledgerEntry struct {
	Timestamp time.Time `json:"ts"`
	Driver    string    `json:"driver"`
	From      string    `json:"from"`
	To        string    `json:"to"`
	Reason    string    `json:"reason,omitempty"`
}

// appendLedger appends one JSON line to <storeDir>/switches.jsonl,
// creating it on first write. The file is append-mode 0600.
func appendLedger(storeDir string, e ledgerEntry) error {
	if err := os.MkdirAll(storeDir, storeDirMode); err != nil {
		return err
	}
	b, err := json.Marshal(e)
	if err != nil {
		return err
	}
	b = append(b, '\n')
	f, err := os.OpenFile(filepath.Join(storeDir, ledgerFile), os.O_WRONLY|os.O_CREATE|os.O_APPEND, storeFileMode)
	if err != nil {
		return err
	}
	defer f.Close()
	if _, err := f.Write(b); err != nil {
		return fmt.Errorf("append ledger: %w", err)
	}
	return nil
}

// snapshotPath returns the per-id snapshot path inside storeDir.
func snapshotPath(storeDir, id string) string {
	return filepath.Join(storeDir, id+".json")
}

// rationedUntilPath returns the per-id rationed-until marker path.
// Used by the codex Auto picker (no /usage signal yet upstream).
func rationedUntilPath(storeDir, id string) string {
	return filepath.Join(storeDir, id+rationedExt)
}

// readRationedUntil reads <id>.rationed-until and returns the parsed
// UTC RFC3339 timestamp. Missing marker returns the zero time and a
// nil error - the caller treats that as "not rationed".
func readRationedUntil(storeDir, id string) (time.Time, error) {
	b, err := os.ReadFile(rationedUntilPath(storeDir, id))
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return time.Time{}, nil
		}
		return time.Time{}, err
	}
	t, perr := time.Parse(time.RFC3339, strings.TrimSpace(string(b)))
	if perr != nil {
		return time.Time{}, fmt.Errorf("parse %s: %w", filepath.Base(rationedUntilPath(storeDir, id)), perr)
	}
	return t.UTC(), nil
}
