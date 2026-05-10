package spawn

import (
	"context"
	"fmt"
	"os/exec"
	"strings"
)

// ExecTmux is the production Tmux implementation: shell out to the
// real `tmux` binary. WaitFor blocks the calling goroutine in a
// `tmux wait-for <chan>` subprocess; ctx cancellation kills the
// subprocess so the wrapper unwinds promptly on SIGTERM.
type ExecTmux struct{}

func (ExecTmux) Run(args ...string) error {
	out, err := exec.Command("tmux", args...).CombinedOutput()
	if err != nil {
		return fmt.Errorf("tmux %s: %v: %s", strings.Join(args, " "), err, strings.TrimSpace(string(out)))
	}
	return nil
}

func (ExecTmux) Output(args ...string) (string, error) {
	out, err := exec.Command("tmux", args...).Output()
	if err != nil {
		return "", err
	}
	return strings.TrimRight(string(out), "\n"), nil
}

func (ExecTmux) WaitFor(ctx context.Context, channel string) error {
	cmd := exec.CommandContext(ctx, "tmux", "wait-for", channel)
	// CombinedOutput would buffer; we want the process to just sit.
	if err := cmd.Run(); err != nil {
		// ctx cancellation kills the process; surface as nil so
		// the caller's signal-driven shutdown path runs cleanly.
		if ctx.Err() != nil {
			return nil
		}
		return err
	}
	return nil
}
