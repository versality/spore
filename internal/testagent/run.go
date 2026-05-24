package testagent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"time"
)

type Options struct {
	Provider string
	Argv     []string
	Stdout   io.Writer
	Stderr   io.Writer
	Now      func() time.Time
}

func Run(ctx context.Context, opts Options) int {
	if opts.Stdout == nil {
		opts.Stdout = os.Stdout
	}
	if opts.Stderr == nil {
		opts.Stderr = os.Stderr
	}
	if opts.Now == nil {
		opts.Now = time.Now
	}
	mode := os.Getenv(EnvMode)
	if mode == "" {
		mode = ModeIdle
	}
	logPath := os.Getenv(EnvEventLog)
	if logPath == "" {
		fmt.Fprintln(opts.Stderr, "fake agent: SPORE_FAKE_AGENT_EVENT_LOG is required")
		return 2
	}
	log, err := os.OpenFile(logPath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		fmt.Fprintf(opts.Stderr, "fake agent: open event log: %v\n", err)
		return 2
	}
	defer log.Close()
	rec := recorder{w: log, now: opts.Now}
	if err := rec.event(Event{
		Type:     "start",
		Provider: opts.Provider,
		Mode:     mode,
		PID:      os.Getpid(),
		CWD:      cwd(),
		Argv:     append([]string(nil), opts.Argv...),
		Env:      selectedEnv(),
	}); err != nil {
		fmt.Fprintf(opts.Stderr, "fake agent: write event log: %v\n", err)
		return 2
	}
	recordLaunchContract(rec, opts.Provider, mode)
	if opts.Provider == "codex" {
		runCodexHooks(ctx, rec, "SessionStart")
	}
	switch mode {
	case ModeExitZero:
		runStopHooks(ctx, rec, opts.Provider)
		_ = rec.event(Event{Type: "stop", Provider: opts.Provider, Mode: mode})
		return 0
	case ModeExitNonzero:
		_ = rec.event(Event{Type: "error", Provider: opts.Provider, Mode: mode, Error: "exit-immediately-nonzero"})
		return 127
	case ModeCrashReady:
		touch(os.Getenv(EnvReadyFile))
		_ = rec.event(Event{Type: "ready", Provider: opts.Provider, Mode: mode})
		_ = rec.event(Event{Type: "error", Provider: opts.Provider, Mode: mode, Error: "crash-after-ready"})
		return 1
	case ModeHangReady:
		_ = rec.event(Event{Type: "hang", Provider: opts.Provider, Mode: mode, Message: "waiting without ready"})
		<-ctx.Done()
		_ = rec.event(Event{Type: "stop", Provider: opts.Provider, Mode: mode, Message: ctx.Err().Error()})
		return 0
	case ModeIdle:
		touch(os.Getenv(EnvReadyFile))
		_ = rec.event(Event{Type: "ready", Provider: opts.Provider, Mode: mode})
		<-ctx.Done()
		_ = rec.event(Event{Type: "stop", Provider: opts.Provider, Mode: mode, Message: ctx.Err().Error()})
		return 0
	case ModeProgress:
		touch(os.Getenv(EnvReadyFile))
		_ = rec.event(Event{Type: "ready", Provider: opts.Provider, Mode: mode})
		ticker := time.NewTicker(100 * time.Millisecond)
		defer ticker.Stop()
		i := 0
		for {
			select {
			case <-ctx.Done():
				_ = rec.event(Event{Type: "stop", Provider: opts.Provider, Mode: mode, Message: ctx.Err().Error()})
				return 0
			case <-ticker.C:
				i++
				fmt.Fprintf(opts.Stdout, "fake %s progress %d\n", opts.Provider, i)
				_ = rec.event(Event{Type: "progress", Provider: opts.Provider, Mode: mode, Message: strconv.Itoa(i)})
			}
		}
	case ModeWaitForFile:
		touch(os.Getenv(EnvReadyFile))
		_ = rec.event(Event{Type: "ready", Provider: opts.Provider, Mode: mode})
		waitPath := os.Getenv(EnvExitFile)
		if waitPath == "" {
			_ = rec.event(Event{Type: "error", Provider: opts.Provider, Mode: mode, Error: EnvExitFile + " is required for wait-for-file"})
			return 2
		}
		for {
			if _, err := os.Stat(waitPath); err == nil {
				_ = rec.event(Event{Type: "progress", Provider: opts.Provider, Mode: mode, Message: "sentinel-seen"})
				_ = rec.event(Event{Type: "stop", Provider: opts.Provider, Mode: mode})
				return 0
			}
			select {
			case <-ctx.Done():
				_ = rec.event(Event{Type: "stop", Provider: opts.Provider, Mode: mode, Message: ctx.Err().Error()})
				return 0
			case <-time.After(50 * time.Millisecond):
			}
		}
	case ModeOneTurn:
		touch(os.Getenv(EnvReadyFile))
		_ = rec.event(Event{Type: "ready", Provider: opts.Provider, Mode: mode})
		if opts.Provider == "codex" {
			runCodexHooks(ctx, rec, "PreToolUse")
		}
		fmt.Fprintf(opts.Stdout, "fake %s one turn\n", opts.Provider)
		_ = rec.event(Event{Type: "progress", Provider: opts.Provider, Mode: mode, Message: "one-turn"})
		runStopHooks(ctx, rec, opts.Provider)
		drainInbox(rec, opts.Provider, mode)
		_ = rec.event(Event{Type: "stop", Provider: opts.Provider, Mode: mode})
		return 0
	case ModeWorkThenExit:
		touch(os.Getenv(EnvReadyFile))
		_ = rec.event(Event{Type: "ready", Provider: opts.Provider, Mode: mode})
		if opts.Provider == "codex" {
			runCodexHooks(ctx, rec, "PreToolUse")
		}
		turns := turnLimit()
		for i := 1; i <= turns; i++ {
			fmt.Fprintf(opts.Stdout, "fake %s progress %d\n", opts.Provider, i)
			_ = rec.event(Event{Type: "progress", Provider: opts.Provider, Mode: mode, Message: strconv.Itoa(i)})
		}
		runStopHooks(ctx, rec, opts.Provider)
		drainInbox(rec, opts.Provider, mode)
		_ = rec.event(Event{Type: "stop", Provider: opts.Provider, Mode: mode})
		return 0
	default:
		msg := "unknown fake agent mode: " + mode
		_ = rec.event(Event{Type: "error", Provider: opts.Provider, Mode: mode, Error: msg})
		fmt.Fprintln(opts.Stderr, "fake agent: "+msg)
		return 2
	}
}

