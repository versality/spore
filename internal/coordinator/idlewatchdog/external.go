package idlewatchdog

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// detectGitRoot returns the parent of `git rev-parse --git-common-dir`
// (the main worktree's root), or "" when not in a git repo.
func detectGitRoot() string {
	out, err := exec.Command("git", "rev-parse", "--git-common-dir").Output()
	if err != nil {
		return ""
	}
	common := strings.TrimSpace(string(out))
	if common == "" {
		return ""
	}
	if !filepath.IsAbs(common) {
		wd, _ := os.Getwd()
		common = filepath.Join(wd, common)
	}
	root := filepath.Dir(common)
	if !isDir(root) {
		return ""
	}
	return root
}

// runCapture wraps the configured Runner; returns stdout and the exit
// code (255 on spawn failure).
func runCapture(run Runner, name string, args ...string) (string, int) {
	stdout, code, err := run(name, args...)
	if err != nil {
		return "", 255
	}
	return stdout, code
}

// defaultRunner runs the named binary, captures combined stdout +
// stderr (matching bash `cmd 2>&1`), and returns the exit code.
func defaultRunner(name string, args ...string) (string, int, error) {
	out, err := exec.Command(name, args...).CombinedOutput()
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			return string(out), exitErr.ExitCode(), nil
		}
		return string(out), 255, err
	}
	return string(out), 0, nil
}

// defaultNotifier invokes the notify bin if it resolves on PATH;
// errors are swallowed so a failed notify never blocks findings.
func defaultNotifier(lookPath func(string) (string, error)) Notifier {
	return func(bin string, args []string) {
		if bin == "" {
			return
		}
		if _, err := lookPath(bin); err != nil {
			return
		}
		_ = exec.Command(bin, args...).Run()
	}
}

// pickEscalateBin walks the bash priority chain: skywarden,
// ntfy-push, then ssh-skywing-skywarden as a sentinel that triggers
// the ssh-to-skywing branch in defaultEscalator.
func pickEscalateBin(lookPath func(string) (string, error)) string {
	if p, err := lookPath("skywarden"); err == nil && p != "" {
		return p
	}
	if p, err := lookPath("ntfy-push"); err == nil && p != "" {
		return p
	}
	if _, err := lookPath("ssh"); err == nil {
		return "ssh-skywing-skywarden"
	}
	return ""
}

// defaultEscalator dispatches based on the bin's basename, mirroring
// the bash case statement. Errors are swallowed (bash uses `|| true`).
func defaultEscalator(bin, summary, body string, escalateAfter, startTimeout int) {
	if bin == "" {
		return
	}
	if startTimeout <= 0 {
		startTimeout = DefaultSkywardenStartTimeout
	}
	reason := fmt.Sprintf("skyhelm coordinator gate unresolved for %ds", escalateAfter)
	d := time.Duration(startTimeout) * time.Second
	switch filepath.Base(bin) {
	case "skywarden":
		runWithTimeout(d, bin, "ask",
			"--question", body,
			"--choice", "acknowledged",
			"--reason", reason,
			"--bot-id", "skyhelm",
			"--timeout", "300",
		)
	case "ssh-skywing-skywarden":
		runWithTimeout(d, "ssh",
			"-o", "BatchMode=yes",
			"-o", "ConnectTimeout=3",
			"skywing",
			"skywarden", "ask",
			"--question", body,
			"--choice", "acknowledged",
			"--reason", reason,
			"--bot-id", "skyhelm",
			"--timeout", "300",
		)
	case "ntfy-push":
		_ = exec.Command(bin, summary, "high").Run()
	default:
		_ = exec.Command(bin, summary).Run()
	}
}

func runWithTimeout(d time.Duration, name string, args ...string) {
	cmd := exec.Command(name, args...)
	if err := cmd.Start(); err != nil {
		return
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	select {
	case <-done:
	case <-time.After(d):
		_ = cmd.Process.Kill()
		<-done
	}
}
