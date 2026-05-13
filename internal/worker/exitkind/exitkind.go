// Package exitkind classifies a rower wrapper's exit shape into one of
// four kinds so the operator-visible signal (agent.log exit line +
// skyhelm tell) names the cause directly instead of leaving operators
// to re-derive it from rc.
//
// The four kinds collapse three rc-shapes that the wrapper layer used
// to lump together as "rc=129":
//
//   - lifecycle: the rower ran its clean-exit step (e.g. tmux
//     kill-session) which SIGHUP'd the wrapper. Detected by the
//     clean-exit marker the rower writes BEFORE the kill. This is the
//     normal exit shape, not a crash.
//   - sighup-external: rc=129 with NO clean-exit marker. Something
//     other than the rower tore down the pty (external kill-session,
//     pane teardown by a sibling script, tmux server signal).
//   - crash-rc<n>: any non-zero rc that isn't 129 (the agent itself
//     bailed: agent crash, OOM, exec failure). The rc suffix is grep
//     signal; the operator should be able to `grep kind=crash-rc137`
//     to find OOMs without re-deriving from a separate rc field.
//   - early-exit: rc=0 with no marker (budget-block, helm-tell preamble
//     paths, etc.).
package exitkind

import (
	"fmt"
	"os"
)

// Classify returns the kind string for a wrapper exit. markerPath is
// the path the rower writes before tearing down the pty; empty or
// missing means no lifecycle marker. The classifier never reads the
// marker contents; presence is the signal.
func Classify(rc int, markerPath string) string {
	if markerPath != "" {
		if _, err := os.Stat(markerPath); err == nil {
			return "lifecycle"
		}
	}
	switch {
	case rc == 0:
		return "early-exit"
	case rc == 129:
		return "sighup-external"
	default:
		return fmt.Sprintf("crash-rc%d", rc)
	}
}
