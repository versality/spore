package boot

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
)

// StateInfo carries the state.md inspector's output. Body is the
// verbatim file contents (no truncation); it is inlined into the
// summary between the size header and the first probe section. When
// the file is missing, Exists is false and Body is nil.
type StateInfo struct {
	Path      string
	Exists    bool
	Lines     int
	Bytes     int
	FirstLine string
	Oversized []string
	Body      []byte
	RC        int
}

func inspectState(cfg Config) StateInfo {
	path := filepath.Join(cfg.StateDir, "state.md")
	info := StateInfo{Path: path}

	body, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return info
		}
		info.RC = 1
		info.FirstLine = fmt.Sprintf("read error: %v", err)
		return info
	}
	info.Exists = true
	info.Body = body
	info.Bytes = len(body)
	info.Lines = countLines(body)
	info.FirstLine = firstLine(body)

	if info.Lines > cfg.LineCap {
		info.Oversized = append(info.Oversized,
			fmt.Sprintf("lines=%d>cap=%d", info.Lines, cfg.LineCap))
	}
	if info.Bytes > cfg.ByteCap {
		info.Oversized = append(info.Oversized,
			fmt.Sprintf("bytes=%d>cap=%d", info.Bytes, cfg.ByteCap))
	}
	if len(info.Oversized) > 0 {
		info.RC = 2
	}
	return info
}

// countLines matches `wc -l`: number of newline-terminated lines. A
// file with no trailing newline counts the final line if it carries
// any bytes (matches the bash wc behaviour we replace).
func countLines(b []byte) int {
	n := bytes.Count(b, []byte{'\n'})
	if len(b) > 0 && b[len(b)-1] != '\n' {
		n++
	}
	return n
}

func firstLine(b []byte) string {
	if i := bytes.IndexByte(b, '\n'); i >= 0 {
		return string(b[:i])
	}
	return string(b)
}