func runStopHooks(ctx context.Context, rec recorder, provider string) {
	switch provider {
	case "codex":
		runCodexHooks(ctx, rec, "Stop")
	case "claude":
		runClaudeHooks(ctx, rec, "Stop")
	}
}

func recordLaunchContract(rec recorder, provider, mode string) {
	env := selectedEnv()
	if env["WT_SESSION_KIND"] != "worker" {
		return
	}
	if env["SPORE_TASK_SLUG"] == "" {
		_ = rec.event(Event{
			Type:     "launch-contract-error",
			Provider: provider,
			Mode:     mode,
			Error:    "SPORE_TASK_SLUG is required for worker sessions",
		})
	}
	briefPath := env["SPORE_BRIEF_FILE"]
	fields := map[string]string{
		"brief_file": briefPath,
	}
	if briefPath == "" {
		_ = rec.event(Event{
			Type:     "launch-contract-warning",
			Provider: provider,
			Mode:     mode,
			Error:    "SPORE_BRIEF_FILE is not set",
			Fields:   fields,
		})
		return
	}
	body, err := os.ReadFile(briefPath)
	if err != nil {
		fields["brief_readable"] = "false"
		_ = rec.event(Event{
			Type:     "launch-contract-warning",
			Provider: provider,
			Mode:     mode,
			Error:    "SPORE_BRIEF_FILE is unreadable: " + err.Error(),
			Fields:   fields,
		})
		return
	}
	fields["brief_readable"] = "true"
	fields["brief_bytes"] = strconv.Itoa(len(body))
	if _, err := os.Stat(filepath.Join(cwd(), ".wt", "initial-prompt")); err == nil {
		fields["initial_prompt_exists"] = "true"
	} else {
		fields["initial_prompt_exists"] = "false"
	}
	_ = rec.event(Event{
		Type:     "launch-contract",
		Provider: provider,
		Mode:     mode,
		Fields:   fields,
	})
}

type recorder struct {
	w   io.Writer
	now func() time.Time
}

func (r recorder) event(e Event) error {
	e.Time = r.now().UTC()
	b, err := json.Marshal(e)
	if err != nil {
		return err
	}
	_, err = r.w.Write(append(b, '\n'))
	return err
}

func cwd() string {
	wd, err := os.Getwd()
	if err != nil {
		return ""
	}
	return wd
}

func selectedEnv() map[string]string {
	env := make(map[string]string, len(LaunchEnvKeys))
	for _, key := range LaunchEnvKeys {
		if value, ok := os.LookupEnv(key); ok {
			env[key] = value
		}
	}
	return env
}

func touch(path string) {
	if path == "" {
		return
	}
	_ = os.WriteFile(path, []byte("ready\n"), 0o644)
}

func turnLimit() int {
	raw := os.Getenv(EnvTurnLimit)
	if raw == "" {
		return 1
	}
	n, err := strconv.Atoi(raw)
	if err != nil || n < 1 {
		return 1
	}
	return n
}

func IsMode(mode string) bool {
	switch mode {
	case ModeIdle, ModeProgress, ModeWaitForFile, ModeOneTurn, ModeWorkThenExit,
		ModeExitZero, ModeExitNonzero, ModeCrashReady, ModeHangReady:
		return true
	default:
		return false
	}
}

var ErrMissingEventLog = errors.New("missing fake agent event log")
