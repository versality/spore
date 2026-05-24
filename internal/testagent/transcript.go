package testagent

import (
	"encoding/json"
	"os"
	"strconv"
)

func writeTranscript(rec recorder, provider, mode string) {
	path := os.Getenv(EnvTranscript)
	if path == "" {
		return
	}
	total := tokenTotal()
	var event map[string]any
	switch provider {
	case "codex":
		event = map[string]any{
			"type":     "turn_context",
			"provider": "codex",
			"usage": map[string]any{
				"input_tokens":  total / 2,
				"output_tokens": total - total/2,
				"total_tokens":  total,
			},
		}
	case "claude":
		event = map[string]any{
			"type":     "assistant",
			"provider": "claude",
			"message": map[string]any{
				"usage": map[string]any{
					"input_tokens":  total / 2,
					"output_tokens": total - total/2,
				},
			},
		}
	default:
		event = map[string]any{
			"type":         "fake",
			"provider":     provider,
			"total_tokens": total,
		}
	}
	body, err := json.Marshal(event)
	if err != nil {
		_ = rec.event(Event{Type: "transcript-error", Provider: provider, Mode: mode, Error: err.Error()})
		return
	}
	if err := os.WriteFile(path, append(body, '\n'), 0o644); err != nil {
		_ = rec.event(Event{Type: "transcript-error", Provider: provider, Mode: mode, Error: err.Error(), Fields: map[string]string{"path": path}})
		return
	}
	_ = rec.event(Event{Type: "transcript", Provider: provider, Mode: mode, Fields: map[string]string{"path": path, "total_tokens": strconv.Itoa(total)}})
}

func tokenTotal() int {
	raw := os.Getenv(EnvTokenTotal)
	if raw == "" {
		return 1000
	}
	n, err := strconv.Atoi(raw)
	if err != nil || n < 0 {
		return 1000
	}
	return n
}
