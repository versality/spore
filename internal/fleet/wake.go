package fleet

import (
	"fmt"
	"io"
	"path/filepath"
	"strings"

	"github.com/versality/spore/internal/task"
)

// wakeEnv is the dependency-injection seam: production fills with
// scanActiveRuntimes + task.Ensure; tests fake both.
type wakeEnv struct {
	livenessEnv
	scan       func(livenessEnv) (runtimeStats, error)
	ensureSlug func(projectRoot, slug string) error
	mark       func(projectRoot, slug string)
}

func defaultWakeEnv() wakeEnv {
	return wakeEnv{
		livenessEnv: defaultLivenessEnv(),
		scan:        scanActiveRuntimes,
		ensureSlug: func(projectRoot, slug string) error {
			_, err := task.Ensure(filepath.Join(projectRoot, "tasks"), slug, nil)
			return err
		},
		mark: markWakePending,
	}
}

// Wake scans every active worker on the local host and re-mints the
// tmux session of any idle-with-unread-inbox worker that does not have
// a fresh wake-pending marker. Mirrors wt-task fleet wake.
//
// When onlySlug is non-empty, only that slug is considered; otherwise
// every idle-unread worker across configured projects is woken.
//
// Idle is the rt.state classifier (claude/codex/opencode pane at the
// agent's input prompt). The body of the inbox event is never typed
// into the tmux input - the worker picks it up via its Stop hook and
// the body-free re-mint just nudges the agent back into a turn.
func Wake(onlySlug string, stdout, stderr io.Writer) (int, error) {
	return runWake(onlySlug, stdout, stderr, defaultWakeEnv())
}

func runWake(onlySlug string, stdout, stderr io.Writer, e wakeEnv) (int, error) {
	stats, err := e.scan(e.livenessEnv)
	if err != nil {
		fmt.Fprintf(stderr, "spore: %v\n", err)
		return 1, err
	}

	woken := 0
	skippedPending := 0
	failed := 0
	var failedSlugs []string

	for _, rt := range stats.details {
		if onlySlug != "" && rt.slug != onlySlug {
			continue
		}
		if rt.state != "idle" || rt.unread == 0 {
			continue
		}
		if wakePendingState(rt.projectRoot, rt.slug, e.now()).fresh {
			skippedPending++
			continue
		}
		if err := e.ensureSlug(rt.projectRoot, rt.slug); err != nil {
			failed++
			failedSlugs = append(failedSlugs, rt.slug)
			fmt.Fprintf(stderr, "spore: fleet wake %s: %v\n", rt.slug, err)
			continue
		}
		woken++
		e.mark(rt.projectRoot, rt.slug)
	}

	if onlySlug != "" && woken == 0 && skippedPending == 0 && failed == 0 {
		fmt.Fprintf(stdout, "fleet wake: %s no idle unread worker\n", onlySlug)
		return 0, nil
	}
	if failed > 0 {
		fmt.Fprintf(stderr, "spore: fleet wake failed for %s\n", strings.Join(failedSlugs, ","))
		fmt.Fprintf(stdout, "fleet wake: woken=%d pending=%d failed=%d\n", woken, skippedPending, failed)
		return 2, nil
	}
	fmt.Fprintf(stdout, "fleet wake: woken=%d pending=%d\n", woken, skippedPending)
	return 0, nil
}
