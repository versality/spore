package hooks

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

type tellEvent struct {
	Ts     string `json:"ts"`
	Source string `json:"source"`
	Body   string `json:"body"`
}

// writeTellEnvelope drops a tell-protocol envelope ({ts, source, body})
// into inbox atomically: marshal, write under inbox/.tmp, rename into
// the top-level inbox. nameSuffix distinguishes the producer in the
// filename so two writers in the same millisecond do not collide.
func writeTellEnvelope(inbox, source, body, nameSuffix string) error {
	if err := ensureInbox(inbox); err != nil {
		return fmt.Errorf("write-tell: ensure inbox: %w", err)
	}
	ev := tellEvent{
		Ts:     time.Now().Format("2006-01-02T15:04:05-07:00"),
		Source: source,
		Body:   body,
	}
	b, err := json.Marshal(ev)
	if err != nil {
		return err
	}
	b = append(b, '\n')
	name := fmt.Sprintf("%d-%d-%s.json", time.Now().UnixMilli(), os.Getpid(), nameSuffix)
	tmp := filepath.Join(inbox, ".tmp", name)
	dst := filepath.Join(inbox, name)
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return fmt.Errorf("write-tell: write tmp: %w", err)
	}
	if err := os.Rename(tmp, dst); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("write-tell: rename: %w", err)
	}
	return nil
}

// ReadTellBody returns the body field of a tell envelope, falling back
// to the legacy `msg` field. ok is false on read/parse failure or when
// neither field is set.
func ReadTellBody(path string) (string, bool) {
	b, err := os.ReadFile(path)
	if err != nil {
		return "", false
	}
	var ev struct {
		Body string `json:"body"`
		Msg  string `json:"msg"`
	}
	if err := json.Unmarshal(bytes.TrimSpace(b), &ev); err != nil {
		return "", false
	}
	if ev.Body != "" {
		return ev.Body, true
	}
	if ev.Msg != "" {
		return ev.Msg, true
	}
	return "", false
}

var planHeading = regexp.MustCompile(`(?im)^##+\s*plan\b`)

// HasPlanSection reports whether body holds a markdown heading at H2 or
// deeper whose title starts with "Plan" (case-insensitive). Headings
// inside fenced code blocks (``` or ~~~) are ignored.
func HasPlanSection(body []byte) bool {
	inFence := false
	sc := bufio.NewScanner(bytes.NewReader(body))
	sc.Buffer(make([]byte, 1024*1024), 1024*1024)
	for sc.Scan() {
		line := sc.Text()
		trim := strings.TrimSpace(line)
		if strings.HasPrefix(trim, "```") || strings.HasPrefix(trim, "~~~") {
			inFence = !inFence
			continue
		}
		if inFence {
			continue
		}
		if planHeading.MatchString(line) {
			return true
		}
	}
	return false
}
