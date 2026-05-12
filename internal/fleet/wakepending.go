package fleet

import (
	"os"
	"path/filepath"
	"strconv"
	"time"

	"github.com/versality/spore/internal/task"
)

// defaultWakePendingTTL is the window after which a wake-pending marker
// is treated as stuck and the slug surfaces as idle-wake-stuck.
const defaultWakePendingTTL = 5 * time.Minute

type wakePendingStatus struct {
	exists bool
	fresh  bool
	age    time.Duration
}

// wakePendingPath returns "<state>/<slug>/wake-pending" under spore's
// per-project state dir. projectRoot may be "" to use cwd's project.
func wakePendingPath(projectRoot, slug string) (string, error) {
	state, err := task.StateDirForProject(projectRoot)
	if err != nil {
		return "", err
	}
	return filepath.Join(state, slug, "wake-pending"), nil
}

func markWakePending(projectRoot, slug string) {
	path, err := wakePendingPath(projectRoot, slug)
	if err != nil {
		return
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return
	}
	_ = os.WriteFile(path, []byte(time.Now().Format(time.RFC3339)+"\n"), 0o600)
}

func clearWakePending(projectRoot, slug string) {
	path, err := wakePendingPath(projectRoot, slug)
	if err != nil {
		return
	}
	_ = os.Remove(path)
}

func wakePendingState(projectRoot, slug string, now time.Time) wakePendingStatus {
	path, err := wakePendingPath(projectRoot, slug)
	if err != nil {
		return wakePendingStatus{}
	}
	info, err := os.Stat(path)
	if err != nil {
		return wakePendingStatus{}
	}
	age := now.Sub(info.ModTime())
	if age < 0 {
		age = 0
	}
	ttl := wakePendingTTL()
	if ttl <= 0 {
		return wakePendingStatus{exists: true, fresh: true, age: age}
	}
	return wakePendingStatus{exists: true, fresh: age < ttl, age: age}
}

func wakePendingTTL() time.Duration {
	raw := os.Getenv("WT_ROWER_WAKE_PENDING_TTL")
	if raw == "" {
		return defaultWakePendingTTL
	}
	secs, err := strconv.Atoi(raw)
	if err != nil {
		return defaultWakePendingTTL
	}
	return time.Duration(secs) * time.Second
}
