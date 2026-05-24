package testagent

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

func drainInbox(rec recorder, provider, mode string) {
	inbox := os.Getenv("SPORE_TASK_INBOX")
	if inbox == "" {
		return
	}
	entries, err := os.ReadDir(inbox)
	if err != nil {
		_ = rec.event(Event{Type: "inbox-error", Provider: provider, Mode: mode, Error: err.Error(), Fields: map[string]string{"inbox": inbox}})
		return
	}
	var seen int
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		path := filepath.Join(inbox, entry.Name())
		body, err := os.ReadFile(path)
		if err != nil {
			_ = rec.event(Event{Type: "inbox-error", Provider: provider, Mode: mode, Error: err.Error(), Fields: map[string]string{"path": path}})
			continue
		}
		seen++
		_ = rec.event(Event{
			Type:     "inbox-seen",
			Provider: provider,
			Mode:     mode,
			Fields: map[string]string{
				"path":  path,
				"bytes": string(body),
			},
		})
		if err := os.Rename(path, path+".processed"); err != nil {
			_ = rec.event(Event{Type: "inbox-error", Provider: provider, Mode: mode, Error: err.Error(), Fields: map[string]string{"path": path}})
		}
	}
	if seen > 0 {
		_ = rec.event(Event{Type: "inbox-drained", Provider: provider, Mode: mode, Fields: map[string]string{"count": strconv.Itoa(seen)}})
		_ = rec.event(Event{Type: "wake-processed", Provider: provider, Mode: mode, Fields: map[string]string{"inbox": inbox}})
	}
}
