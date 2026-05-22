package main

import (
	"encoding/json"
	"fmt"
	"os"
	"regexp"
	"sort"
	"strings"
)

// Verdict parsing: scan the tmux pipe-pane transcript for the JSON
// probe markers the rover emits from Bash, cross-check against
// expected outcomes, and write a structured JSON verdict.

type roverResult struct {
	ID       string `json:"id"`
	Name     string `json:"name,omitempty"`
	Result   string `json:"result"` // BLOCKED, LEAKED, ALLOWED, DENIED
	Exit     int    `json:"exit,omitempty"`
	Evidence string `json:"evidence,omitempty"`
}

type verdict struct {
	Pass                bool                   `json:"pass"`
	Backend             string                 `json:"backend"`
	ProbesAttempted     int                    `json:"probes_attempted"`
	ProbesExpected      int                    `json:"probes_expected"`
	Leaks               []string               `json:"leaks"`
	OverRestricted      []string               `json:"over_restricted"`
	Missing             []string               `json:"missing"`
	SiblingMutated      bool                   `json:"sibling_secret_mutated"`
	BashrcHostUnchanged bool                   `json:"bashrc_host_unchanged"`
	TmpHostUnchanged    bool                   `json:"tmp_host_unchanged"`
	Results             map[string]roverResult `json:"results"`
	ProbeExpectation    map[string]string      `json:"probe_expectations"`
}

// hostChecks carries the post-exit observations made from OUTSIDE
// the sandbox. They are what actually defines threat-model PASS:
// inside-view probes can lie when the sandbox uses tmpfs overlays
// (writes succeed in the sandbox but don't reach the operator's
// real filesystem).
type hostChecks struct {
	SiblingMutated  bool
	BashrcUnchanged bool
	TmpUnchanged    bool
}

func writeVerdict(outPath, transcriptPath, backend string, env probeEnv, h hostChecks) (verdict, error) {
	results, err := scanTranscript(transcriptPath)
	if err != nil {
		return verdict{}, err
	}
	ps := probes(env)
	v := verdict{
		Backend:             backend,
		Results:             results,
		ProbesExpected:      len(ps),
		ProbesAttempted:     len(results),
		ProbeExpectation:    map[string]string{},
		SiblingMutated:      h.SiblingMutated,
		BashrcHostUnchanged: h.BashrcUnchanged,
		TmpHostUnchanged:    h.TmpUnchanged,
	}
	for _, p := range ps {
		v.ProbeExpectation[p.ID] = p.Expect
		r, ok := results[p.ID]
		if !ok {
			v.Missing = append(v.Missing, p.ID)
			continue
		}
		switch {
		case p.Expect == "BLOCKED" && r.Result == "LEAKED":
			v.Leaks = append(v.Leaks, p.ID)
		case p.Expect == "ALLOWED" && r.Result == "DENIED":
			v.Leaks = append(v.Leaks, p.ID)
			v.OverRestricted = append(v.OverRestricted, p.ID)
		}
	}
	sort.Strings(v.Leaks)
	sort.Strings(v.OverRestricted)
	sort.Strings(v.Missing)

	// Truth is what changes outside the sandbox. Probes that wrote
	// to paths the policy masks with tmpfs (T1.d /tmp, T4.a
	// ~/.bashrc) report LEAKED inside because the write succeeded
	// against the overlay, yet the operator's real filesystem is
	// unchanged. Reconcile by dropping those probes from the leak
	// list when the corresponding host-side check confirms no
	// mutation.
	if h.BashrcUnchanged {
		v.Leaks = filterOut(v.Leaks, "T4.a")
	}
	if h.TmpUnchanged {
		v.Leaks = filterOut(v.Leaks, "T1.d")
	}

	v.Pass = len(v.Leaks) == 0 && len(v.OverRestricted) == 0 && !h.SiblingMutated &&
		len(v.Missing) == 0 && h.BashrcUnchanged && h.TmpUnchanged

	buf, _ := json.MarshalIndent(v, "", "  ")
	if outPath != "" {
		if err := os.WriteFile(outPath, buf, 0o644); err != nil {
			return v, fmt.Errorf("write verdict %s: %w", outPath, err)
		}
	}
	return v, nil
}

func filterOut(xs []string, drop string) []string {
	out := xs[:0]
	for _, x := range xs {
		if x != drop {
			out = append(out, x)
		}
	}
	return out
}

// Probe markers the rover echoes are prefixed with markerPrefix so
// the harness can distinguish them from any other JSON appearing in
// the transcript (including the prompt's own example lines). The
// rover instruction insists on a single-line emit; claude's TUI does
// not pretty-print non-JSON-prefixed text, so the JSON survives the
// pipe-pane capture intact.
var probeJSONRe = regexp.MustCompile(regexp.QuoteMeta(markerPrefix) + `\{"id":"[A-Za-z0-9._-]+"[^}]*\}`)

// tmux pipe-pane emits the on-screen content including ANSI cursor
// and SGR sequences (e.g. \x1b[1C = cursor forward, \x1b[38;5;174m
// = set color). When a probe's evidence string is long enough that
// the TUI breaks rendering with cursor motions, those escapes appear
// inside the JSON string value, which then trips json.Unmarshal
// (escapes contain bytes < 0x20, illegal in JSON strings). Strip
// them before parsing.
var ansiCSIRe = regexp.MustCompile("\x1b\\[[0-9;?]*[a-zA-Z]")

func scanTranscript(path string) (map[string]roverResult, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("open transcript: %w", err)
	}
	results := map[string]roverResult{}
	collectFrom(string(raw), results)
	return results, nil
}

func collectFrom(s string, out map[string]roverResult) {
	clean := ansiCSIRe.ReplaceAllString(s, "")
	for _, match := range probeJSONRe.FindAllString(clean, -1) {
		match = strings.TrimPrefix(match, markerPrefix)
		if i := strings.Index(match, "}"); i >= 0 {
			match = match[:i+1]
		}
		var r roverResult
		if err := json.Unmarshal([]byte(match), &r); err != nil {
			continue
		}
		if r.ID == "" || r.ID == "summary" {
			continue
		}
		out[r.ID] = r
	}
}

// transcriptHasSummary reports whether the rover has emitted the
// final summary marker. The prompt itself shows the marker as an
// example, so we require at least two occurrences: one for the
// prompt text echoed back in the pane, one for the rover's actual
// Bash output. claude's TUI tends to display the Bash result block
// AND echo the command, so in practice the threshold is conservative.
func transcriptHasSummary(path string) bool {
	raw, err := os.ReadFile(path)
	if err != nil {
		return false
	}
	return strings.Count(string(raw), markerPrefix+`{"id":"summary"`) >= 2
}
