package task

import (
	"encoding/json"
	"fmt"
	"os"
)

// WaybarChip is the JSON payload waybar's custom module expects.
type WaybarChip struct {
	Text    string `json:"text"`
	Class   string `json:"class"`
	Tooltip string `json:"tooltip"`
}

// Waybar scans tasksDir and returns a JSON chip for waybar's custom
// module. Counts tasks by status, filters to host-local tasks (host
// matches hostname or is empty), and renders d/a/b counts. Legacy
// on-disk values (backlog, paused, parked) are folded via
// CanonicalStatus before counting.
func Waybar(tasksDir string) ([]byte, error) {
	hostname, err := os.Hostname()
	if err != nil {
		hostname = ""
	}
	metas, err := List(tasksDir)
	if err != nil {
		return nil, err
	}

	var draft, active, blocked int
	for _, m := range metas {
		if m.Host != "" && m.Host != hostname {
			continue
		}
		switch CanonicalStatus(m.Status) {
		case StatusDraft:
			draft++
		case StatusActive:
			active++
		case StatusBlocked:
			blocked++
		}
	}

	class := "idle"
	switch {
	case blocked > 0:
		class = "blocked"
	case active > 0:
		class = "active"
	}

	chip := WaybarChip{
		Text:    fmt.Sprintf("%d/%d/%d", draft, active, blocked),
		Class:   class,
		Tooltip: fmt.Sprintf("draft:%d active:%d blocked:%d", draft, active, blocked),
	}
	return json.Marshal(chip)
}
