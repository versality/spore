package task

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// Tell appends a JSON message envelope to the per-slug inbox dir under
// StateDir. Filename is "<unix-millis>.json"; payload schema is
// {"slug","ts","msg"}. No agent wake side effect: the Stop-hook drain
// that turns inbox writes into resumed turns is downstream consumer
// territory.
func Tell(slug, msg string) error {
	dir, err := InboxDir(slug)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	now := time.Now()
	path := filepath.Join(dir, fmt.Sprintf("%d.json", now.UnixMilli()))
	payload := struct {
		Slug string `json:"slug"`
		TS   string `json:"ts"`
		Msg  string `json:"msg"`
	}{Slug: slug, TS: now.UTC().Format(time.RFC3339), Msg: msg}
	b, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	return os.WriteFile(path, b, 0o644)
}
