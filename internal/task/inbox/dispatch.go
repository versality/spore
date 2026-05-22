// Package inbox provides reusable inbox-drain primitives for spore
// coordinators and helpers. Dispatch lifts the
// "drain envelopes matching a token, call a handler, move to
// inbox/read/" pattern out of consumer harness scripts so
// coordinators stop reimplementing it.
package inbox

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

// DispatchOptions configures a single Dispatch pass.
type DispatchOptions struct {
	// Dir is the inbox directory. *.json at its top level are
	// candidates; subdirectories (including read/) are ignored.
	Dir string
	// Token is the regex matched against the envelope body. Required.
	Token *regexp.Regexp
	// Handler is the executable invoked per matched envelope. It
	// receives the envelope path as its sole positional argument.
	// Handler exit 0 means "consume" (move to read/); any non-zero
	// exit leaves the envelope in place for the next tick to retry.
	Handler string
	// HandlerEnv is appended to the handler's environment.
	HandlerEnv []string
	// Log, if non-nil, receives one line per envelope action.
	Log io.Writer
}

// DispatchResult reports per-pass counters.
type DispatchResult struct {
	Scanned int
	Matched int
	Handled int
	Failed  int
}

// Dispatch drains envelopes from opts.Dir whose body matches opts.Token,
// invokes opts.Handler with the envelope path, and moves each
// successfully handled envelope into opts.Dir/read/. Returns counters
// and the first envelope-level error encountered; a handler that exits
// non-zero is recorded in Failed but does not abort the pass.
func Dispatch(opts DispatchOptions) (DispatchResult, error) {
	var res DispatchResult
	if opts.Token == nil {
		return res, fmt.Errorf("inbox dispatch: token regex required")
	}
	if opts.Handler == "" {
		return res, fmt.Errorf("inbox dispatch: handler required")
	}
	if opts.Dir == "" {
		return res, fmt.Errorf("inbox dispatch: dir required")
	}

	entries, err := os.ReadDir(opts.Dir)
	if err != nil {
		if os.IsNotExist(err) {
			return res, nil
		}
		return res, err
	}
	readDir := filepath.Join(opts.Dir, "read")
	if err := os.MkdirAll(readDir, 0o755); err != nil {
		return res, err
	}

	var names []string
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		info, err := e.Info()
		if err != nil || !info.Mode().IsRegular() {
			continue
		}
		names = append(names, e.Name())
	}
	sort.Strings(names)

	for _, name := range names {
		res.Scanned++
		path := filepath.Join(opts.Dir, name)
		body, err := extractBody(path)
		if err != nil {
			logf(opts.Log, "%s: read failed: %v", name, err)
			continue
		}
		if !opts.Token.MatchString(body) {
			continue
		}
		res.Matched++
		cmd := exec.Command(opts.Handler, path)
		cmd.Env = append(os.Environ(), opts.HandlerEnv...)
		cmd.Stdout = opts.Log
		cmd.Stderr = opts.Log
		if err := cmd.Run(); err != nil {
			res.Failed++
			logf(opts.Log, "%s: handler failed: %v (leaving for retry)", name, err)
			continue
		}
		dst := filepath.Join(readDir, name)
		if err := os.Rename(path, dst); err != nil {
			res.Failed++
			logf(opts.Log, "%s: handler ok but mv to read/ failed: %v", name, err)
			continue
		}
		res.Handled++
		logf(opts.Log, "%s: handled", name)
	}
	return res, nil
}

// extractBody returns the text the regex is matched against. The
// envelope schema is convention-driven: spore tells write {"msg":...},
// older harness writers (e.g. pre-spore tell scripts) write
// {"body":...}. We pull
// the first non-empty of msg/body; if neither parses or is set, we
// fall back to the raw file content so a regex over the JSON itself
// still works (this lets callers match against arbitrary fields
// without us adding a flag per schema).
func extractBody(path string) (string, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	var ev struct {
		Msg  string `json:"msg"`
		Body string `json:"body"`
	}
	if err := json.Unmarshal(b, &ev); err == nil {
		if ev.Msg != "" {
			return ev.Msg, nil
		}
		if ev.Body != "" {
			return ev.Body, nil
		}
	}
	return string(b), nil
}

func logf(w io.Writer, format string, args ...any) {
	if w == nil {
		return
	}
	fmt.Fprintf(w, "inbox-dispatch: "+format+"\n", args...)
}
