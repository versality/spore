package proactiveloop

import (
	"encoding/json"
	"os"
	"strconv"
	"strings"
	"time"
)

// appendEvent writes one JSONL event to LoopEvents. Best-effort: any
// error swallowed (matches the bash || true).
func appendEvent(cfg Config, status, fingerprint, message string) {
	if cfg.DryRun {
		return
	}
	if _, err := os.Stat(cfg.LoopEvents); os.IsNotExist(err) {
		f, err := os.OpenFile(cfg.LoopEvents, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
		if err == nil {
			f.Close()
		}
	}
	line, err := json.Marshal(map[string]string{
		"ts":          cfg.Now().Format(time.RFC3339),
		"status":      status,
		"fingerprint": fingerprint,
		"message":     message,
	})
	if err != nil {
		return
	}
	f, err := os.OpenFile(cfg.LoopEvents, os.O_WRONLY|os.O_APPEND|os.O_CREATE, 0o600)
	if err != nil {
		return
	}
	defer f.Close()
	f.Write(line)
	f.Write([]byte{'\n'})
}

// readLoopState parses the prior-fingerprint file. Format:
// "<sha256> <unix-seconds>\n". Missing or malformed -> ("", zero).
func readLoopState(path string) (fingerprint string, sent time.Time) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", time.Time{}
	}
	parts := strings.Fields(strings.TrimSpace(string(data)))
	if len(parts) < 2 {
		return "", time.Time{}
	}
	secs, err := strconv.ParseInt(parts[1], 10, 64)
	if err != nil {
		return parts[0], time.Time{}
	}
	return parts[0], time.Unix(secs, 0)
}

func writeLoopState(path, fingerprint string, when time.Time) {
	body := fingerprint + " " + strconv.FormatInt(when.Unix(), 10) + "\n"
	os.WriteFile(path, []byte(body), 0o600)
}

func clearLoopState(cfg Config) {
	if cfg.DryRun {
		return
	}
	os.Remove(cfg.LoopState)
}
